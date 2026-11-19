package collector

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func approxEqual(a, b, tol float64) bool {
	return math.Abs(a-b) < tol
}

const sampleSGLangMetrics = `# HELP sglang:num_running_reqs Number of requests currently running.
# TYPE sglang:num_running_reqs gauge
sglang:num_running_reqs{model_name="Qwen/Qwen2.5-72B-Instruct"} 8.0
# HELP sglang:num_waiting_reqs Number of requests waiting in the queue.
# TYPE sglang:num_waiting_reqs gauge
sglang:num_waiting_reqs{model_name="Qwen/Qwen2.5-72B-Instruct"} 3.0
# HELP sglang:token_usage Token usage (KV cache utilization).
# TYPE sglang:token_usage gauge
sglang:token_usage{model_name="Qwen/Qwen2.5-72B-Instruct"} 0.58
# HELP sglang:cache_hit_rate Prefix cache hit rate.
# TYPE sglang:cache_hit_rate gauge
sglang:cache_hit_rate{model_name="Qwen/Qwen2.5-72B-Instruct"} 0.32
# HELP sglang:time_to_first_token_seconds Histogram of time to first token.
# TYPE sglang:time_to_first_token_seconds histogram
sglang:time_to_first_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="0.01"} 3
sglang:time_to_first_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="0.05"} 12
sglang:time_to_first_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="0.1"} 18
sglang:time_to_first_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="0.5"} 20
sglang:time_to_first_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="+Inf"} 20
sglang:time_to_first_token_seconds_sum{model_name="Qwen/Qwen2.5-72B-Instruct"} 0.95
sglang:time_to_first_token_seconds_count{model_name="Qwen/Qwen2.5-72B-Instruct"} 20
# HELP sglang:time_per_output_token_seconds Histogram of inter-token latency.
# TYPE sglang:time_per_output_token_seconds histogram
sglang:time_per_output_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="0.005"} 8
sglang:time_per_output_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="0.01"} 25
sglang:time_per_output_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="0.05"} 38
sglang:time_per_output_token_seconds_bucket{model_name="Qwen/Qwen2.5-72B-Instruct",le="+Inf"} 40
sglang:time_per_output_token_seconds_sum{model_name="Qwen/Qwen2.5-72B-Instruct"} 0.52
sglang:time_per_output_token_seconds_count{model_name="Qwen/Qwen2.5-72B-Instruct"} 40
# HELP sglang:prompt_tokens_total Total prompt tokens processed.
# TYPE sglang:prompt_tokens_total counter
sglang:prompt_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct"} 8500
# HELP sglang:generation_tokens_total Total generation tokens produced.
# TYPE sglang:generation_tokens_total counter
sglang:generation_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct"} 22000
`

func TestParseSGLangMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}

	parseSGLangMetrics(m, nil, counterState{}, pm)

	if m.RequestsRunning != 8 {
		t.Errorf("RequestsRunning = %d, want 8", m.RequestsRunning)
	}
	if m.RequestsWaiting != 3 {
		t.Errorf("RequestsWaiting = %d, want 3", m.RequestsWaiting)
	}
	if !approxEqual(m.KVCacheUsagePct, 58, 0.01) {
		t.Errorf("KVCacheUsagePct = %f, want 58", m.KVCacheUsagePct)
	}
	if !approxEqual(m.CacheHitRatePct, 32, 0.01) {
		t.Errorf("CacheHitRatePct = %f, want 32", m.CacheHitRatePct)
	}
	if m.TTFT_P50 <= 0 {
		t.Errorf("TTFT_P50 = %f, want > 0", m.TTFT_P50)
	}
	if m.TTFT_P99 <= 0 {
		t.Errorf("TTFT_P99 = %f, want > 0", m.TTFT_P99)
	}
	if m.ITL_P50 <= 0 {
		t.Errorf("ITL_P50 = %f, want > 0", m.ITL_P50)
	}
	if m.ITL_P99 <= 0 {
		t.Errorf("ITL_P99 = %f, want > 0", m.ITL_P99)
	}
}

