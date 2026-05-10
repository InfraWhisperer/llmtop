package collector

import "github.com/InfraWhisperer/llmtop/internal/metrics"

// populateLatency fills both the legacy scalar TTFT/ITL fields and the new
// LatencySnapshot views on WorkerMetrics, given the histogram name for TTFT
// and an ordered list of fallback histogram names for ITL (different vLLM
// versions emit ITL under different names).
//
// Invariants enforced here:
//   P50 <= P95 <= P99 (P95 is clamped to P99 if interpolation jitter pushes
//   it above on sparse buckets).
//   CDFPoints is zero-filled when P99 == 0.
func populateLatency(m *metrics.WorkerMetrics, pm *metrics.ParsedMetrics, ttftName string, itlNames []string) {
	m.TTFT = buildSnapshot(pm, ttftName)
	m.TTFT_P50 = m.TTFT.P50
	m.TTFT_P99 = m.TTFT.P99

	for _, name := range itlNames {
		if _, _, ok := pm.GetHistogramBucketsAny(name); ok {
			m.ITL = buildSnapshot(pm, name)
			break
		}
		// Some backends emit only quantiles without the bucket method paths
		// being usable; fall through to next name.
		if _, ok := pm.GetHistogramQuantileAny(name, 0.99); ok {
			m.ITL = buildSnapshot(pm, name)
			break
		}
	}
	m.ITL_P50 = m.ITL.P50
	m.ITL_P99 = m.ITL.P99
}

// buildSnapshot computes a LatencySnapshot from a Prometheus histogram name.
// All values are returned in milliseconds.
func buildSnapshot(pm *metrics.ParsedMetrics, name string) metrics.LatencySnapshot {
	var ls metrics.LatencySnapshot
	p50, ok50 := pm.GetHistogramQuantileAny(name, 0.50)
	p95, ok95 := pm.GetHistogramQuantileAny(name, 0.95)
	p99, ok99 := pm.GetHistogramQuantileAny(name, 0.99)
	if ok50 {
		ls.P50 = p50 * 1000
	}
	if ok95 {
		ls.P95 = p95 * 1000
	}
	if ok99 {
		ls.P99 = p99 * 1000
	}
	if ls.P95 > ls.P99 {
		ls.P95 = ls.P99
	}
	if ls.P50 > ls.P95 {
		ls.P50 = ls.P95
	}
	if ls.P99 == 0 {
		return ls
	}
	if buckets, total, ok := pm.GetHistogramBucketsAny(name); ok {
		ls.CDFPoints = metrics.ComputeCDFPoints(buckets, total, ls.P99)
	}
	return ls
}
