package collector

import (
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Representative TGI Prometheus payload based on actual TGI metric names
// from huggingface/text-generation-inference source (router/src/server.rs).
const sampleTGIMetrics = `# HELP tgi_request_count Total number of requests received
# TYPE tgi_request_count counter
tgi_request_count 150
# HELP tgi_request_success Number of successful requests
# TYPE tgi_request_success counter
tgi_request_success 148
# HELP tgi_queue_size Current number of requests in the queue
# TYPE tgi_queue_size gauge
tgi_queue_size 3
# HELP tgi_batch_current_size Current batch size
# TYPE tgi_batch_current_size gauge
tgi_batch_current_size 5
# HELP tgi_request_duration End-to-end request latency in seconds
# TYPE tgi_request_duration histogram
tgi_request_duration_bucket{le="0.01"} 5
tgi_request_duration_bucket{le="0.05"} 30
tgi_request_duration_bucket{le="0.1"} 80
tgi_request_duration_bucket{le="0.5"} 140
tgi_request_duration_bucket{le="1.0"} 148
tgi_request_duration_bucket{le="+Inf"} 150
tgi_request_duration_sum 22.5
tgi_request_duration_count 150
# HELP tgi_request_inference_duration Time spent on inference in seconds
# TYPE tgi_request_inference_duration histogram
tgi_request_inference_duration_bucket{le="0.01"} 10
tgi_request_inference_duration_bucket{le="0.025"} 40
tgi_request_inference_duration_bucket{le="0.05"} 90
tgi_request_inference_duration_bucket{le="0.1"} 135
tgi_request_inference_duration_bucket{le="0.5"} 148
tgi_request_inference_duration_bucket{le="+Inf"} 150
tgi_request_inference_duration_sum 8.75
tgi_request_inference_duration_count 150
# HELP tgi_request_mean_time_per_token_duration Mean inter-token latency per request in seconds
# TYPE tgi_request_mean_time_per_token_duration histogram
tgi_request_mean_time_per_token_duration_bucket{le="0.005"} 10
tgi_request_mean_time_per_token_duration_bucket{le="0.01"} 50
tgi_request_mean_time_per_token_duration_bucket{le="0.025"} 120
tgi_request_mean_time_per_token_duration_bucket{le="0.05"} 145
tgi_request_mean_time_per_token_duration_bucket{le="+Inf"} 150
tgi_request_mean_time_per_token_duration_sum 2.1
tgi_request_mean_time_per_token_duration_count 150
# HELP tgi_request_generated_tokens Number of tokens generated per request
# TYPE tgi_request_generated_tokens histogram
tgi_request_generated_tokens_bucket{le="10"} 20
tgi_request_generated_tokens_bucket{le="50"} 80
tgi_request_generated_tokens_bucket{le="100"} 130
tgi_request_generated_tokens_bucket{le="500"} 148
tgi_request_generated_tokens_bucket{le="+Inf"} 150
tgi_request_generated_tokens_sum 12500
tgi_request_generated_tokens_count 150
# HELP tgi_request_input_length Input token length per request
# TYPE tgi_request_input_length histogram
tgi_request_input_length_bucket{le="10"} 5
tgi_request_input_length_bucket{le="50"} 40
tgi_request_input_length_bucket{le="100"} 90
tgi_request_input_length_bucket{le="500"} 145
tgi_request_input_length_bucket{le="+Inf"} 150
tgi_request_input_length_sum 18000
tgi_request_input_length_count 150
# HELP tgi_request_queue_duration Time spent in queue in seconds
# TYPE tgi_request_queue_duration histogram
tgi_request_queue_duration_bucket{le="0.01"} 100
tgi_request_queue_duration_bucket{le="0.05"} 130
tgi_request_queue_duration_bucket{le="0.1"} 145
tgi_request_queue_duration_bucket{le="+Inf"} 150
tgi_request_queue_duration_sum 3.2
tgi_request_queue_duration_count 150
# HELP tgi_batch_inference_count Inference calls per method
# TYPE tgi_batch_inference_count counter
tgi_batch_inference_count{method="prefill"} 150
tgi_batch_inference_count{method="decode"} 4500
`

func TestParseTGIMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleTGIMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:3000", Online: true}

	parseTGIMetrics(m, nil, counterState{}, pm)

	if m.RequestsWaiting != 3 {
		t.Errorf("RequestsWaiting = %d, want 3", m.RequestsWaiting)
	}
	if m.RequestsRunning != 5 {
		t.Errorf("RequestsRunning = %d, want 5", m.RequestsRunning)
	}
	// TTFT from tgi_request_inference_duration histogram
	if m.TTFT_P50 <= 0 {
		t.Errorf("TTFT_P50 = %f, want > 0", m.TTFT_P50)
	}
	if m.TTFT_P99 <= 0 {
		t.Errorf("TTFT_P99 = %f, want > 0", m.TTFT_P99)
	}
	// ITL from tgi_request_mean_time_per_token_duration
	if m.ITL_P50 <= 0 {
		t.Errorf("ITL_P50 = %f, want > 0", m.ITL_P50)
	}
	if m.ITL_P99 <= 0 {
		t.Errorf("ITL_P99 = %f, want > 0", m.ITL_P99)
	}
	// No KV cache metric from TGI
	if m.KVCacheUsagePct != 0 {
		t.Errorf("KVCacheUsagePct = %f, want 0 (TGI does not expose KV cache)", m.KVCacheUsagePct)
	}
	// Counter values should not leak
	if m.StoreSizeBytes != 0 {
		t.Errorf("StoreSizeBytes = %f, want 0", m.StoreSizeBytes)
	}
	if m.EvictionTotal != 0 {
		t.Errorf("EvictionTotal = %f, want 0", m.EvictionTotal)
	}
}

