package collector

import (
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type tgiParser struct{}

func init() {
	RegisterParser(metrics.BackendTGI, &tgiParser{})
	detectors = append(detectors, &tgiParser{})
}

func (p *tgiParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 4 && s.Name[:4] == "tgi_" {
			return metrics.BackendTGI, ""
		}
	}
	for _, h := range pm.Histograms {
		if len(h.Name) >= 4 && h.Name[:4] == "tgi_" {
			return metrics.BackendTGI, ""
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *tgiParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseTGIMetrics(m, prev, prevCounters, pm)
}

// parseTGIMetrics extracts Hugging Face Text Generation Inference metrics.
//
// TGI uses the tgi_ prefix on all metrics. Key differences from vLLM/SGLang:
//   - No model_name label on metrics — model info requires a separate /info call
//   - No KV cache utilization metric — TGI doesn't expose GPU memory stats
//   - No direct TTFT metric — approximate from tgi_batch_forward_duration{method="prefill"}
//   - ITL via tgi_request_mean_time_per_token_duration histogram
//   - Throughput from tgi_request_generated_tokens counter (rate computation)
//   - Queue depth from tgi_queue_size gauge
func parseTGIMetrics(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	// Queue depth (gauge)
	if v, _, ok := pm.GetGaugeAny("tgi_queue_size"); ok {
		m.RequestsWaiting = int(v)
	}

	// Current batch size as a proxy for running requests
	if v, _, ok := pm.GetGaugeAny("tgi_batch_current_size"); ok {
		m.RequestsRunning = int(v)
	}

	// TTFT approximation: use prefill forward duration as the closest proxy.
	// TGI doesn't expose a per-request TTFT histogram, but the prefill batch
	// forward duration captures the model execution time for the first token.
	if p50, ok := pm.GetHistogramQuantileAny("tgi_request_inference_duration", 0.50); ok {
		m.TTFT_P50 = p50 * 1000
	}
	if p99, ok := pm.GetHistogramQuantileAny("tgi_request_inference_duration", 0.99); ok {
		m.TTFT_P99 = p99 * 1000
	}

	// Inter-token latency: tgi_request_mean_time_per_token_duration is a histogram
	// of per-request average TPOT (time per output token) in seconds.
	if p50, ok := pm.GetHistogramQuantileAny("tgi_request_mean_time_per_token_duration", 0.50); ok {
		m.ITL_P50 = p50 * 1000
	}
	if p99, ok := pm.GetHistogramQuantileAny("tgi_request_mean_time_per_token_duration", 0.99); ok {
		m.ITL_P99 = p99 * 1000
	}

	// Token throughput: tgi_request_generated_tokens is a histogram whose _sum
	// tracks total generated tokens. We compute rate from the counter.
	// The _count tracks total requests, _sum tracks total tokens.
	if prev != nil && prev.Online {
		dt := time.Since(prev.LastSeen).Seconds()
		if dt > 0 {
			// Generated tokens rate from histogram _sum (appears as a sample
			// named tgi_request_generated_tokens_sum in our parser)
			if v, ok := pm.GetHistogramSumAny("tgi_request_generated_tokens"); ok {
				m.GenTokPerSec = (v - prevCounters.genTokensTotal) / dt
				if m.GenTokPerSec < 0 {
					m.GenTokPerSec = 0
				}
			}
			// Input tokens rate
			if v, ok := pm.GetHistogramSumAny("tgi_request_input_length"); ok {
				m.PromptTokPerSec = (v - prevCounters.promptTokensTotal) / dt
				if m.PromptTokPerSec < 0 {
					m.PromptTokPerSec = 0
				}
			}
		}
	}

	// Store raw counters for next rate calculation
	var counters counterState
	if v, ok := pm.GetHistogramSumAny("tgi_request_input_length"); ok {
		counters.promptTokensTotal = v
	}
	if v, ok := pm.GetHistogramSumAny("tgi_request_generated_tokens"); ok {
		counters.genTokensTotal = v
	}

	return counters
}
