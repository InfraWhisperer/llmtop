package collector

import (
	"encoding/json"
	"strings"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type ollamaParser struct{}

func init() {
	RegisterParser(metrics.BackendOllama, &ollamaParser{})
	// Ollama detection is not prefix-based — it uses a JSON endpoint.
	// Detection happens via the discovery layer probing /api/ps, not via
	// the Prometheus parser. The detector is registered but always returns
	// Unknown from Prometheus text (Ollama doesn't serve Prometheus metrics).
	detectors = append(detectors, &ollamaParser{})
}

func (p *ollamaParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	// Ollama doesn't expose Prometheus metrics, so detection from parsed
	// Prometheus text always returns Unknown. Detection happens at the
	// discovery layer by probing /api/ps for JSON.
	//
	// However, the fetchFunc for Ollama workers synthesizes a pseudo-Prometheus
	// format with the ollama_ prefix so the parser can extract values.
	for _, s := range pm.Samples {
		if len(s.Name) >= 7 && s.Name[:7] == "ollama_" {
			return metrics.BackendOllama, s.Labels["model"]
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *ollamaParser) Parse(m *metrics.WorkerMetrics, _ *metrics.WorkerMetrics, _ counterState, pm *metrics.ParsedMetrics) counterState {
	return parseOllamaMetrics(m, pm)
}

// parseOllamaMetrics extracts Ollama metrics from synthesized Prometheus text.
//
// Ollama doesn't serve a /metrics endpoint. Instead, the discovery layer
// probes /api/ps (JSON) and the fetchFunc synthesizes pseudo-Prometheus text
// with the ollama_ prefix. This function parses that synthesized format.
//
// Available data from /api/ps:
//   - Model name, parameter size, quantization level
//   - VRAM usage (size_vram in bytes)
//   - Total model size
//   - No request metrics, no latency, no throughput
//
// The worker will show as online with a model name but most metric columns
// will be empty ("-"), which is accurate — Ollama doesn't expose those.
func parseOllamaMetrics(_ *metrics.WorkerMetrics, _ *metrics.ParsedMetrics) counterState {
	// Ollama doesn't expose request metrics, latency, or throughput.
	// The worker shows as online with model name from detection; all
	// metric columns display "-".
	return counterState{}
}

// ollamaAPIResponse represents the /api/ps JSON response.
type ollamaAPIResponse struct {
	Models []ollamaModel `json:"models"`
}

type ollamaModel struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Size    int64  `json:"size"`
	Details struct {
		ParameterSize    string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
		Family           string `json:"family"`
	} `json:"details"`
	SizeVRAM int64 `json:"size_vram"`
}

// SynthesizeOllamaMetrics converts an Ollama /api/ps JSON response into
// pseudo-Prometheus text format that the collector can parse. This bridges
// Ollama's JSON API to the Prometheus-based metrics pipeline.
func SynthesizeOllamaMetrics(body string) string {
	var resp ollamaAPIResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return ""
	}
	if len(resp.Models) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, model := range resp.Models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		sb.WriteString("# TYPE ollama_model_loaded gauge\n")
		sb.WriteString("ollama_model_loaded{model=\"" + name + "\"} 1\n")
	}

	return sb.String()
}
