package collector

import (
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type sglangParser struct{}

func init() {
	RegisterParser(metrics.BackendSGLang, &sglangParser{})
	detectors = append(detectors, &sglangParser{})
}

func (p *sglangParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 7 && s.Name[:7] == "sglang:" {
			return metrics.BackendSGLang, s.Labels["model_name"]
		}
	}
	for _, h := range pm.Histograms {
		if len(h.Name) >= 7 && h.Name[:7] == "sglang:" {
			return metrics.BackendSGLang, h.Labels["model_name"]
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *sglangParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseSGLangMetrics(m, prev, prevCounters, pm)
}

// parseSGLangMetrics extracts SGLang-specific metrics from the parsed Prometheus data.
func parseSGLangMetrics(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	// Running requests
	if v, _, ok := pm.GetGaugeAny("sglang:num_running_reqs"); ok {
		m.RequestsRunning = int(v)
	}

	// Waiting requests (queue depth)
	if v, _, ok := pm.GetGaugeAny("sglang:num_waiting_reqs"); ok {
		m.RequestsWaiting = int(v)
	}

	// Token usage (KV cache utilization, 0.0-1.0 → 0-100%)
	if v, _, ok := pm.GetGaugeAny("sglang:token_usage"); ok {
		m.KVCacheUsagePct = v * 100
	}



	// TTFT/ITL: populate scalar fields and LatencySnapshots in one pass.
	populateLatency(m, pm,
		"sglang:time_to_first_token_seconds",
		[]string{"sglang:time_per_output_token_seconds"},
	)

	// Token throughput rates (issue #15).
	//
	// sglang:{prompt,generation}_tokens_total carry an is_streaming label
	// (2 series) and only increment when a request finishes, so taking the
	// first series froze rates at 0 and long generations showed zero+spike
	// patterns. We sum all matching series and prefer the per-log-interval
	// sglang:realtime_tokens_total when present. dt comes from the timestamp
	// stored with the previous counter snapshot, not prev.LastSeen, which
	// lags one poll behind (see counterState.rateWindow).
	now := time.Now()
	p, g, pc, okP, okG, split := sglangTokenCounters(pm)

	splitOK, haveWindow := split, false
	if prev != nil && prev.Online {
		if dt, ok := prevCounters.rateWindow(now); ok {
			haveWindow = true
			if okP {
				m.PromptTokPerSec = max(0.0, (p-prevCounters.promptTokensTotal)/dt)
				// Tokens reused from the radix cache: an accounting number, not a
				// compute rate. Kept in its own field so the header never renders
				// it as generation (issue #15).
				cache := max(0.0, (pc-prevCounters.prefillCacheTokensTotal)/dt)
				m.PrefillCacheTokPerSec = cache
				m.PrefillComputeTokPerSec = max(0.0, m.PromptTokPerSec-cache)
			}
			if okG {
				m.GenTokPerSec = max(0.0, (g-prevCounters.genTokensTotal)/dt)
			}
		}
	}

	// Cache hit rate. sglang:cache_hit_rate is an instantaneous gauge that
	// SGLang only sets on iterations with a new prefill batch, so it reads 0
	// during idle and decode-only windows — the same instantaneous-source trap
	// as the tok/s bug (issue #15), on the hit-rate axis. Derive from the
	// cumulative realtime counters when available (same lifetime semantics as
	// vLLM's hits_total/queries_total), falling back to the gauge; the
	// windowed delta feeds the detail row.
	applySGLangCacheHit(m, pm, p, pc, split, prevCounters, splitOK, haveWindow)

	return counterState{
		promptTokensTotal:       p,
		genTokensTotal:          g,
		prefillCacheTokensTotal: pc,
		ts:                      now,
	}
}

// applySGLangCacheHit fills CacheHitRatePct (lifetime-cumulative, header
// semantics) and CacheHitWindowPct (last-window delta, detail-row semantics).
// haveWindow is false on the first poll or when the rate window is unusable.
func applySGLangCacheHit(m *metrics.WorkerMetrics, pm *metrics.ParsedMetrics, p, pc float64, split bool, prevCounters counterState, splitOK, haveWindow bool) {
	if split && p > 0 {
		m.CacheHitRatePct = clampPct(pc / p * 100)
	} else if v, _, ok := pm.GetGaugeAny("sglang:cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	}
	if splitOK && haveWindow {
		dPrompt := p - prevCounters.promptTokensTotal
		dCache := pc - prevCounters.prefillCacheTokensTotal
		if dPrompt > 0 && dCache >= 0 {
			m.CacheHitWindowPct = clampPct(dCache / dPrompt * 100)
		}
	}
}

// sglangTokenCounters returns summed prompt/generation token counters, plus the
// raw prefix-cache token counter behind the prompt side, per-value found flags,
// and whether a cache split is available. Prefers sglang:realtime_tokens_total
// (updated every log interval; prompt = prefill_compute + prefill_cache, decode
// = generation), falling back to the finished-request counters summed across
// all is_streaming series — that fallback has no cache split, so it reports
// pc=0, split=false. See issue #15.
func sglangTokenCounters(pm *metrics.ParsedMetrics) (prompt, gen, prefillCache float64, okP, okG, split bool) {
	if g, ok := pm.GetGaugeSum("sglang:realtime_tokens_total", map[string]string{"mode": "decode"}); ok {
		pc, _ := pm.GetGaugeSum("sglang:realtime_tokens_total", map[string]string{"mode": "prefill_compute"})
		pcache, _ := pm.GetGaugeSum("sglang:realtime_tokens_total", map[string]string{"mode": "prefill_cache"})
		return pc + pcache, g, pcache, true, true, true
	}
	prompt, okP = pm.GetGaugeSum("sglang:prompt_tokens_total", nil)
	gen, okG = pm.GetGaugeSum("sglang:generation_tokens_total", nil)
	return prompt, gen, 0, okP, okG, false
}

// clampPct keeps a derived ratio inside the 0-100 display range despite
// counter skew (e.g. cached_tokens_total lagging prompt_tokens_total by one
// log interval).
func clampPct(v float64) float64 {
	return min(100.0, max(0.0, v))
}
