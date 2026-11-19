package collector

import (
	"math"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// vLLM fans its token and prefix-cache counters out per model_name (and per
// engine in V1). Issue #15 replaced the single-series reads with per-series
// reads; these tests lock in that rates are taken series by series, over the
// counter sample window.
const sampleVLLMMultiSeries = `vllm:num_requests_running{model_name="a"} 2
vllm:num_requests_waiting{model_name="a"} 0
vllm:gpu_cache_usage_perc{model_name="a"} 0.5
vllm:prompt_tokens_total{model_name="a",engine="0"} 7000
vllm:prompt_tokens_total{model_name="a",engine="1"} 5000
vllm:generation_tokens_total{model_name="a",engine="0"} 20000
vllm:generation_tokens_total{model_name="a",engine="1"} 15000
vllm:num_preemptions_total{model_name="a"} 12
vllm:prefix_cache_hits_total{model_name="a"} 4000
vllm:prefix_cache_hits_total{model_name="b"} 1000
vllm:prefix_cache_queries_total{model_name="a"} 5000
vllm:prefix_cache_queries_total{model_name="b"} 5000
`

// One poll earlier: prompt Σ10000, gen Σ33000, preemptions 10, hits Σ3500,
// queries Σ8000 — so the current sample is Δ2000 prompt / Δ2000 gen / Δ2
// preemptions over the window, with a 75 % windowed and 50 % lifetime hit rate.
const sampleVLLMMultiSeriesPrev = `vllm:prompt_tokens_total{model_name="a",engine="0"} 6000
vllm:prompt_tokens_total{model_name="a",engine="1"} 4000
vllm:generation_tokens_total{model_name="a",engine="0"} 18000
vllm:generation_tokens_total{model_name="a",engine="1"} 15000
vllm:num_preemptions_total{model_name="a"} 10
vllm:prefix_cache_hits_total{model_name="a"} 3000
vllm:prefix_cache_hits_total{model_name="b"} 500
vllm:prefix_cache_queries_total{model_name="a"} 4000
vllm:prefix_cache_queries_total{model_name="b"} 4000
`

func pollVLLM(t *testing.T, text string, prev *metrics.WorkerMetrics, prevCounters counterState) *metrics.WorkerMetrics {
	t.Helper()
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true}
	parseVLLMMetrics(m, prev, prevCounters, metrics.ParseText(text))
	return m
}

func TestVLLMRatesSumAllSeries(t *testing.T) {
	online := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-10 * time.Second)}
	prevCounters := prevSnapshot(t, parseVLLMMetrics, sampleVLLMMultiSeriesPrev, 2*time.Second)

	m := pollVLLM(t, sampleVLLMMultiSeries, online, prevCounters)

	if prevCounters.promptTokensTotal != 10000 || prevCounters.genTokensTotal != 33000 {
		t.Errorf("stored counters = %+v, want summed 10000/33000", prevCounters)
	}
	if m.PromptTokPerSec < 900 || m.PromptTokPerSec > 1100 {
		t.Errorf("PromptTokPerSec = %f, want ~1000 (both engine series counted)", m.PromptTokPerSec)
	}
	if m.GenTokPerSec < 900 || m.GenTokPerSec > 1100 {
		t.Errorf("GenTokPerSec = %f, want ~1000 (both engine series counted)", m.GenTokPerSec)
	}
	if m.EvictPerSec < 0.9 || m.EvictPerSec > 1.1 {
		t.Errorf("EvictPerSec = %f, want ~1 (Δ2 over 2s)", m.EvictPerSec)
	}
	// Prefix cache counters fan out per model_name too: Σ5000/Σ10000 = 50 %.
	if math.Abs(m.CacheHitRatePct-50) > 0.01 {
		t.Errorf("CacheHitRatePct = %f, want 50 (hits/queries summed over models)", m.CacheHitRatePct)
	}
	// Detail-row semantics: Δ1500 hits / Δ2000 queries over this window = 75 %.
	if math.Abs(m.CacheHitWindowPct-75) > 0.01 {
		t.Errorf("CacheHitWindowPct = %f, want 75", m.CacheHitWindowPct)
	}
}

// P3 (issue #15 follow-up): rateWindow-style clamping over Σcur−Σprev collapses
// the whole window when one of N series restarts. Restarting engine 1 must cost
// the window that series' tokens, not every series'.
func TestVLLMOneSeriesRestartKeepsOthers(t *testing.T) {
	const current = `vllm:prompt_tokens_total{model_name="a",engine="0"} 8000
vllm:prompt_tokens_total{model_name="a",engine="1"} 100
vllm:generation_tokens_total{model_name="a",engine="0"} 20000
vllm:generation_tokens_total{model_name="a",engine="1"} 50
`
	const previous = `vllm:prompt_tokens_total{model_name="a",engine="0"} 6000
vllm:prompt_tokens_total{model_name="a",engine="1"} 9000
vllm:generation_tokens_total{model_name="a",engine="0"} 16000
vllm:generation_tokens_total{model_name="a",engine="1"} 9000
`
	// Σ falls from 15000 to 8100 (engine 1 restarted): the summed form would
	// clamp the entire window to 0 even though engine 0 served 2000 tokens.
	m := pollVLLM(t, current,
		&metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)},
		prevSnapshot(t, parseVLLMMetrics, previous, 2*time.Second))

	if m.PromptTokPerSec < 900 || m.PromptTokPerSec > 1100 {
		t.Errorf("PromptTokPerSec = %f, want ~1000 (engine 0's Δ2000/2s, engine 1 clamped)", m.PromptTokPerSec)
	}
	if m.GenTokPerSec < 1900 || m.GenTokPerSec > 2100 {
		t.Errorf("GenTokPerSec = %f, want ~2000", m.GenTokPerSec)
	}
}

