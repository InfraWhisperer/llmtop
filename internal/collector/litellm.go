package collector

import (
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type litellmParser struct{}

func init() {
	RegisterParser(metrics.BackendLiteLLM, &litellmParser{})
	detectors = append(detectors, &litellmParser{})
}

func (p *litellmParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 8 && s.Name[:8] == "litellm_" {
			// Extract model from the "model" label if present
			if model := s.Labels["model"]; model != "" {
				return metrics.BackendLiteLLM, model
			}
			return metrics.BackendLiteLLM, ""
		}
	}
	for _, h := range pm.Histograms {
		if len(h.Name) >= 8 && h.Name[:8] == "litellm_" {
			if model := h.Labels["model"]; model != "" {
				return metrics.BackendLiteLLM, model
			}
			return metrics.BackendLiteLLM, ""
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *litellmParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseLiteLLMMetrics(m, prev, prevCounters, pm)
}

// parseLiteLLMMetrics extracts LiteLLM proxy metrics.
//
// LiteLLM is an API proxy that unifies multiple LLM backends behind a single
// OpenAI-compatible endpoint. It uses the litellm_ prefix on all metrics.
// Requires "prometheus" in the callbacks list in proxy_config.yaml.
//
// LiteLLM metrics are proxy-level — they measure the proxy's view of requests,
// not the underlying inference engine's internal state. There are no KV cache
// or GPU metrics (those come from the backend engine's own metrics).
//
// Default port: 4000, metrics path: /metrics
func parseLiteLLMMetrics(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	// In-flight requests (gauge)
	if v, _, ok := pm.GetGaugeAny("litellm_in_flight_requests"); ok {
		m.RequestsRunning = int(v)
	}

	// TTFT histogram (seconds → ms)
	if p50, ok := pm.GetHistogramQuantileAny("litellm_llm_api_time_to_first_token_metric", 0.50); ok {
		m.TTFT_P50 = p50 * 1000
	}
	if p99, ok := pm.GetHistogramQuantileAny("litellm_llm_api_time_to_first_token_metric", 0.99); ok {
		m.TTFT_P99 = p99 * 1000
	}

	// LLM API latency as ITL proxy (seconds → ms)
	if p50, ok := pm.GetHistogramQuantileAny("litellm_llm_api_latency_metric", 0.50); ok {
		m.ITL_P50 = p50 * 1000
	}
	if p99, ok := pm.GetHistogramQuantileAny("litellm_llm_api_latency_metric", 0.99); ok {
		m.ITL_P99 = p99 * 1000
	}

	// Token throughput from counters, summed across series, with dt from
	// the previous counter snapshot (issue #15).
	now := time.Now()
	input, okInput := pm.GetGaugeSum("litellm_input_tokens_metric", nil)
	output, okOutput := pm.GetGaugeSum("litellm_output_tokens_metric", nil)
	if prev != nil && prev.Online {
		if dt, ok := prevCounters.rateWindow(now); ok {
			if okInput {
				m.PromptTokPerSec = max(0.0, (input-prevCounters.promptTokensTotal)/dt)
			}
			if okOutput {
				m.GenTokPerSec = max(0.0, (output-prevCounters.genTokensTotal)/dt)
			}
		}
	}

	// Store summed raw counters (with sample timestamp) for next rate calculation
	var counters counterState
	if okInput {
		counters.promptTokensTotal = input
	}
	if okOutput {
		counters.genTokensTotal = output
	}
	counters.ts = now

	return counters
}
