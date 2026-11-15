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
