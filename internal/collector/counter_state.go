package collector

import "time"

// counterState holds raw Prometheus counter values for rate computation.
// These are internal to the collector and never exported or serialized.
// Previously, these values were aliased onto WorkerMetrics.StoreSizeBytes
// and WorkerMetrics.EvictionTotal, which corrupted JSON export output
// for non-LMCache backends.
//
// All three rate-computing backends (vLLM, SGLang, NIM) use the same
// two counters: prompt_tokens_total and generation_tokens_total.
//
// tenantReqTotals retains the previous-poll vllm:request_success_total
// counter per request_tag value. It is consumed by the model_group
// aggregation layer (which currently relies on raw counters, not rates),
// but kept here so future rate computation has a stable place to live
// without churning unrelated parsers.
type counterState struct {
	promptTokensTotal float64
	genTokensTotal    float64
	// Raw counter value behind PrefillCacheTokPerSec, tracked separately so a
	// prefix-cache burst never contaminates the prefill-compute rate (issue #15).
	prefillCacheTokensTotal float64
	preemptionsTotal        float64
	tenantReqTotals         map[string]float64
	// Per-series previous values for the multi-series counters, keyed by
	// metrics' canonical label-set key (see seriesDelta). Backends whose
	// counters fan out over labels (vLLM per model_name/engine, SGLang per
	// is_streaming/mode, NIM and LiteLLM per model) keep their baseline here so
	// one restarted series cannot cancel the growth of its siblings; single-series
	// backends (TGI, Triton) only use the scalar totals above.
	promptSeries        map[string]float64
	genSeries           map[string]float64
	preemptSeries       map[string]float64
	prefillCacheSeries  map[string]float64
	prefixHitsSeries    map[string]float64
	prefixQueriesSeries map[string]float64
	// ts is when these counter values were sampled. Rate deltas MUST be
	// divided by time.Since(ts), not time.Since(prev.LastSeen): in pollWorker
	// the prev snapshot lags prevCounters by one poll (prev is N-2 while
	// counters are N-1), so LastSeen-based dt doubles dt and halves every
	// rate (issue #15, cause 3).
	ts time.Time
}

// rateWindow returns the seconds between the previous counter snapshot and
// now, and whether a usable window exists (zero on first poll after startup
// or after a worker went offline).
func (cs counterState) rateWindow(now time.Time) (float64, bool) {
	if cs.ts.IsZero() {
		return 0, false
	}
	dt := now.Sub(cs.ts).Seconds()
	return dt, dt > 0
}

// seriesDelta returns the total tokens added since the previous snapshot for one
// counter, taken series by series with each delta clamped at zero.
//
// The summed form (Σcur − Σprev) is what issue #15 replaced GetGaugeAny with,
// and it has its own failure mode: if 1 of N series restarts (a vLLM engine
// coming back at 0 while the others keep counting), the negative series cancels
// the positive ones and the whole window collapses to 0, hiding real throughput.
// Clamping per series loses only the restarted series — the same blast radius a
// per-series rate() has.
//
// When the current read has no per-series breakdown (nil values) the clamped
// delta of the scalar totals is all there is — that is the single-series path
// used by TGI and Triton.
func seriesDelta(prevSeries, curSeries map[string]float64, prevTotal, curTotal float64) float64 {
	if len(curSeries) == 0 {
		return max(0.0, curTotal-prevTotal)
	}
	var sum float64
	for k, v := range curSeries {
		if p, ok := prevSeries[k]; ok && v > p {
			sum += v - p
		}
	}
	return sum
}

// seriesRate is seriesDelta expressed per second over the counter window.
func seriesRate(prevSeries, curSeries map[string]float64, prevTotal, curTotal float64, dt float64) float64 {
	if dt <= 0 {
		return 0
	}
	return seriesDelta(prevSeries, curSeries, prevTotal, curTotal) / dt
}
