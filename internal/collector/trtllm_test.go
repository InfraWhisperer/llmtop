package collector

import (
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Representative TensorRT-LLM standalone (trtllm-serve) Prometheus payload.
// Source: NVIDIA/TensorRT-LLM tensorrt_llm/metrics/collector.py
const sampleTRTLLMMetrics = `# HELP trtllm_request_success_total Count of successfully processed requests
# TYPE trtllm_request_success_total counter
trtllm_request_success_total{model_name="llama-3.1-8b",engine_type="trtllm",finished_reason="stop"} 95
trtllm_request_success_total{model_name="llama-3.1-8b",engine_type="trtllm",finished_reason="length"} 5
# HELP trtllm_kv_cache_utilization KV cache utilization (usedBlocks/maxBlocks)
# TYPE trtllm_kv_cache_utilization gauge
trtllm_kv_cache_utilization{model_name="llama-3.1-8b",engine_type="trtllm"} 0.62
# HELP trtllm_kv_cache_hit_rate KV cache hit rate
# TYPE trtllm_kv_cache_hit_rate gauge
trtllm_kv_cache_hit_rate{model_name="llama-3.1-8b",engine_type="trtllm"} 0.38
# HELP trtllm_time_to_first_token_seconds Time to first token
# TYPE trtllm_time_to_first_token_seconds histogram
trtllm_time_to_first_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.001"} 2
trtllm_time_to_first_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.01"} 20
trtllm_time_to_first_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.1"} 85
trtllm_time_to_first_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.5"} 98
trtllm_time_to_first_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="+Inf"} 100
trtllm_time_to_first_token_seconds_sum{model_name="llama-3.1-8b",engine_type="trtllm"} 5.2
trtllm_time_to_first_token_seconds_count{model_name="llama-3.1-8b",engine_type="trtllm"} 100
# HELP trtllm_time_per_output_token_seconds Per-output-token latency
# TYPE trtllm_time_per_output_token_seconds histogram
trtllm_time_per_output_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.01"} 10
trtllm_time_per_output_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.025"} 50
trtllm_time_per_output_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.05"} 80
trtllm_time_per_output_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.1"} 95
trtllm_time_per_output_token_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="+Inf"} 100
trtllm_time_per_output_token_seconds_sum{model_name="llama-3.1-8b",engine_type="trtllm"} 3.8
trtllm_time_per_output_token_seconds_count{model_name="llama-3.1-8b",engine_type="trtllm"} 100
# HELP trtllm_e2e_request_latency_seconds End-to-end request latency
# TYPE trtllm_e2e_request_latency_seconds histogram
trtllm_e2e_request_latency_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="0.5"} 30
trtllm_e2e_request_latency_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="1.0"} 70
trtllm_e2e_request_latency_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="5.0"} 95
trtllm_e2e_request_latency_seconds_bucket{model_name="llama-3.1-8b",engine_type="trtllm",le="+Inf"} 100
trtllm_e2e_request_latency_seconds_sum{model_name="llama-3.1-8b",engine_type="trtllm"} 120.5
trtllm_e2e_request_latency_seconds_count{model_name="llama-3.1-8b",engine_type="trtllm"} 100
`

func TestParseTRTLLMMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleTRTLLMMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8000", Online: true}

	parseTRTLLMMetrics(m, nil, counterState{}, pm)

	if m.KVCacheUsagePct != 62 {
		t.Errorf("KVCacheUsagePct = %f, want 62", m.KVCacheUsagePct)
	}
	if m.CacheHitRatePct != 38 {
		t.Errorf("CacheHitRatePct = %f, want 38", m.CacheHitRatePct)
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
	// TRT-LLM standalone doesn't expose running/waiting request gauges
	if m.RequestsRunning != 0 {
		t.Errorf("RequestsRunning = %d, want 0 (no gauge in trtllm-serve)", m.RequestsRunning)
	}
	// Counter values should not leak
	if m.StoreSizeBytes != 0 {
		t.Errorf("StoreSizeBytes = %f, want 0", m.StoreSizeBytes)
	}
	if m.EvictionTotal != 0 {
		t.Errorf("EvictionTotal = %f, want 0", m.EvictionTotal)
	}
}

func TestDetectTRTLLM(t *testing.T) {
	pm := metrics.ParseText(sampleTRTLLMMetrics)
	p := &trtllmParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendTRTLLM {
		t.Errorf("backend = %s, want TRT-LLM", backend)
	}
	if model != "llama-3.1-8b" {
		t.Errorf("model = %s, want llama-3.1-8b", model)
	}
}

func TestDetectTRTLLM_NoFalsePositive(t *testing.T) {
	// TGI metrics should not trigger TRT-LLM detection
	tgiPayload := `tgi_queue_size 3`
	pm := metrics.ParseText(tgiPayload)
	p := &trtllmParser{}
	backend, _ := p.Detect(pm)

	if backend == metrics.BackendTRTLLM {
		t.Error("should not detect TRT-LLM from TGI metrics")
	}
}
