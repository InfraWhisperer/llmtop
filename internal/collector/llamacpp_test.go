package collector

import (
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Representative llama-server Prometheus payload.
// Source: ggml-org/llama.cpp tools/server/server-context.cpp
const sampleLlamaCppMetrics = `# HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
# TYPE llamacpp:prompt_tokens_total counter
llamacpp:prompt_tokens_total 8500
# HELP llamacpp:prompt_seconds_total Prompt process time
# TYPE llamacpp:prompt_seconds_total counter
llamacpp:prompt_seconds_total 12.5
# HELP llamacpp:tokens_predicted_total Number of generation tokens processed.
# TYPE llamacpp:tokens_predicted_total counter
llamacpp:tokens_predicted_total 25000
# HELP llamacpp:tokens_predicted_seconds_total Predict process time
# TYPE llamacpp:tokens_predicted_seconds_total counter
llamacpp:tokens_predicted_seconds_total 45.2
# HELP llamacpp:prompt_tokens_seconds Average prompt throughput in tokens/s.
# TYPE llamacpp:prompt_tokens_seconds gauge
llamacpp:prompt_tokens_seconds 680.0
# HELP llamacpp:predicted_tokens_seconds Average generation throughput in tokens/s.
# TYPE llamacpp:predicted_tokens_seconds gauge
llamacpp:predicted_tokens_seconds 553.1
# HELP llamacpp:kv_cache_usage_ratio KV-cache usage. 1 means 100 percent usage.
# TYPE llamacpp:kv_cache_usage_ratio gauge
llamacpp:kv_cache_usage_ratio 0.42
# HELP llamacpp:kv_cache_tokens KV-cache tokens.
# TYPE llamacpp:kv_cache_tokens gauge
llamacpp:kv_cache_tokens 2150
# HELP llamacpp:requests_processing Number of requests processing.
# TYPE llamacpp:requests_processing gauge
llamacpp:requests_processing 3
# HELP llamacpp:requests_deferred Number of requests deferred.
# TYPE llamacpp:requests_deferred gauge
llamacpp:requests_deferred 1
# HELP llamacpp:n_decode_total Total number of llama_decode() calls
# TYPE llamacpp:n_decode_total counter
llamacpp:n_decode_total 50000
`

func TestParseLlamaCppMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleLlamaCppMetrics)
	m := &metrics.WorkerMetrics{Endpoint: "http://localhost:8080", Online: true}

	parseLlamaCppMetrics(m, pm)

	if m.KVCacheUsagePct != 42 {
		t.Errorf("KVCacheUsagePct = %f, want 42", m.KVCacheUsagePct)
	}
	if m.RequestsRunning != 3 {
		t.Errorf("RequestsRunning = %d, want 3", m.RequestsRunning)
	}
	if m.RequestsWaiting != 1 {
		t.Errorf("RequestsWaiting = %d, want 1", m.RequestsWaiting)
	}
	// Direct throughput gauges — no rate computation needed
	if m.PromptTokPerSec != 680 {
		t.Errorf("PromptTokPerSec = %f, want 680", m.PromptTokPerSec)
	}
	if m.GenTokPerSec != 553.1 {
		t.Errorf("GenTokPerSec = %f, want 553.1", m.GenTokPerSec)
	}
	// No TTFT/ITL in llama.cpp metrics
	if m.TTFT_P99 != 0 {
		t.Errorf("TTFT_P99 = %f, want 0 (llama.cpp has no TTFT metric)", m.TTFT_P99)
	}
	// Counter values should not leak
	if m.StoreSizeBytes != 0 {
		t.Errorf("StoreSizeBytes = %f, want 0", m.StoreSizeBytes)
	}
}

func TestDetectLlamaCpp(t *testing.T) {
	pm := metrics.ParseText(sampleLlamaCppMetrics)
	p := &llamacppParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendLlamaCpp {
		t.Errorf("backend = %s, want llama.cpp", backend)
	}
	// llama-server has no model_name label
	if model != "" {
		t.Errorf("model = %q, want empty", model)
	}
}

func TestDetectLlamaCpp_NoFalsePositive(t *testing.T) {
	tgiPayload := `tgi_queue_size 3`
	pm := metrics.ParseText(tgiPayload)
	p := &llamacppParser{}
	backend, _ := p.Detect(pm)

	if backend == metrics.BackendLlamaCpp {
		t.Error("should not detect llama.cpp from TGI metrics")
	}
}
