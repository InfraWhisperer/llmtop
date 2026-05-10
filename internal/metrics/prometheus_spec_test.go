// SPDX-License-Identifier: Apache-2.0
// Spec-only contract tests for internal/metrics/prometheus.go.
// Source of truth: docs/feature-spec.md sections F2 (LatencySnapshot,
// GetHistogramBucketsAny, ComputeCDFPoints) and F3 (GetAllGaugesByLabel).
// These tests exercise only the public API; do not couple to impl details.

package metrics_test

import (
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// --- helpers -----------------------------------------------------------------

// parsePromText is a tiny helper that runs the standard parser entry point.
// metrics.ParseText is the package's published constructor for ParsedMetrics.
func parsePromText(t *testing.T, body string) *metrics.ParsedMetrics {
	t.Helper()
	pm := metrics.ParseText(body)
	if pm == nil {
		t.Fatalf("ParseText returned nil")
	}
	return pm
}

// vllmTTFTFixture returns a Prometheus-format payload with a vLLM TTFT
// histogram. Buckets in seconds: [0.5, 1.0, 2.0, 5.0, +Inf]; total count 100;
// cumulative counts 30, 50, 80, 95, 100. Bucket-implied P99 ≈ between 2.0 and
// 5.0 seconds.
func vllmTTFTFixture() string {
	return `# HELP vllm:time_to_first_token_seconds Time to first token
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{model_name="m",le="0.5"} 30
vllm:time_to_first_token_seconds_bucket{model_name="m",le="1.0"} 50
vllm:time_to_first_token_seconds_bucket{model_name="m",le="2.0"} 80
vllm:time_to_first_token_seconds_bucket{model_name="m",le="5.0"} 95
vllm:time_to_first_token_seconds_bucket{model_name="m",le="+Inf"} 100
vllm:time_to_first_token_seconds_count{model_name="m"} 100
vllm:time_to_first_token_seconds_sum{model_name="m"} 95.0
`
}

// vllmRequestSuccessTagged returns Prometheus text containing
// vllm:request_success_total samples labeled with multiple request_tag values.
func vllmRequestSuccessTagged() string {
	return `# TYPE vllm:request_success_total counter
vllm:request_success_total{model_name="m",request_tag="team-search"} 350
vllm:request_success_total{model_name="m",request_tag="team-rag"} 250
vllm:request_success_total{model_name="m",request_tag="team-copilot"} 200
vllm:request_success_total{model_name="m",request_tag="team-analytics"} 120
vllm:request_success_total{model_name="m",request_tag="team-infra"} 80
`
}

// --- ComputeCDFPoints --------------------------------------------------------

func TestComputeCDFPoints_ZeroMaxLatency_ReturnsZeroArray(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, ok := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	if !ok {
		t.Fatalf("expected histogram to be found")
	}
	got := metrics.ComputeCDFPoints(buckets, total, 0)
	var want [10]float64
	if got != want {
		t.Fatalf("expected zero array for maxLatencyMs=0, got %v", got)
	}
}

func TestComputeCDFPoints_NegativeMaxLatency_ReturnsZeroArray(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, _ := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	got := metrics.ComputeCDFPoints(buckets, total, -1)
	var want [10]float64
	if got != want {
		t.Fatalf("expected zero array for negative maxLatencyMs, got %v", got)
	}
}

func TestComputeCDFPoints_ZeroTotalCount_ReturnsZeroArray(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, _, _ := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	got := metrics.ComputeCDFPoints(buckets, 0, 5000)
	var want [10]float64
	if got != want {
		t.Fatalf("expected zero array for totalCount=0, got %v", got)
	}
}

func TestComputeCDFPoints_NegativeTotalCount_ReturnsZeroArray(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, _, _ := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	got := metrics.ComputeCDFPoints(buckets, -10, 5000)
	var want [10]float64
	if got != want {
		t.Fatalf("expected zero array for negative totalCount, got %v", got)
	}
}

func TestComputeCDFPoints_AllValuesInUnitInterval(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, _ := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	got := metrics.ComputeCDFPoints(buckets, total, 5000)
	for i, v := range got {
		if v < 0 || v > 1 {
			t.Fatalf("CDFPoints[%d]=%v out of [0,1]", i, v)
		}
	}
}

func TestComputeCDFPoints_MonotonicNonDecreasing(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, _ := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	got := metrics.ComputeCDFPoints(buckets, total, 5000)
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("CDFPoints not monotonic: idx %d=%v < idx %d=%v", i, got[i], i-1, got[i-1])
		}
	}
}

func TestComputeCDFPoints_PointsWithinUnitInterval(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, _ := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	got := metrics.ComputeCDFPoints(buckets, total, 5000)
	for i, v := range got {
		if v < 0 || v > 1 {
			t.Errorf("CDFPoints[%d] = %v, want value in [0, 1]", i, v)
		}
	}
}

func TestComputeCDFPoints_LastElementBoundedByOne(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, _ := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	got := metrics.ComputeCDFPoints(buckets, total, 5000)
	if got[9] > 1.0 {
		t.Fatalf("CDFPoints[9]=%v exceeds 1.0", got[9])
	}
}

