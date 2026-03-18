package discovery

import (
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestDetectBackend_VLLM(t *testing.T) {
	body := `vllm:num_requests_running{model_name="test"} 5`
	got := detectBackend(body)
	if got != metrics.BackendVLLM {
		t.Errorf("expected BackendVLLM, got %s", got)
	}
}

func TestDetectBackend_SGLang(t *testing.T) {
	body := `sglang:num_running_reqs{} 3`
	got := detectBackend(body)
	if got != metrics.BackendSGLang {
		t.Errorf("expected BackendSGLang, got %s", got)
	}
}

func TestDetectBackend_LMCache(t *testing.T) {
	body := `lmcache_hit_rate 0.5`
	got := detectBackend(body)
	if got != metrics.BackendLMCache {
		t.Errorf("expected BackendLMCache, got %s", got)
	}
}

func TestDetectBackend_NIM(t *testing.T) {
	body := "num_requests_running{model_name=\"nim-model\"} 2\n" +
		"gpu_cache_usage_perc 0.45\n" +
		"# HELP time_to_first_token_seconds TTFT\n" +
		"time_to_first_token_seconds_bucket{le=\"0.1\"} 10\n"
	got := detectBackend(body)
	if got != metrics.BackendNIM {
		t.Errorf("expected BackendNIM, got %s", got)
	}
}

func TestDetectBackend_NIM_Partial(t *testing.T) {
	// Only 2 of 3 NIM signals — should not match.
	body := "num_requests_running{model_name=\"nim-model\"} 2\n" +
		"gpu_cache_usage_perc 0.45\n"
	got := detectBackend(body)
	if got != metrics.BackendUnknown {
		t.Errorf("expected BackendUnknown for partial NIM signals, got %s", got)
	}
}

func TestDetectBackend_Unknown(t *testing.T) {
	body := "some_random_metric 42\nanother_metric 7\n"
	got := detectBackend(body)
	if got != metrics.BackendUnknown {
		t.Errorf("expected BackendUnknown, got %s", got)
	}
}

func TestDetectBackend_TGI(t *testing.T) {
	body := `tgi_queue_size 3
tgi_batch_current_size 5
tgi_request_count 150
`
	got := detectBackend(body)
	if got != metrics.BackendTGI {
		t.Errorf("expected BackendTGI, got %s", got)
	}
}

func TestDetectBackend_TRTLLM(t *testing.T) {
	body := `trtllm_kv_cache_utilization{model_name="llama-8b",engine_type="trtllm"} 0.62`
	got := detectBackend(body)
	if got != metrics.BackendTRTLLM {
		t.Errorf("expected BackendTRTLLM, got %s", got)
	}
}

func TestDetectBackend_Triton(t *testing.T) {
	body := `nv_inference_request_success{model="llama-7b",version="1"} 500`
	got := detectBackend(body)
	if got != metrics.BackendTriton {
		t.Errorf("expected BackendTriton, got %s", got)
	}
}

func TestDetectBackend_TritonTRTLLM(t *testing.T) {
	body := `nv_trt_llm_request_metrics{model="tensorrt_llm",version="1",request_type="context"} 3`
	got := detectBackend(body)
	if got != metrics.BackendTriton {
		t.Errorf("expected BackendTriton, got %s", got)
	}
}

func TestDetectBackend_LlamaCpp(t *testing.T) {
	body := `llamacpp:kv_cache_usage_ratio 0.42
llamacpp:requests_processing 3
`
	got := detectBackend(body)
	if got != metrics.BackendLlamaCpp {
		t.Errorf("expected BackendLlamaCpp, got %s", got)
	}
}

func TestDetectBackend_Empty(t *testing.T) {
	got := detectBackend("")
	if got != metrics.BackendUnknown {
		t.Errorf("expected BackendUnknown for empty input, got %s", got)
	}
}
