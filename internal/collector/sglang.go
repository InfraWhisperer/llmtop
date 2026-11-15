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

	// Cache hit rate (0.0-1.0 → 0-100%)
	if v, _, ok := pm.GetGaugeAny("sglang:cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
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
	p, g, okP, okG := sglangTokenCounters(pm)
	if prev != nil && prev.Online {
		if dt, ok := prevCounters.rateWindow(now); ok {
			if okP {
				m.PromptTokPerSec = max(0.0, (p-prevCounters.promptTokensTotal)/dt)
			}
			if okG {
				m.GenTokPerSec = max(0.0, (g-prevCounters.genTokensTotal)/dt)
			}
		}
	}

	return counterState{promptTokensTotal: p, genTokensTotal: g, ts: now}
}

// sglangTokenCounters returns summed prompt/generation token counters from pm
// and per-value found flags. Prefers sglang:realtime_tokens_total (updated
// every log interval; prefill = prefill_compute + prefill_cache, decode =
// generation), falling back to the finished-request counters summed across
// all is_streaming series. See issue #15.
func sglangTokenCounters(pm *metrics.ParsedMetrics) (prompt, gen float64, okP, okG bool) {
	if g, ok := pm.GetGaugeSum("sglang:realtime_tokens_total", map[string]string{"mode": "decode"}); ok {
		pc, _ := pm.GetGaugeSum("sglang:realtime_tokens_total", map[string]string{"mode": "prefill_compute"})
		pcache, _ := pm.GetGaugeSum("sglang:realtime_tokens_total", map[string]string{"mode": "prefill_cache"})
		return pc + pcache, g, true, true
	}
	prompt, okP = pm.GetGaugeSum("sglang:prompt_tokens_total", nil)
	gen, okG = pm.GetGaugeSum("sglang:generation_tokens_total", nil)
	return prompt, gen, okP, okG
}
