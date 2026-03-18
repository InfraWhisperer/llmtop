package collector

import (
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Generic Triton metrics payload (any model backend).
const sampleTritonGenericMetrics = `# HELP nv_inference_request_success Number of successful inference requests
# TYPE nv_inference_request_success counter
nv_inference_request_success{model="llama-7b",version="1"} 500
# HELP nv_inference_count Total inferences performed
# TYPE nv_inference_count counter
nv_inference_count{model="llama-7b",version="1"} 2500
# HELP nv_inference_exec_count Total inference batch executions
# TYPE nv_inference_exec_count counter
nv_inference_exec_count{model="llama-7b",version="1"} 500
# HELP nv_inference_pending_request_count Requests awaiting execution
# TYPE nv_inference_pending_request_count gauge
nv_inference_pending_request_count{model="llama-7b",version="1"} 4
# HELP nv_inference_request_duration_us Cumulative request duration
# TYPE nv_inference_request_duration_us counter
nv_inference_request_duration_us{model="llama-7b",version="1"} 25000000
# HELP nv_gpu_utilization GPU utilization
# TYPE nv_gpu_utilization gauge
nv_gpu_utilization{gpu_uuid="GPU-abc123"} 0.78
`

// TRT-LLM behind Triton metrics payload.
const sampleTritonTRTLLMMetrics = `# HELP nv_trt_llm_request_metrics TRT-LLM request metrics
# TYPE nv_trt_llm_request_metrics gauge
nv_trt_llm_request_metrics{model="tensorrt_llm",version="1",request_type="context"} 3
nv_trt_llm_request_metrics{model="tensorrt_llm",version="1",request_type="scheduled"} 2
nv_trt_llm_request_metrics{model="tensorrt_llm",version="1",request_type="waiting"} 1
nv_trt_llm_request_metrics{model="tensorrt_llm",version="1",request_type="max"} 256
nv_trt_llm_request_metrics{model="tensorrt_llm",version="1",request_type="active"} 5
# HELP nv_trt_llm_kv_cache_block_metrics TRT-LLM KV cache block metrics
# TYPE nv_trt_llm_kv_cache_block_metrics gauge
nv_trt_llm_kv_cache_block_metrics{model="tensorrt_llm",version="1",kv_cache_block_type="fraction"} 0.55
nv_trt_llm_kv_cache_block_metrics{model="tensorrt_llm",version="1",kv_cache_block_type="used"} 110
nv_trt_llm_kv_cache_block_metrics{model="tensorrt_llm",version="1",kv_cache_block_type="free"} 90
nv_trt_llm_kv_cache_block_metrics{model="tensorrt_llm",version="1",kv_cache_block_type="max"} 200
nv_trt_llm_kv_cache_block_metrics{model="tensorrt_llm",version="1",kv_cache_block_type="tokens_per"} 64
# HELP nv_inference_pending_request_count Requests awaiting execution
# TYPE nv_inference_pending_request_count gauge
nv_inference_pending_request_count{model="tensorrt_llm",version="1"} 7
# HELP nv_inference_request_success Successful requests
# TYPE nv_inference_request_success counter
nv_inference_request_success{model="tensorrt_llm",version="1"} 1000
# HELP nv_inference_count Total inferences
# TYPE nv_inference_count counter
nv_inference_count{model="tensorrt_llm",version="1"} 5000
`

func TestParseTritonGenericMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleTritonGenericMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8002", Online: true}

	parseTritonMetrics(m, nil, counterState{}, pm)

	if m.RequestsWaiting != 4 {
		t.Errorf("RequestsWaiting = %d, want 4", m.RequestsWaiting)
	}
	// No KV cache in generic Triton metrics
	if m.KVCacheUsagePct != 0 {
		t.Errorf("KVCacheUsagePct = %f, want 0 (no KV cache in generic Triton)", m.KVCacheUsagePct)
	}
}

func TestParseTritonTRTLLMMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleTritonTRTLLMMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8002", Online: true}

	parseTritonMetrics(m, nil, counterState{}, pm)

	// nv_trt_llm_request_metrics waiting=1 overrides nv_inference_pending_request_count=7
	if m.RequestsWaiting != 1 {
		t.Errorf("RequestsWaiting = %d, want 1 (from nv_trt_llm_request_metrics waiting)", m.RequestsWaiting)
	}
	// context(3) + scheduled(2) = 5
	if m.RequestsRunning != 5 {
		t.Errorf("RequestsRunning = %d, want 5 (context + scheduled)", m.RequestsRunning)
	}
	// KV cache fraction (0.55 * 100 may have float precision error)
	if m.KVCacheUsagePct < 54.99 || m.KVCacheUsagePct > 55.01 {
		t.Errorf("KVCacheUsagePct = %f, want ~55", m.KVCacheUsagePct)
	}
}

func TestTritonRateCalculation(t *testing.T) {
	pm := metrics.ParseText(sampleTritonGenericMetrics)

	prev := &metrics.WorkerMetrics{
		Online:   true,
		LastSeen: time.Now().Add(-2 * time.Second),
	}
	prevCounters := counterState{
		promptTokensTotal: 450,  // prev nv_inference_request_success
		genTokensTotal:    2000, // prev nv_inference_count
	}

	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8002", Online: true}
	parseTritonMetrics(m, prev, prevCounters, pm)

	// inference_count delta = 2500 - 2000 = 500 over ~2s = ~250/s
	if m.GenTokPerSec < 220 || m.GenTokPerSec > 280 {
		t.Errorf("GenTokPerSec = %f, want ~250", m.GenTokPerSec)
	}
	// request_success delta = 500 - 450 = 50 over ~2s = ~25/s
	if m.PromptTokPerSec < 22 || m.PromptTokPerSec > 28 {
		t.Errorf("PromptTokPerSec = %f, want ~25", m.PromptTokPerSec)
	}
}

func TestDetectTritonGeneric(t *testing.T) {
	pm := metrics.ParseText(sampleTritonGenericMetrics)
	p := &tritonParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendTriton {
		t.Errorf("backend = %s, want Triton", backend)
	}
	if model != "llama-7b" {
		t.Errorf("model = %s, want llama-7b", model)
	}
}

func TestDetectTritonTRTLLM(t *testing.T) {
	pm := metrics.ParseText(sampleTritonTRTLLMMetrics)
	p := &tritonParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendTriton {
		t.Errorf("backend = %s, want Triton", backend)
	}
	if model != "tensorrt_llm" {
		t.Errorf("model = %s, want tensorrt_llm", model)
	}
}

func TestDetectTriton_NoFalsePositive(t *testing.T) {
	vllmPayload := `vllm:num_requests_running{model_name="llama"} 3`
	pm := metrics.ParseText(vllmPayload)
	p := &tritonParser{}
	backend, _ := p.Detect(pm)

	if backend == metrics.BackendTriton {
		t.Error("should not detect Triton from vLLM metrics")
	}
}
