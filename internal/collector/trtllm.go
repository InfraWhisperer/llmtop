package collector

import (
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type trtllmParser struct{}

func init() {
	RegisterParser(metrics.BackendTRTLLM, &trtllmParser{})
	detectors = append(detectors, &trtllmParser{})
}

func (p *trtllmParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 7 && s.Name[:7] == "trtllm_" {
			return metrics.BackendTRTLLM, s.Labels["model_name"]
		}
	}
	for _, h := range pm.Histograms {
		if len(h.Name) >= 7 && h.Name[:7] == "trtllm_" {
			return metrics.BackendTRTLLM, h.Labels["model_name"]
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *trtllmParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseTRTLLMMetrics(m, prev, prevCounters, pm)
}

// parseTRTLLMMetrics extracts TensorRT-LLM standalone metrics (trtllm-serve).
//
// TensorRT-LLM uses the trtllm_ prefix when running standalone via trtllm-serve.
// Metrics endpoint: /prometheus/metrics on port 8000.
// Key differences from vLLM:
//   - KV cache utilization is a direct gauge (trtllm_kv_cache_utilization)
//   - KV cache hit rate is a direct gauge (trtllm_kv_cache_hit_rate)
//   - TTFT and TPOT are histograms in seconds
//   - No queue depth gauge — use trtllm_request_queue_time_seconds histogram presence
//   - Token throughput must be derived from request success counter + histogram data
//   - Labels: model_name, engine_type
//
// Note: When TensorRT-LLM runs behind Triton, metrics use the nv_trt_llm_ prefix
// and a different schema — that's handled by the Triton parser, not this one.
func parseTRTLLMMetrics(m *metrics.WorkerMetrics, _ *metrics.WorkerMetrics, _ counterState, pm *metrics.ParsedMetrics) counterState {
	// KV cache utilization (0.0-1.0 gauge → 0-100%)
	if v, _, ok := pm.GetGaugeAny("trtllm_kv_cache_utilization"); ok {
		m.KVCacheUsagePct = v * 100
	}

	// KV cache hit rate (0.0-1.0 gauge → 0-100%)
	if v, _, ok := pm.GetGaugeAny("trtllm_kv_cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	}

	// TTFT histogram (seconds → ms)
	if p50, ok := pm.GetHistogramQuantileAny("trtllm_time_to_first_token_seconds", 0.50); ok {
		m.TTFT_P50 = p50 * 1000
	}
	if p99, ok := pm.GetHistogramQuantileAny("trtllm_time_to_first_token_seconds", 0.99); ok {
		m.TTFT_P99 = p99 * 1000
	}

	// Inter-token latency histogram (seconds → ms)
	if p50, ok := pm.GetHistogramQuantileAny("trtllm_time_per_output_token_seconds", 0.50); ok {
		m.ITL_P50 = p50 * 1000
	}
	if p99, ok := pm.GetHistogramQuantileAny("trtllm_time_per_output_token_seconds", 0.99); ok {
		m.ITL_P99 = p99 * 1000
	}

	// Request success counter — no direct running/waiting gauges in trtllm-serve,
	// but queue time histogram presence indicates requests are being queued.
	// The request_success counter with finished_reason labels gives completion signals.
	if v, _, ok := pm.GetGaugeAny("trtllm_request_success_total"); ok {
		_ = v // counter, not directly useful as a gauge — tracked for rate in future
	}

	// TensorRT-LLM standalone doesn't expose running/waiting request gauges.
	// These remain at zero, which the UI renders as "-".

	return counterState{}
}
