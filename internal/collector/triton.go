package collector

import (
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type tritonParser struct{}

func init() {
	RegisterParser(metrics.BackendTriton, &tritonParser{})
	detectors = append(detectors, &tritonParser{})
}

func (p *tritonParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	// Check for nv_trt_llm_ prefix first (TensorRT-LLM behind Triton) —
	// more specific than generic nv_inference_.
	for _, s := range pm.Samples {
		if len(s.Name) >= 11 && s.Name[:11] == "nv_trt_llm_" {
			return metrics.BackendTriton, s.Labels["model"]
		}
	}
	// Generic Triton metrics
	for _, s := range pm.Samples {
		if len(s.Name) >= 13 && s.Name[:13] == "nv_inference_" {
			return metrics.BackendTriton, s.Labels["model"]
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *tritonParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseTritonMetrics(m, prev, prevCounters, pm)
}

// parseTritonMetrics extracts NVIDIA Triton Inference Server metrics.
//
// Triton exposes metrics on port 8002 at /metrics. Two metric families:
//
// 1. Generic Triton metrics (nv_inference_ prefix):
//   - nv_inference_pending_request_count — queue depth gauge
//   - nv_inference_request_success — successful request counter
//   - nv_inference_count — total inferences (batch-expanded)
//   - nv_inference_exec_count — batch execution count
//   - nv_inference_request_duration_us — cumulative request duration counter
//   - nv_inference_queue_duration_us — cumulative queue duration counter
//   - nv_inference_compute_infer_duration_us — cumulative compute duration counter
//
// 2. TensorRT-LLM backend metrics (nv_trt_llm_ prefix, when TRT-LLM is loaded):
//   - nv_trt_llm_request_metrics — gauge with request_type label
//   - nv_trt_llm_kv_cache_block_metrics — gauge with kv_cache_block_type label
//
// All per-model metrics carry model and version labels.
func parseTritonMetrics(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	// Queue depth: nv_inference_pending_request_count ends with _count, so
	// the Prometheus parser misclassifies it as a histogram _count field for
	// "nv_inference_pending_request". Read the value from the histogram's
	// Count field.
	for _, h := range pm.Histograms {
		if h.Name == "nv_inference_pending_request" {
			m.RequestsWaiting = int(h.Count)
			break
		}
	}

	// TRT-LLM behind Triton: request metrics with request_type label
	for _, s := range pm.Samples {
		if s.Name != "nv_trt_llm_request_metrics" {
			continue
		}
		switch s.Labels["request_type"] {
		case "context":
			m.RequestsRunning += int(s.Value)
		case "scheduled":
			m.RequestsRunning += int(s.Value)
		case "waiting":
			m.RequestsWaiting = int(s.Value)
		}
	}

	// KV cache from TRT-LLM Triton backend
	for _, s := range pm.Samples {
		if s.Name != "nv_trt_llm_kv_cache_block_metrics" {
			continue
		}
		if s.Labels["kv_cache_block_type"] == "fraction" {
			m.KVCacheUsagePct = s.Value * 100
		}
	}

	// Triton counter metrics (nv_inference_count, nv_inference_request_success,
	// nv_inference_exec_count) all end with _count, so the Prometheus parser
	// misclassifies them as histogram _count fields. Read from the histogram
	// Count field to get the actual counter values.
	var inferCount, reqSuccess float64
	for _, h := range pm.Histograms {
		switch h.Name {
		case "nv_inference":
			inferCount = h.Count
		case "nv_inference_request_success":
			// This one is nv_inference_request_success (no _count suffix),
			// so it lands in Samples normally.
		}
	}
	// nv_inference_request_success doesn't end with _count, so it's in Samples.
	if v, _, ok := pm.GetGaugeAny("nv_inference_request_success"); ok {
		reqSuccess = v
	}

	// Compute throughput rates from counter deltas
	if prev != nil && prev.Online {
		dt := time.Since(prev.LastSeen).Seconds()
		if dt > 0 {
			if inferCount > 0 {
				rate := (inferCount - prevCounters.genTokensTotal) / dt
				if rate > 0 {
					m.GenTokPerSec = rate
				}
			}
			if reqSuccess > 0 {
				rate := (reqSuccess - prevCounters.promptTokensTotal) / dt
				if rate > 0 {
					m.PromptTokPerSec = rate
				}
			}
		}
	}

	// First response histogram (disabled by default, but parse if available).
	// Triton emits ms-unit histograms — buckets need no conversion. Populate
	// the LatencySnapshot directly so CDFPoints are aligned to ms thresholds.
	if p50, ok := pm.GetHistogramQuantileAny("nv_inference_first_response_histogram_ms", 0.50); ok {
		m.TTFT_P50 = p50
		m.TTFT.P50 = p50
	}
	if p95, ok := pm.GetHistogramQuantileAny("nv_inference_first_response_histogram_ms", 0.95); ok {
		m.TTFT.P95 = p95
	}
	if p99, ok := pm.GetHistogramQuantileAny("nv_inference_first_response_histogram_ms", 0.99); ok {
		m.TTFT_P99 = p99
		m.TTFT.P99 = p99
	}
	if m.TTFT.P95 > m.TTFT.P99 {
		m.TTFT.P95 = m.TTFT.P99
	}
	if m.TTFT.P99 > 0 {
		if buckets, total, ok := pm.GetHistogramBucketsAny("nv_inference_first_response_histogram_ms"); ok {
			// Triton bucket UpperBounds are already in ms; convert to a "seconds"
			// view for ComputeCDFPoints, which expects seconds. Multiplying both
			// the bucket UpperBound and maxLatencyMs by the same scalar leaves
			// the resulting fractions unchanged — pass buckets and ms threshold
			// to a synthetic /1000-scale comparison by adapting the buckets.
			scaled := make([]metrics.HistogramBucket, len(buckets))
			for i, b := range buckets {
				scaled[i] = metrics.HistogramBucket{UpperBound: b.UpperBound / 1000.0, Count: b.Count}
			}
			m.TTFT.CDFPoints = metrics.ComputeCDFPoints(scaled, total, m.TTFT.P99)
		}
	}

	// Store counters for rate computation
	var counters counterState
	counters.promptTokensTotal = reqSuccess
	counters.genTokensTotal = inferCount

	return counters
}