// Issue #15: sglang:{prompt,generation}_tokens_total carry an is_streaming
// label → 2 series. GetGaugeAny returned only the first one; if the frozen
// is_streaming="false" series came first, tokens/s showed a permanent 0.
// All matching series must be summed before taking the delta.
const sampleSGLangMetricsMultiSeries = `# TYPE sglang:num_running_reqs gauge
sglang:num_running_reqs{model_name="Qwen/Qwen2.5-72B-Instruct"} 8.0
# TYPE sglang:prompt_tokens_total counter
sglang:prompt_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="false"} 6000
sglang:prompt_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="true"} 2500
# TYPE sglang:generation_tokens_total counter
sglang:generation_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="false"} 15000
sglang:generation_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="true"} 7000
`

// Issue #15: *_tokens_total only increment when a request finishes, producing
// zeros + spikes during long generations. sglang:realtime_tokens_total is
// updated every log interval and must be preferred when present.
const sampleSGLangMetricsRealtime = `# TYPE sglang:num_running_reqs gauge
sglang:num_running_reqs{model_name="Qwen/Qwen2.5-72B-Instruct"} 8.0
# TYPE sglang:realtime_tokens_total counter
sglang:realtime_tokens_total{model_name="m",mode="prefill_compute"} 600
sglang:realtime_tokens_total{model_name="m",mode="prefill_cache"} 100
sglang:realtime_tokens_total{model_name="m",mode="decode"} 3000
# TYPE sglang:prompt_tokens_total counter
sglang:prompt_tokens_total{model_name="m",is_streaming="false"} 500
# TYPE sglang:generation_tokens_total counter
sglang:generation_tokens_total{model_name="m",is_streaming="false"} 2000
`

// sglangStreamText is a previous-poll exposition for the finished-request
// counters, so rate tests carry a real per-series baseline (the collector keys
// its snapshots by label set).
func sglangStreamText(promptFalse, promptTrue, genFalse, genTrue float64) string {
	return fmt.Sprintf(`sglang:prompt_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="false"} %v
sglang:prompt_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="true"} %v
sglang:generation_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="false"} %v
sglang:generation_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct",is_streaming="true"} %v
`, promptFalse, promptTrue, genFalse, genTrue)
}

// sglangRealtimeText is the same for the realtime counters, whose label sets also
// carry the mode that splits prompt work into compute and cache.
func sglangRealtimeText(compute, cache, decode float64) string {
	return fmt.Sprintf(`sglang:realtime_tokens_total{model_name="m",mode="prefill_compute"} %v
sglang:realtime_tokens_total{model_name="m",mode="prefill_cache"} %v
sglang:realtime_tokens_total{model_name="m",mode="decode"} %v
`, compute, cache, decode)
}

// sglangMonoText is a previous-poll exposition for backends that export the
// finished-request counters as one series per model (no is_streaming label), so
// the series keys line up with sampleSGLangMetrics.
func sglangMonoText(prompt, gen float64) string {
	return fmt.Sprintf(`sglang:prompt_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct"} %v
sglang:generation_tokens_total{model_name="Qwen/Qwen2.5-72B-Instruct"} %v
`, prompt, gen)
}

func TestSGLangRate_MultiSeriesSum(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetricsMultiSeries)
	prev := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)}
	prevCounters := prevSnapshot(t, parseSGLangMetrics,
		sglangStreamText(5000, 2500, 13000, 7000), 2*time.Second) // Σ7500 / Σ20000
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)
	// summed prompt = 6000+2500 = 8500, delta 1000 over ~2s = ~500 tok/s
	if m.PromptTokPerSec < 450 || m.PromptTokPerSec > 550 {
		t.Errorf("PromptTokPerSec = %f, want ~500 (multi-series sum)", m.PromptTokPerSec)
	}
	// summed gen = 15000+7000 = 22000, delta 2000 over ~2s = ~1000 tok/s
	if m.GenTokPerSec < 900 || m.GenTokPerSec > 1100 {
		t.Errorf("GenTokPerSec = %f, want ~1000 (multi-series sum)", m.GenTokPerSec)
	}
}

