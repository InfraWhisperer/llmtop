package ui

import "github.com/InfraWhisperer/llmtop/internal/metrics"

// AnomalyGetter is the test-injectable interface the Workers table and
// sidebar consume to render V3 halo gutter and the ANOMALIES sidebar block.
//
// This is a local alias of metrics.AnomalyGetter — the interface type is
// duplicated here so test stubs in this package can implement it without
// pulling the metrics import into every test file. *metrics.AnomalyStore
// satisfies this interface via Go's structural typing; no swap is needed
// at the call sites.
type AnomalyGetter interface {
	MaxSigma(endpoint string, w *metrics.WorkerMetrics) (float64, string)
	Anomalies(endpoint string, w *metrics.WorkerMetrics) []metrics.AnomalyDetail
}

// AnomalyDetail re-exports metrics.AnomalyDetail at the ui package level
// so that test code and render helpers can refer to a single type without
// fully qualifying. Field-by-field equivalent.
type AnomalyDetail = metrics.AnomalyDetail