// dt must come from the counter snapshot's own timestamp. prev.LastSeen lags
// one poll behind prevCounters, so using it halved every vLLM rate (issue #15).
func TestVLLMDtUsesCounterTimestamp(t *testing.T) {
	// prev.LastSeen is deliberately 10 s stale; the counters are 2 s old.
	online := &metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-10 * time.Second)}
	m := pollVLLM(t, sampleVLLMMultiSeries, online,
		prevSnapshot(t, parseVLLMMetrics, sampleVLLMMultiSeriesPrev, 2*time.Second))

	if m.PromptTokPerSec < 900 || m.PromptTokPerSec > 1100 {
		t.Errorf("PromptTokPerSec = %f, want ~1000 (2s counter window, not the 10s LastSeen window)", m.PromptTokPerSec)
	}
}

// First poll (no previous sample timestamp) must report no rate rather than
// dividing the lifetime counter by an arbitrary dt.
func TestVLLMFirstPollHasNoRate(t *testing.T) {
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true}
	counters := parseVLLMMetrics(m, nil, counterState{}, metrics.ParseText(sampleVLLMMultiSeries))

	if m.PromptTokPerSec != 0 || m.GenTokPerSec != 0 || m.EvictPerSec != 0 {
		t.Errorf("rates on first poll = %f/%f/%f, want 0/0/0", m.PromptTokPerSec, m.GenTokPerSec, m.EvictPerSec)
	}
	if counters.ts.IsZero() {
		t.Error("counter snapshot must carry a timestamp for the next poll's rate window")
	}
	// A counter seen for the first time has no window, so its rate stays unset
	// even when a stale-looking window exists.
	m2 := pollVLLM(t, sampleVLLMMultiSeries, &metrics.WorkerMetrics{Online: true},
		counterState{ts: time.Now().Add(-2 * time.Second)})
	if m2.PromptTokPerSec != 0 || m2.GenTokPerSec != 0 {
		t.Errorf("rates with no counter baseline = %f/%f, want 0/0", m2.PromptTokPerSec, m2.GenTokPerSec)
	}
}

// A restarted vLLM worker resets every counter; the deltas must clamp to 0
// instead of rendering a huge negative rate or a nonsense spike.
func TestVLLMCounterResetClamps(t *testing.T) {
	const previous = `vllm:prompt_tokens_total{model_name="a",engine="0"} 9e9
vllm:prompt_tokens_total{model_name="a",engine="1"} 9e9
vllm:generation_tokens_total{model_name="a",engine="0"} 9e9
vllm:generation_tokens_total{model_name="a",engine="1"} 9e9
vllm:num_preemptions_total{model_name="a"} 9e9
vllm:prefix_cache_hits_total{model_name="a"} 9e9
vllm:prefix_cache_queries_total{model_name="a"} 9e9
`
	m := pollVLLM(t, sampleVLLMMultiSeries,
		&metrics.WorkerMetrics{Online: true, LastSeen: time.Now().Add(-2 * time.Second)},
		prevSnapshot(t, parseVLLMMetrics, previous, 2*time.Second))

	if m.PromptTokPerSec != 0 || m.GenTokPerSec != 0 || m.EvictPerSec != 0 {
		t.Errorf("rates after counter reset = %f/%f/%f, want 0/0/0", m.PromptTokPerSec, m.GenTokPerSec, m.EvictPerSec)
	}
	// The window is empty rather than negative, so it must stay unset instead of
	// reporting a fake 0 % hit rate.
	if m.CacheHitWindowPct != 0 {
		t.Errorf("CacheHitWindowPct = %f, want 0 (empty window leaves it unset)", m.CacheHitWindowPct)
	}
}

// P2: vllm:gpu_prefix_cache_hit_rate is an instantaneous gauge only set on
// iterations with a new prefill batch — it reads 0 at idle while the cumulative
// counters prove the prefix cache is doing real work (issue #15 cause 5, which
// the counters-first fix for SGLang already handles). The counters must win.
func TestVLLMCacheHitCountersBeatGauge(t *testing.T) {
	withIdleGauge := sampleVLLMMultiSeries + "vllm:gpu_prefix_cache_hit_rate{model_name=\"a\"} 0\n"
	m := pollVLLM(t, withIdleGauge, nil, counterState{})

	if math.Abs(m.CacheHitRatePct-50) > 0.01 {
		t.Errorf("CacheHitRatePct = %f, want 50 from counters, not the idle 0 gauge", m.CacheHitRatePct)
	}
}

// Builds exporting neither counter keep the gauge as the only source.
func TestVLLMCacheHitGaugeFallback(t *testing.T) {
	m := pollVLLM(t, `vllm:gpu_prefix_cache_hit_rate{model_name="a"} 0.45`, nil, counterState{})
	if math.Abs(m.CacheHitRatePct-45) > 0.01 {
		t.Errorf("CacheHitRatePct = %f, want 45 (gauge fallback)", m.CacheHitRatePct)
	}

	// Counters present but no queries yet → still no made-up hit rate.
	noQueries := pollVLLM(t, `vllm:prefix_cache_hits_total{model_name="a"} 0
vllm:prefix_cache_queries_total{model_name="a"} 0
vllm:gpu_prefix_cache_hit_rate{model_name="a"} 0.45`, nil, counterState{})
	if math.Abs(noQueries.CacheHitRatePct-45) > 0.01 {
		t.Errorf("CacheHitRatePct = %f, want 45 (0 queries is not a 0 %% hit rate)", noQueries.CacheHitRatePct)
	}
}