func TestSGLangRate_PrefersRealtimeTokens(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetricsRealtime)
	prev := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)}
	prevCounters := prevSnapshot(t, parseSGLangMetrics, sglangRealtimeText(400, 100, 2000), 2*time.Second)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)
	// realtime prompt = prefill_compute(600)+prefill_cache(100) = 700, delta 200/2s = 100
	if m.PromptTokPerSec < 90 || m.PromptTokPerSec > 110 {
		t.Errorf("PromptTokPerSec = %f, want ~100 (realtime_tokens_total)", m.PromptTokPerSec)
	}
	// realtime decode = 3000, delta 1000/2s = 500
	if m.GenTokPerSec < 450 || m.GenTokPerSec > 550 {
		t.Errorf("GenTokPerSec = %f, want ~500 (realtime_tokens_total)", m.GenTokPerSec)
	}
}

func TestSGLangRate_FallsBackWhenNoRealtime(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetricsMultiSeries)
	prev := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)}
	prevCounters := prevSnapshot(t, parseSGLangMetrics,
		sglangStreamText(5500, 2500, 14000, 7000), 2*time.Second) // Σ8000 / Σ21000
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)
	// fallback summed prompt 8500-8000=500/2s=250; gen 22000-21000=1000/2s=500
	if m.PromptTokPerSec < 225 || m.PromptTokPerSec > 275 {
		t.Errorf("PromptTokPerSec = %f, want ~250 (fallback)", m.PromptTokPerSec)
	}
	if m.GenTokPerSec < 450 || m.GenTokPerSec > 550 {
		t.Errorf("GenTokPerSec = %f, want ~500 (fallback)", m.GenTokPerSec)
	}
}

// Issue #15 cause 3: dt must come from the timestamp stored with the previous
// counter snapshot, not prev.LastSeen (which lags one extra poll behind
// prevCounters, halving every rate).
func TestSGLangRate_UsesCounterTimestamp(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetrics)
	prev := &metrics.WorkerMetrics{
		Online:   true,
		LastSeen: time.Now().Add(-4 * time.Second), // stale: one extra poll behind
	}
	// Actual counter age is 2s, not the stale LastSeen.
	prevCounters := prevSnapshot(t, parseSGLangMetrics, sglangMonoText(7500, 20000), 2*time.Second)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)
	// delta 1000/2000 over 2s (ts), not 4s (LastSeen): ~500 / ~1000 tok/s.
	if m.PromptTokPerSec < 450 || m.PromptTokPerSec > 550 {
		t.Errorf("PromptTokPerSec = %f, want ~500 (dt from counter ts)", m.PromptTokPerSec)
	}
	if m.GenTokPerSec < 900 || m.GenTokPerSec > 1100 {
		t.Errorf("GenTokPerSec = %f, want ~1000 (dt from counter ts)", m.GenTokPerSec)
	}
}

// Issue #15: prefill_cache tokens are reused from the radix cache, not
// computed. Reporting them inside the prompt/gen rate is what produced the
// 25k+ tok/s spikes, so they must land in their own field and be subtracted
// from the prefill-compute rate.
func TestSGLangRate_PrefillCacheSplit(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetricsRealtime)
	prev := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)}
	prevCounters := prevSnapshot(t, parseSGLangMetrics, sglangRealtimeText(550, 50, 2000), 2*time.Second)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)

	// prompt = (600+100)-600 = 100 over 2s = ~50 tok/s
	if m.PromptTokPerSec < 45 || m.PromptTokPerSec > 55 {
		t.Errorf("PromptTokPerSec = %f, want ~50", m.PromptTokPerSec)
	}
	// cache = 100-50 = 50 over 2s = ~25 tok/s, kept out of the compute rate
	if m.PrefillCacheTokPerSec < 22.5 || m.PrefillCacheTokPerSec > 27.5 {
		t.Errorf("PrefillCacheTokPerSec = %f, want ~25", m.PrefillCacheTokPerSec)
	}
	if m.PrefillComputeTokPerSec < 22.5 || m.PrefillComputeTokPerSec > 27.5 {
		t.Errorf("PrefillComputeTokPerSec = %f, want ~25 (prompt-cache)", m.PrefillComputeTokPerSec)
	}
	// The bug: gen must stay the decode rate, unpolluted by the cache burst.
	if m.GenTokPerSec < 450 || m.GenTokPerSec > 550 {
		t.Errorf("GenTokPerSec = %f, want ~500 (decode only)", m.GenTokPerSec)
	}
	// Re-derivable invariant: prompt == compute + cache.
	if !approxEqual(m.PromptTokPerSec, m.PrefillComputeTokPerSec+m.PrefillCacheTokPerSec, 1.0) {
		t.Errorf("prompt %f != compute %f + cache %f",
			m.PromptTokPerSec, m.PrefillComputeTokPerSec, m.PrefillCacheTokPerSec)
	}
}