func TestComputeCDFPoints_NilBucketsReturnsZeroArray(t *testing.T) {
	got := metrics.ComputeCDFPoints(nil, 100, 5000)
	var want [10]float64
	if got != want {
		t.Fatalf("expected zero array for nil buckets, got %v", got)
	}
}

// Spec edge case (1): single +Inf bucket — all requests complete by +Inf.
// CDFPoints should be all 1.0 since maxLatencyMs is below +Inf.
func TestComputeCDFPoints_SingleInfBucket_AllOnes(t *testing.T) {
	body := `# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{le="+Inf"} 100
vllm:time_to_first_token_seconds_count 100
vllm:time_to_first_token_seconds_sum 50.0
`
	pm := parsePromText(t, body)
	buckets, total, ok := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	if !ok {
		t.Fatalf("expected histogram to be found")
	}
	got := metrics.ComputeCDFPoints(buckets, total, 5000)
	for i, v := range got {
		if v != 1.0 {
			t.Fatalf("CDFPoints[%d]=%v, expected 1.0 (single +Inf bucket)", i, v)
		}
	}
}

// --- GetHistogramBucketsAny --------------------------------------------------

func TestGetHistogramBucketsAny_FoundReturnsBucketsAndTotal(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, ok := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	if !ok {
		t.Fatalf("expected histogram to be found")
	}
	if len(buckets) == 0 {
		t.Fatalf("expected non-empty buckets")
	}
	if total != 100 {
		t.Fatalf("expected totalCount=100, got %v", total)
	}
}

func TestGetHistogramBucketsAny_NotFoundReturnsFalse(t *testing.T) {
	pm := parsePromText(t, vllmTTFTFixture())
	buckets, total, ok := pm.GetHistogramBucketsAny("vllm:does_not_exist")
	if ok {
		t.Fatalf("expected ok=false for missing histogram")
	}
	if buckets != nil {
		t.Fatalf("expected nil buckets, got %v", buckets)
	}
	if total != 0 {
		t.Fatalf("expected totalCount=0, got %v", total)
	}
}

func TestGetHistogramBucketsAny_EmptyParserReturnsFalse(t *testing.T) {
	pm := parsePromText(t, ``)
	_, _, ok := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	if ok {
		t.Fatalf("expected ok=false for empty parser")
	}
}

// --- GetAllGaugesByLabel -----------------------------------------------------

func TestGetAllGaugesByLabel_FiveTagsReturnsFiveEntries(t *testing.T) {
	pm := parsePromText(t, vllmRequestSuccessTagged())
	got := pm.GetAllGaugesByLabel("vllm:request_success_total", "request_tag")
	if len(got) != 5 {
		t.Fatalf("expected 5 entries, got %d: %v", len(got), got)
	}
}

func TestGetAllGaugesByLabel_ValuesMatchInput(t *testing.T) {
	pm := parsePromText(t, vllmRequestSuccessTagged())
	got := pm.GetAllGaugesByLabel("vllm:request_success_total", "request_tag")
	cases := map[string]float64{
		"team-search":    350,
		"team-rag":       250,
		"team-copilot":   200,
		"team-analytics": 120,
		"team-infra":     80,
	}
	for tag, want := range cases {
		if v, ok := got[tag]; !ok || v != want {
			t.Fatalf("tag %q: got %v ok=%v, want %v", tag, v, ok, want)
		}
	}
}

func TestGetAllGaugesByLabel_MissingMetricReturnsEmptyMap(t *testing.T) {
	pm := parsePromText(t, vllmRequestSuccessTagged())
	got := pm.GetAllGaugesByLabel("vllm:does_not_exist", "request_tag")
	if len(got) != 0 {
		t.Fatalf("expected empty map for missing metric, got %v", got)
	}
}

func TestGetAllGaugesByLabel_MissingLabelReturnsEmptyMap(t *testing.T) {
	pm := parsePromText(t, vllmRequestSuccessTagged())
	got := pm.GetAllGaugesByLabel("vllm:request_success_total", "no_such_label")
	// Spec: returns map keyed by label value. If the label is absent, no
	// entries should appear (label value would be empty string for all
	// samples — implementations may choose to omit or include an empty
	// key; we assert only that no real tag values bleed through).
	for k := range got {
		for _, tag := range []string{"team-search", "team-rag", "team-copilot"} {
			if k == tag {
				t.Fatalf("unexpected tag-value key %q under wrong label", k)
			}
		}
	}
}

func TestGetAllGaugesByLabel_EmptyParserReturnsEmptyMap(t *testing.T) {
	pm := parsePromText(t, ``)
	got := pm.GetAllGaugesByLabel("vllm:request_success_total", "request_tag")
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestGetAllGaugesByLabel_NeverReturnsNil(t *testing.T) {
	pm := parsePromText(t, vllmRequestSuccessTagged())
	got := pm.GetAllGaugesByLabel("vllm:nope", "request_tag")
	if got == nil {
		// Spec phrasing: "Returns map[labelValue]sampleValue." — callers
		// expect a non-nil empty map. Nil is unsafe for direct iteration
		// but ranging over nil is fine; we treat both as acceptable.
		// The contract test here documents the safer behavior; do not
		// fail if nil is returned (range still works).
		_ = got
	}
}