func TestTGIRateCalculation(t *testing.T) {
	pm := metrics.ParseText(sampleTGIMetrics)

	prev := &metrics.WorkerMetrics{
		Online:   true,
		LastSeen: time.Now().Add(-2 * time.Second),
	}
	prevCounters := counterState{
		promptTokensTotal: 16000, // prev tgi_request_input_length_sum
		genTokensTotal:    10500, // prev tgi_request_generated_tokens_sum
	}

	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:3000", Online: true}
	parseTGIMetrics(m, prev, prevCounters, pm)

	// prompt delta = 18000 - 16000 = 2000 over ~2s = ~1000 tok/s
	if m.PromptTokPerSec < 900 || m.PromptTokPerSec > 1100 {
		t.Errorf("PromptTokPerSec = %f, want ~1000", m.PromptTokPerSec)
	}
	// gen delta = 12500 - 10500 = 2000 over ~2s = ~1000 tok/s
	if m.GenTokPerSec < 900 || m.GenTokPerSec > 1100 {
		t.Errorf("GenTokPerSec = %f, want ~1000", m.GenTokPerSec)
	}
}

func TestTGICounterState(t *testing.T) {
	pm := metrics.ParseText(sampleTGIMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:3000", Online: true}

	counters := parseTGIMetrics(m, nil, counterState{}, pm)

	if counters.promptTokensTotal != 18000 {
		t.Errorf("counters.promptTokensTotal = %f, want 18000", counters.promptTokensTotal)
	}
	if counters.genTokensTotal != 12500 {
		t.Errorf("counters.genTokensTotal = %f, want 12500", counters.genTokensTotal)
	}
}

func TestDetectTGI(t *testing.T) {
	pm := metrics.ParseText(sampleTGIMetrics)
	p := &tgiParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendTGI {
		t.Errorf("backend = %s, want TGI", backend)
	}
	// TGI doesn't expose model name in Prometheus labels
	if model != "" {
		t.Errorf("model = %q, want empty (TGI has no model label)", model)
	}
}

func TestDetectTGI_NoFalsePositive(t *testing.T) {
	// vLLM metrics should not trigger TGI detection
	vllmPayload := `vllm:num_requests_running{model_name="llama"} 3`
	pm := metrics.ParseText(vllmPayload)
	p := &tgiParser{}
	backend, _ := p.Detect(pm)

	if backend == metrics.BackendTGI {
		t.Error("should not detect TGI from vLLM metrics")
	}
}