// The finished-request fallback has no cache split at all, so it must report
// zero cache tokens and leave the whole prompt side as compute rather than
// inventing one.
func TestSGLangRate_FallbackHasNoCacheSplit(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetricsMultiSeries)
	prev := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)}
	prevCounters := prevSnapshot(t, parseSGLangMetrics, sglangStreamText(5500, 2500, 14000, 7000), 2*time.Second)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)

	if m.PrefillCacheTokPerSec != 0 {
		t.Errorf("PrefillCacheTokPerSec = %f, want 0 (fallback has no split)", m.PrefillCacheTokPerSec)
	}
	if !approxEqual(m.PrefillComputeTokPerSec, m.PromptTokPerSec, 0.001) {
		t.Errorf("PrefillComputeTokPerSec = %f, want == PromptTokPerSec %f", m.PrefillComputeTokPerSec, m.PromptTokPerSec)
	}
}

// sglang:cache_hit_rate is an instantaneous gauge only set on iterations with
// a new prefill batch, so it reads 0 during idle/decode-only windows while the
// cumulative counters show ~89 % reuse (issue #15, hit-rate axis). With the
// realtime counters present they must win over the gauge, with lifetime and
// windowed values reported per the header/detail-row split.
func TestSGLangCacheHit_CountersBeatGauge(t *testing.T) {
	body := sampleSGLangMetricsRealtime + "# TYPE sglang:cache_hit_rate gauge\nsglang:cache_hit_rate{model_name=\"m\"} 0.0\n"
	pm := metrics.ParseText(body)
	prev := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)}
	prevCounters := prevSnapshot(t, parseSGLangMetrics, sglangRealtimeText(550, 50, 2000), 2*time.Second)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)

	// Lifetime: cache 100 / prompt 700 = 14.29 %.
	if !approxEqual(m.CacheHitRatePct, 100.0/7.0, 0.01) {
		t.Errorf("CacheHitRatePct = %f, want %f", m.CacheHitRatePct, 100.0/7.0)
	}
	// Window: Δcache 50 / Δprompt 100 = 50 %.
	if !approxEqual(m.CacheHitWindowPct, 50, 0.01) {
		t.Errorf("CacheHitWindowPct = %f, want 50", m.CacheHitWindowPct)
	}
}

// Without a cache split (finished-request fallback) the gauge remains the only
// source, and no windowed value may be invented from it.
func TestSGLangCacheHit_GaugeFallbackNoSplit(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetrics)
	prev := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)}
	prevCounters := prevSnapshot(t, parseSGLangMetrics, sglangStreamText(5500, 2500, 14000, 7000), 2*time.Second)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)

	if !approxEqual(m.CacheHitRatePct, 32, 0.01) {
		t.Errorf("CacheHitRatePct = %f, want 32 (gauge fallback)", m.CacheHitRatePct)
	}
	if m.CacheHitWindowPct != 0 {
		t.Errorf("CacheHitWindowPct = %f, want 0 (no split, no window)", m.CacheHitWindowPct)
	}
}

