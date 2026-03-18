package collector

import (
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Representative LiteLLM Prometheus payload.
// Source: BerriAI/litellm litellm/integrations/prometheus.py
const sampleLiteLLMMetrics = `# HELP litellm_requests_metric Total requests
# TYPE litellm_requests_metric counter
litellm_requests_metric{model="gpt-4",team="engineering"} 500
# HELP litellm_input_tokens_metric Total input tokens
# TYPE litellm_input_tokens_metric counter
litellm_input_tokens_metric{model="gpt-4",team="engineering"} 125000
# HELP litellm_output_tokens_metric Total output tokens
# TYPE litellm_output_tokens_metric counter
litellm_output_tokens_metric{model="gpt-4",team="engineering"} 75000
# HELP litellm_in_flight_requests In-flight request count
# TYPE litellm_in_flight_requests gauge
litellm_in_flight_requests 8
# HELP litellm_llm_api_time_to_first_token_metric TTFT in seconds
# TYPE litellm_llm_api_time_to_first_token_metric histogram
litellm_llm_api_time_to_first_token_metric_bucket{model="gpt-4",le="0.1"} 100
litellm_llm_api_time_to_first_token_metric_bucket{model="gpt-4",le="0.5"} 400
litellm_llm_api_time_to_first_token_metric_bucket{model="gpt-4",le="1.0"} 480
litellm_llm_api_time_to_first_token_metric_bucket{model="gpt-4",le="5.0"} 498
litellm_llm_api_time_to_first_token_metric_bucket{model="gpt-4",le="+Inf"} 500
litellm_llm_api_time_to_first_token_metric_sum{model="gpt-4"} 125.0
litellm_llm_api_time_to_first_token_metric_count{model="gpt-4"} 500
# HELP litellm_llm_api_latency_metric LLM API latency in seconds
# TYPE litellm_llm_api_latency_metric histogram
litellm_llm_api_latency_metric_bucket{model="gpt-4",le="0.5"} 50
litellm_llm_api_latency_metric_bucket{model="gpt-4",le="1.0"} 200
litellm_llm_api_latency_metric_bucket{model="gpt-4",le="5.0"} 450
litellm_llm_api_latency_metric_bucket{model="gpt-4",le="10.0"} 490
litellm_llm_api_latency_metric_bucket{model="gpt-4",le="+Inf"} 500
litellm_llm_api_latency_metric_sum{model="gpt-4"} 850.0
litellm_llm_api_latency_metric_count{model="gpt-4"} 500
# HELP litellm_deployment_state Deployment health state
# TYPE litellm_deployment_state gauge
litellm_deployment_state{litellm_model_name="gpt-4",api_provider="openai"} 0
`

func TestParseLiteLLMMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleLiteLLMMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:4000", Online: true}

	parseLiteLLMMetrics(m, nil, counterState{}, pm)

	if m.RequestsRunning != 8 {
		t.Errorf("RequestsRunning = %d, want 8", m.RequestsRunning)
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
	// No KV cache in LiteLLM (it's a proxy)
	if m.KVCacheUsagePct != 0 {
		t.Errorf("KVCacheUsagePct = %f, want 0", m.KVCacheUsagePct)
	}
}

func TestLiteLLMRateCalculation(t *testing.T) {
	pm := metrics.ParseText(sampleLiteLLMMetrics)

	prev := &metrics.WorkerMetrics{
		Online:   true,
		LastSeen: time.Now().Add(-2 * time.Second),
	}
	prevCounters := counterState{
		promptTokensTotal: 123000, // prev litellm_input_tokens_metric
		genTokensTotal:    73000,  // prev litellm_output_tokens_metric
	}

	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:4000", Online: true}
	parseLiteLLMMetrics(m, prev, prevCounters, pm)

	// input delta = 125000 - 123000 = 2000 over ~2s = ~1000 tok/s
	if m.PromptTokPerSec < 900 || m.PromptTokPerSec > 1100 {
		t.Errorf("PromptTokPerSec = %f, want ~1000", m.PromptTokPerSec)
	}
	// output delta = 75000 - 73000 = 2000 over ~2s = ~1000 tok/s
	if m.GenTokPerSec < 900 || m.GenTokPerSec > 1100 {
		t.Errorf("GenTokPerSec = %f, want ~1000", m.GenTokPerSec)
	}
}

func TestDetectLiteLLM(t *testing.T) {
	pm := metrics.ParseText(sampleLiteLLMMetrics)
	p := &litellmParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendLiteLLM {
		t.Errorf("backend = %s, want LiteLLM", backend)
	}
	if model != "gpt-4" {
		t.Errorf("model = %s, want gpt-4", model)
	}
}

func TestDetectLiteLLM_NoFalsePositive(t *testing.T) {
	vllmPayload := `vllm:num_requests_running{model_name="llama"} 3`
	pm := metrics.ParseText(vllmPayload)
	p := &litellmParser{}
	backend, _ := p.Detect(pm)

	if backend == metrics.BackendLiteLLM {
		t.Error("should not detect LiteLLM from vLLM metrics")
	}
}
