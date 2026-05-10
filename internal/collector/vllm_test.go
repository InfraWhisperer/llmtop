package collector

import (
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

const sampleVLLMMetrics = `# HELP vllm:num_requests_running Number of requests currently running on GPU.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="meta-llama/Llama-3.1-8B-Instruct"} 5.0
# HELP vllm:num_requests_waiting Number of requests waiting to be processed.
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="meta-llama/Llama-3.1-8B-Instruct"} 2.0
# HELP vllm:gpu_cache_usage_perc GPU KV-cache usage.
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{model_name="meta-llama/Llama-3.1-8B-Instruct"} 0.73
# HELP vllm:gpu_prefix_cache_hit_rate GPU prefix cache hit rate.
# TYPE vllm:gpu_prefix_cache_hit_rate gauge
vllm:gpu_prefix_cache_hit_rate{model_name="meta-llama/Llama-3.1-8B-Instruct"} 0.45
# HELP vllm:time_to_first_token_seconds Histogram of time to first token in seconds.
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.01"} 5
vllm:time_to_first_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.025"} 20
vllm:time_to_first_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.05"} 40
vllm:time_to_first_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.1"} 48
vllm:time_to_first_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.5"} 50
vllm:time_to_first_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="+Inf"} 50
vllm:time_to_first_token_seconds_sum{model_name="meta-llama/Llama-3.1-8B-Instruct"} 1.25
vllm:time_to_first_token_seconds_count{model_name="meta-llama/Llama-3.1-8B-Instruct"} 50
# HELP vllm:time_per_output_token_seconds Histogram of inter-token latency in seconds.
# TYPE vllm:time_per_output_token_seconds histogram
vllm:time_per_output_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.005"} 10
vllm:time_per_output_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.01"} 30
vllm:time_per_output_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.025"} 80
vllm:time_per_output_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="0.05"} 95
vllm:time_per_output_token_seconds_bucket{model_name="meta-llama/Llama-3.1-8B-Instruct",le="+Inf"} 100
vllm:time_per_output_token_seconds_sum{model_name="meta-llama/Llama-3.1-8B-Instruct"} 1.8
vllm:time_per_output_token_seconds_count{model_name="meta-llama/Llama-3.1-8B-Instruct"} 100
# HELP vllm:prompt_tokens_total Total number of prompt tokens processed.
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="meta-llama/Llama-3.1-8B-Instruct"} 12000
# HELP vllm:generation_tokens_total Total number of generation tokens produced.
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="meta-llama/Llama-3.1-8B-Instruct"} 35000
`

func TestParseVLLMMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true}

	parseVLLMMetrics(m, nil, counterState{}, pm)

	if m.RequestsRunning != 5 {
		t.Errorf("RequestsRunning = %d, want 5", m.RequestsRunning)
	}
	if m.RequestsWaiting != 2 {
		t.Errorf("RequestsWaiting = %d, want 2", m.RequestsWaiting)
	}
	if m.KVCacheUsagePct != 73 {
		t.Errorf("KVCacheUsagePct = %f, want 73", m.KVCacheUsagePct)
	}
	if m.CacheHitRatePct != 45 {
		t.Errorf("CacheHitRatePct = %f, want 45", m.CacheHitRatePct)
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

func TestVLLMRateCalculation(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMMetrics)

	prev := &metrics.WorkerMetrics{
		Online:   true,
		LastSeen: time.Now().Add(-2 * time.Second),
	}
	prevCounters := counterState{
		promptTokensTotal: 10000,
		genTokensTotal:    33000,
	}

	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true}
	parseVLLMMetrics(m, prev, prevCounters, pm)

	// prompt delta = 12000 - 10000 = 2000 over ~2s = ~1000 tok/s
	if m.PromptTokPerSec < 900 || m.PromptTokPerSec > 1100 {
		t.Errorf("PromptTokPerSec = %f, want ~1000", m.PromptTokPerSec)
	}
	// gen delta = 35000 - 33000 = 2000 over ~2s = ~1000 tok/s
	if m.GenTokPerSec < 900 || m.GenTokPerSec > 1100 {
		t.Errorf("GenTokPerSec = %f, want ~1000", m.GenTokPerSec)
	}
}

func TestDetectVLLM(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMMetrics)
	p := &vllmParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendVLLM {
		t.Errorf("backend = %s, want vLLM", backend)
	}
	if model != "meta-llama/Llama-3.1-8B-Instruct" {
		t.Errorf("model = %s, want meta-llama/Llama-3.1-8B-Instruct", model)
	}
}