// A window with no new prompt work (0/0 while idle) or a counter reset
// (negative delta after a server restart) must leave the windowed value unset
// rather than showing a fake 0 % or a negative rate.
func TestSGLangCacheHit_IdleAndReset(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetricsRealtime)
	online := &metrics.WorkerMetrics{Online: true}

	idle := &metrics.WorkerMetrics{Endpoint: "http://x", Online: true}
	parseSGLangMetrics(idle, online,
		prevSnapshot(t, parseSGLangMetrics, sglangRealtimeText(600, 100, 3000), 2*time.Second), pm)
	if idle.CacheHitWindowPct != 0 {
		t.Errorf("idle CacheHitWindowPct = %f, want 0", idle.CacheHitWindowPct)
	}

	reset := &metrics.WorkerMetrics{Endpoint: "http://x", Online: true}
	parseSGLangMetrics(reset, online,
		prevSnapshot(t, parseSGLangMetrics, sglangRealtimeText(70000, 10000, 30000), 2*time.Second), pm)
	if reset.CacheHitWindowPct != 0 {
		t.Errorf("reset CacheHitWindowPct = %f, want 0", reset.CacheHitWindowPct)
	}
	// Lifetime still reflects the fresh cumulative counters.
	if !approxEqual(reset.CacheHitRatePct, 100.0/7.0, 0.01) {
		t.Errorf("reset CacheHitRatePct = %f, want %f", reset.CacheHitRatePct, 100.0/7.0)
	}
}

func TestDetectSGLang(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetrics)
	p := &sglangParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendSGLang {
		t.Errorf("backend = %s, want SGLang", backend)
	}
	if model != "Qwen/Qwen2.5-72B-Instruct" {
		t.Errorf("model = %s, want Qwen/Qwen2.5-72B-Instruct", model)
	}
}

func TestSGLangCounterState(t *testing.T) {
	_ = counterState{ts: time.Now()} // ts field must exist (issue #15)
	pm := metrics.ParseText(sampleSGLangMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}

	counters := parseSGLangMetrics(m, nil, counterState{}, pm)

	// Counter values must NOT leak into WorkerMetrics fields
	if m.StoreSizeBytes != 0 {
		t.Errorf("StoreSizeBytes = %f, want 0 (counters should not leak)", m.StoreSizeBytes)
	}
	if m.EvictionTotal != 0 {
		t.Errorf("EvictionTotal = %f, want 0 (counters should not leak)", m.EvictionTotal)
	}
	// The snapshot must carry the poll timestamp so the next poll computes
	// dt from it (issue #15).
	if counters.ts.IsZero() {
		t.Error("counters.ts is zero; want poll timestamp")
	}
	// Counter values should be in the returned counterState
	if counters.promptTokensTotal != 8500 {
		t.Errorf("counters.promptTokensTotal = %f, want 8500", counters.promptTokensTotal)
	}
	if counters.genTokensTotal != 22000 {
		t.Errorf("counters.genTokensTotal = %f, want 22000", counters.genTokensTotal)
	}
}

func TestSGLangRateCalculation(t *testing.T) {
	pm := metrics.ParseText(sampleSGLangMetrics)

	prev := &metrics.WorkerMetrics{
		Online:   true,
		LastSeen: time.Now().Add(-2 * time.Second),
	}
	// As of issue #15, dt comes from prevCounters.ts; LastSeen is stale by design.
	prevCounters := prevSnapshot(t, parseSGLangMetrics, sglangMonoText(7500, 20000), 2*time.Second)

	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:30000", Online: true}
	parseSGLangMetrics(m, prev, prevCounters, pm)

	// prompt delta = 8500 - 7500 = 1000 over ~2s = ~500 tok/s
	if m.PromptTokPerSec < 450 || m.PromptTokPerSec > 550 {
		t.Errorf("PromptTokPerSec = %f, want ~500", m.PromptTokPerSec)
	}
	// gen delta = 22000 - 20000 = 2000 over ~2s = ~1000 tok/s
	if m.GenTokPerSec < 900 || m.GenTokPerSec > 1100 {
		t.Errorf("GenTokPerSec = %f, want ~1000", m.GenTokPerSec)
	}
}
