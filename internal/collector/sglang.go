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
	// patterns. We read every matching series and prefer the per-log-interval
	// sglang:realtime_tokens_total when present. Deltas are taken series by
	// series and clamped at 0 (see seriesDelta), so one restarted series cannot
	// cancel the growth of its siblings. dt comes from the timestamp stored with
	// the previous counter snapshot, not prev.LastSeen, which lags one poll
	// behind (see counterState.rateWindow).
	now := time.Now()
	c := sglangTokenCounters(pm)

	splitOK, haveWindow := c.split, false
	dt, windowOK := prevCounters.rateWindow(now)
	if prev != nil && prev.Online && windowOK {
		haveWindow = true
		if c.okPrompt {
			m.PromptTokPerSec = seriesRate(prevCounters.promptSeries, c.promptSeries, prevCounters.promptTokensTotal, c.prompt, dt)
			// Tokens reused from the radix cache: an accounting number, not a
			// compute rate. Kept in its own field so the header never renders
			// it as generation (issue #15).
			cache := seriesRate(prevCounters.prefillCacheSeries, c.cacheSeries, prevCounters.prefillCacheTokensTotal, c.prefillCache, dt)
			m.PrefillCacheTokPerSec = cache
			m.PrefillComputeTokPerSec = max(0.0, m.PromptTokPerSec-cache)
		}
		if c.okGen {
			m.GenTokPerSec = seriesRate(prevCounters.genSeries, c.genSeries, prevCounters.genTokensTotal, c.gen, dt)
		}
	}

	// Cache hit rate. sglang:cache_hit_rate is an instantaneous gauge that
	// SGLang only sets on iterations with a new prefill batch, so it reads 0
	// during idle and decode-only windows — the same instantaneous-source trap
	// as the tok/s bug (issue #15), on the hit-rate axis. Derive from the
	// cumulative realtime counters when available (same lifetime semantics as
	// vLLM's hits_total/queries_total), falling back to the gauge; the
	// windowed delta feeds the detail row.
	applySGLangCacheHit(m, pm, c, prevCounters, splitOK, haveWindow)

	counters := counterState{
		promptSeries:            c.promptSeries,
		promptTokensTotal:       c.prompt,
		genSeries:               c.genSeries,
		genTokensTotal:          c.gen,
		prefillCacheSeries:      c.cacheSeries,
		prefillCacheTokensTotal: c.prefillCache,
		ts:                      now,
	}
	return counters
}

// applySGLangCacheHit fills CacheHitRatePct (lifetime-cumulative, header
// semantics) and CacheHitWindowPct (last-window delta, detail-row semantics).
// haveWindow is false on the first poll or when the rate window is unusable.
func applySGLangCacheHit(m *metrics.WorkerMetrics, pm *metrics.ParsedMetrics, c sglangCounterRead, prevCounters counterState, splitOK, haveWindow bool) {
	if c.split && c.prompt > 0 {
		m.CacheHitRatePct = clampPct(c.prefillCache / c.prompt * 100)
	} else if v, _, ok := pm.GetGaugeAny("sglang:cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	}
	if splitOK && haveWindow {
		dPrompt := seriesDelta(prevCounters.promptSeries, c.promptSeries, prevCounters.promptTokensTotal, c.prompt)
		dCache := seriesDelta(prevCounters.prefillCacheSeries, c.cacheSeries, prevCounters.prefillCacheTokensTotal, c.prefillCache)
		if dPrompt > 0 && dCache <= dPrompt {
			m.CacheHitWindowPct = clampPct(dCache / dPrompt * 100)
		}
	}
}

// sglangCounterRead is one poll's SGLang token counters: summed values plus the
// per-series breakdown the next poll deltas against.
type sglangCounterRead struct {
	prompt, gen, prefillCache float64
	promptSeries              map[string]float64
	genSeries                 map[string]float64
	cacheSeries               map[string]float64
	okPrompt, okGen           bool
	// split is true when the prompt side came from realtime_tokens_total, i.e.
	// the compute/cache halves are known separately.
	split bool
}

// sglangTokenCounters reads SGLang's token counters, preferring
// sglang:realtime_tokens_total (updated every log interval; prompt =
// prefill_compute + prefill_cache, decode = generation) and falling back to the
// finished-request counters summed across all is_streaming series — that
// fallback has no cache split, so it reports prefillCache=0, split=false.
// See issue #15.
func sglangTokenCounters(pm *metrics.ParsedMetrics) sglangCounterRead {
	decode, decodeSum, okDecode := pm.GaugeSeriesFiltered("sglang:realtime_tokens_total", map[string]string{"mode": "decode"})
	if okDecode {
		compute, computeSum, _ := pm.GaugeSeriesFiltered("sglang:realtime_tokens_total", map[string]string{"mode": "prefill_compute"})
		cache, cacheSum, _ := pm.GaugeSeriesFiltered("sglang:realtime_tokens_total", map[string]string{"mode": "prefill_cache"})
		promptSeries, promptSum, _ := metrics.MergeGaugeSeries(compute, computeSum, cache, cacheSum)
		return sglangCounterRead{
			prompt: promptSum, gen: decodeSum, prefillCache: cacheSum,
			promptSeries: promptSeries, genSeries: decode, cacheSeries: cache,
			okPrompt: true, okGen: true, split: true,
		}
	}
	promptSeries, promptSum, okP := pm.GaugeSeries("sglang:prompt_tokens_total", nil)
	genSeries, genSum, okG := pm.GaugeSeries("sglang:generation_tokens_total", nil)
	return sglangCounterRead{
		prompt: promptSum, gen: genSum, prefillCache: 0,
		promptSeries: promptSeries, genSeries: genSeries,
		okPrompt: okP, okGen: okG, split: false,
	}
}

// clampPct keeps a derived ratio inside the 0-100 display range despite
// counter skew (e.g. cached_tokens_total lagging prompt_tokens_total by one
// log interval).
func clampPct(v float64) float64 {
	return min(100.0, max(0.0, v))
}
