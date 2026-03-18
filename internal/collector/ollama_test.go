package collector

import (
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestSynthesizeOllamaMetrics(t *testing.T) {
	jsonBody := `{
  "models": [
    {
      "name": "mistral:latest",
      "model": "mistral:latest",
      "size": 5137025024,
      "details": {
        "parameter_size": "7.2B",
        "quantization_level": "Q4_0",
        "family": "llama"
      },
      "size_vram": 5137025024
    }
  ]
}`
	result := SynthesizeOllamaMetrics(jsonBody)

	if result == "" {
		t.Fatal("SynthesizeOllamaMetrics returned empty string")
	}

	pm := metrics.ParseText(result)

	// Should have a model_loaded gauge
	if v, labels, ok := pm.GetGaugeAny("ollama_model_loaded"); !ok {
		t.Error("ollama_model_loaded not found in synthesized metrics")
	} else {
		if v != 1 {
			t.Errorf("ollama_model_loaded = %f, want 1", v)
		}
		if labels["model"] != "mistral:latest" {
			t.Errorf("model label = %q, want mistral:latest", labels["model"])
		}
	}
}

func TestSynthesizeOllamaMetrics_Empty(t *testing.T) {
	jsonBody := `{"models": []}`
	result := SynthesizeOllamaMetrics(jsonBody)
	if result != "" {
		t.Errorf("expected empty string for no models, got %q", result)
	}
}

func TestSynthesizeOllamaMetrics_InvalidJSON(t *testing.T) {
	result := SynthesizeOllamaMetrics("not json")
	if result != "" {
		t.Errorf("expected empty string for invalid JSON, got %q", result)
	}
}

func TestDetectOllama(t *testing.T) {
	synthesized := SynthesizeOllamaMetrics(`{"models":[{"name":"llama3:8b","model":"llama3:8b","size":4000000000,"size_vram":4000000000}]}`)
	pm := metrics.ParseText(synthesized)
	p := &ollamaParser{}
	backend, model := p.Detect(pm)

	if backend != metrics.BackendOllama {
		t.Errorf("backend = %s, want Ollama", backend)
	}
	if model != "llama3:8b" {
		t.Errorf("model = %s, want llama3:8b", model)
	}
}

func TestDetectOllama_NoFalsePositive(t *testing.T) {
	vllmPayload := `vllm:num_requests_running{model_name="llama"} 3`
	pm := metrics.ParseText(vllmPayload)
	p := &ollamaParser{}
	backend, _ := p.Detect(pm)

	if backend == metrics.BackendOllama {
		t.Error("should not detect Ollama from vLLM metrics")
	}
}