func TestVLLMCounterState(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true}

	counters := parseVLLMMetrics(m, nil, counterState{}, pm)

	// Counter values must NOT leak into WorkerMetrics fields
	if m.StoreSizeBytes != 0 {
		t.Errorf("StoreSizeBytes = %f, want 0 (counters should not leak into exported fields)", m.StoreSizeBytes)
	}
	if m.EvictionTotal != 0 {
		t.Errorf("EvictionTotal = %f, want 0 (counters should not leak into exported fields)", m.EvictionTotal)
	}
	// Counter values should be in the returned counterState
	if counters.promptTokensTotal != 12000 {
		t.Errorf("counters.promptTokensTotal = %f, want 12000", counters.promptTokensTotal)
	}
	if counters.genTokensTotal != 35000 {
		t.Errorf("counters.genTokensTotal = %f, want 35000", counters.genTokensTotal)
	}
}

const sampleVLLMNVMeAndXfer = `vllm:gpu_cache_usage_perc{model_name="m"} 0.5
vllm:nvme_cache_usage_perc{model_name="m"} 0.42
vllm:kv_cache_offload_time_seconds_bucket{model_name="m",le="0.005"} 5
vllm:kv_cache_offload_time_seconds_bucket{model_name="m",le="0.01"} 50
vllm:kv_cache_offload_time_seconds_bucket{model_name="m",le="0.02"} 95
vllm:kv_cache_offload_time_seconds_bucket{model_name="m",le="+Inf"} 100
vllm:kv_cache_offload_time_seconds_count{model_name="m"} 100
vllm:kv_cache_offload_time_seconds_sum{model_name="m"} 0.8
vllm:request_success_total{model_name="m",request_tag="team-search"} 200
vllm:request_success_total{model_name="m",request_tag="team-rag"} 100
`

func TestParseVLLM_NVMePct(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMNVMeAndXfer)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true, Role: "prefill"}
	parseVLLMMetrics(m, nil, counterState{}, pm)
	if m.KVCacheUsageNVMePct != 42 {
		t.Errorf("KVCacheUsageNVMePct = %f, want 42", m.KVCacheUsageNVMePct)
	}
}

func TestParseVLLM_KVTransferP99_PrefillOnly(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMNVMeAndXfer)

	prefill := &metrics.WorkerMetrics{Endpoint: "p", Online: true, Role: "prefill"}
	parseVLLMMetrics(prefill, nil, counterState{}, pm)
	if prefill.KVTransferP99Ms <= 0 {
		t.Errorf("expected KVTransferP99Ms > 0 for prefill, got %f", prefill.KVTransferP99Ms)
	}

	decode := &metrics.WorkerMetrics{Endpoint: "d", Online: true, Role: "decode"}
	parseVLLMMetrics(decode, nil, counterState{}, pm)
	if decode.KVTransferP99Ms != 0 {
		t.Errorf("expected KVTransferP99Ms == 0 for decode, got %f", decode.KVTransferP99Ms)
	}
}

func TestParseVLLM_TenantReqs(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMNVMeAndXfer)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true, Role: "prefill"}
	counters := parseVLLMMetrics(m, nil, counterState{}, pm)
	if m.TenantReqs["team-search"] != 200 || m.TenantReqs["team-rag"] != 100 {
		t.Errorf("unexpected TenantReqs: %v", m.TenantReqs)
	}
	if counters.tenantReqTotals["team-search"] != 200 {
		t.Errorf("expected tenant counter snapshot, got %v", counters.tenantReqTotals)
	}
}

func TestParseVLLM_TTFTLatencySnapshot(t *testing.T) {
	pm := metrics.ParseText(sampleVLLMMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "p", Online: true, Role: "prefill"}
	parseVLLMMetrics(m, nil, counterState{}, pm)
	if m.TTFT.P50 == 0 || m.TTFT.P95 == 0 || m.TTFT.P99 == 0 {
		t.Errorf("TTFT snapshot incomplete: %+v", m.TTFT)
	}
	if !(m.TTFT.P50 <= m.TTFT.P95 && m.TTFT.P95 <= m.TTFT.P99) {
		t.Errorf("TTFT quantiles violate ordering: %+v", m.TTFT)
	}
	prev := 0.0
	for i, v := range m.TTFT.CDFPoints {
		if v < prev {
			t.Errorf("CDFPoints not monotonic at %d: %f < %f", i, v, prev)
		}
		prev = v
	}
}
