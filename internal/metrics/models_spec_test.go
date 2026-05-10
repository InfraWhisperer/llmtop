// SPDX-License-Identifier: Apache-2.0
// Spec-only contract tests for internal/metrics/models.go.
// Source of truth: docs/feature-spec.md sections F1, F2, F3, F6, F7.
// Black-box: tests construct WorkerMetrics directly and assert invariants
// on the type contract — JSON tags, field defaults, nil-safety.

package metrics_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// --- WorkerMetrics field invariants -----------------------------------------

func TestWorkerMetrics_KVCacheUsageNVMePct_ZeroValue(t *testing.T) {
	w := metrics.WorkerMetrics{}
	if w.KVCacheUsageNVMePct != 0 {
		t.Fatalf("expected zero default, got %v", w.KVCacheUsageNVMePct)
	}
}

func TestWorkerMetrics_KVTransferP99Ms_ZeroValue(t *testing.T) {
	w := metrics.WorkerMetrics{}
	if w.KVTransferP99Ms != 0 {
		t.Fatalf("expected zero default, got %v", w.KVTransferP99Ms)
	}
}

func TestWorkerMetrics_TenantReqs_NilDefault(t *testing.T) {
	w := metrics.WorkerMetrics{}
	if w.TenantReqs != nil {
		t.Fatalf("expected nil default, got %v", w.TenantReqs)
	}
	// Spec invariant: nil map and zero-length map are semantically equivalent
	// to "no tenant labels observed". Iteration over nil must not panic.
	for range w.TenantReqs {
		t.Fatalf("nil map should not yield entries")
	}
}

func TestWorkerMetrics_TTFT_ZeroValue_IsZeroSnapshot(t *testing.T) {
	w := metrics.WorkerMetrics{}
	if w.TTFT != (metrics.LatencySnapshot{}) {
		t.Fatalf("expected zero LatencySnapshot, got %#v", w.TTFT)
	}
}

func TestWorkerMetrics_ITL_ZeroValue_IsZeroSnapshot(t *testing.T) {
	w := metrics.WorkerMetrics{}
	if w.ITL != (metrics.LatencySnapshot{}) {
		t.Fatalf("expected zero LatencySnapshot, got %#v", w.ITL)
	}
}

// --- WorkerMetrics JSON tags (F7) -------------------------------------------

func TestWorkerMetrics_JSON_KVCacheUsageNVMePct_Tag(t *testing.T) {
	w := metrics.WorkerMetrics{KVCacheUsageNVMePct: 42.5}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := raw["kv_cache_usage_nvme_pct"]
	if !ok {
		t.Fatalf("missing kv_cache_usage_nvme_pct key, got: %v", raw)
	}
	if v.(float64) != 42.5 {
		t.Fatalf("expected 42.5, got %v", v)
	}
}

func TestWorkerMetrics_JSON_KVCacheUsageNVMePct_PresentWhenZero(t *testing.T) {
	w := metrics.WorkerMetrics{}
	b, _ := json.Marshal(w)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["kv_cache_usage_nvme_pct"]; !ok {
		t.Fatalf("kv_cache_usage_nvme_pct must be present even when zero (no omitempty)")
	}
}

func TestWorkerMetrics_JSON_KVTransferP99Ms_Tag(t *testing.T) {
	w := metrics.WorkerMetrics{KVTransferP99Ms: 12.5}
	b, _ := json.Marshal(w)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	v, ok := raw["kv_transfer_p99_ms"]
	if !ok {
		t.Fatalf("missing kv_transfer_p99_ms key")
	}
	if v.(float64) != 12.5 {
		t.Fatalf("expected 12.5, got %v", v)
	}
}

func TestWorkerMetrics_JSON_KVTransferP99Ms_PresentWhenZero(t *testing.T) {
	w := metrics.WorkerMetrics{}
	b, _ := json.Marshal(w)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["kv_transfer_p99_ms"]; !ok {
		t.Fatalf("kv_transfer_p99_ms must be present even when zero")
	}
}

func TestWorkerMetrics_JSON_TenantReqs_OmitemptyWhenNil(t *testing.T) {
	w := metrics.WorkerMetrics{}
	b, _ := json.Marshal(w)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["tenant_reqs"]; ok {
		t.Fatalf("tenant_reqs must be omitted when nil (omitempty)")
	}
}

func TestWorkerMetrics_JSON_TenantReqs_PresentWhenPopulated(t *testing.T) {
	w := metrics.WorkerMetrics{TenantReqs: map[string]float64{"team-search": 350}}
	b, _ := json.Marshal(w)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	tr, ok := raw["tenant_reqs"]
	if !ok {
		t.Fatalf("tenant_reqs missing when populated")
	}
	m, ok := tr.(map[string]any)
	if !ok {
		t.Fatalf("tenant_reqs is not an object: %T", tr)
	}
	if m["team-search"].(float64) != 350 {
		t.Fatalf("expected team-search=350, got %v", m["team-search"])
	}
}

func TestWorkerMetrics_JSON_TTFT_NestedTags(t *testing.T) {
	w := metrics.WorkerMetrics{
		TTFT: metrics.LatencySnapshot{P50: 100, P95: 200, P99: 300},
	}
	b, _ := json.Marshal(w)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	tt, ok := raw["ttft"]
	if !ok {
		t.Fatalf("missing ttft key")
	}
	m, ok := tt.(map[string]any)
	if !ok {
		t.Fatalf("ttft is not an object")
	}
	for _, key := range []string{"p50", "p95", "p99", "cdf_points"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("ttft missing nested key %q", key)
		}
	}
}

func TestWorkerMetrics_JSON_RoleAndNodeName_PresentInOutput(t *testing.T) {
	// Spec section 1 (current-state): Role and NodeName already have JSON tags.
	// F7 acceptance: every worker JSON object includes node_name and role.
	w := metrics.WorkerMetrics{Role: "prefill", NodeName: "sim-node-01"}
	b, _ := json.Marshal(w)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if raw["role"] != "prefill" {
		t.Fatalf("role missing or wrong: %v", raw["role"])
	}
	if raw["node_name"] != "sim-node-01" {
		t.Fatalf("node_name missing or wrong: %v", raw["node_name"])
	}
}

// --- LatencySnapshot invariants (F2) ----------------------------------------

func TestLatencySnapshot_ZeroValue_AllZeros(t *testing.T) {
	var ls metrics.LatencySnapshot
	if ls.P50 != 0 || ls.P95 != 0 || ls.P99 != 0 {
		t.Fatalf("expected zero quantiles, got %#v", ls)
	}
	for i, v := range ls.CDFPoints {
		if v != 0 {
			t.Fatalf("CDFPoints[%d]=%v, expected 0", i, v)
		}
	}
}

func TestLatencySnapshot_JSON_RoundTripPreservesAllFields(t *testing.T) {
	ls := metrics.LatencySnapshot{
		P50: 100, P95: 250, P99: 500,
		CDFPoints: [10]float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	}
	b, err := json.Marshal(ls)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got metrics.LatencySnapshot
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != ls {
		t.Fatalf("round-trip mismatch: got %#v want %#v", got, ls)
	}
}

func TestLatencySnapshot_JSON_ZeroValueShape(t *testing.T) {
	var ls metrics.LatencySnapshot
	b, _ := json.Marshal(ls)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	for _, key := range []string{"p50", "p95", "p99", "cdf_points"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("zero LatencySnapshot missing JSON key %q", key)
		}
	}
	cdf, ok := raw["cdf_points"].([]any)
	if !ok {
		t.Fatalf("cdf_points is not an array: %T", raw["cdf_points"])
	}
	if len(cdf) != 10 {
		t.Fatalf("expected 10-element cdf_points array, got %d", len(cdf))
	}
	for i, v := range cdf {
		if v.(float64) != 0 {
			t.Fatalf("cdf_points[%d]=%v, expected 0", i, v)
		}
	}
}

// --- TenantMetrics invariants (F3) ------------------------------------------

func TestTenantMetrics_ZeroValue_DefaultsAreSafe(t *testing.T) {
	var tm metrics.TenantMetrics
	if tm.Tag != "" || tm.RequestShare != 0 || tm.TokPerSec != 0 || tm.TTFT_P99Ms != 0 {
		t.Fatalf("expected zero defaults, got %#v", tm)
	}
}

func TestTenantMetrics_JSON_AllTags(t *testing.T) {
	tm := metrics.TenantMetrics{Tag: "team-search", RequestShare: 0.35, TokPerSec: 1200, TTFT_P99Ms: 450}
	b, _ := json.Marshal(tm)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	for _, key := range []string{"tag", "request_share", "tok_per_sec", "ttft_p99_ms"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing JSON key %q in TenantMetrics", key)
		}
	}
	if raw["tag"] != "team-search" {
		t.Fatalf("tag mismatch: %v", raw["tag"])
	}
}

func TestTenantMetrics_JSON_RoundTrip(t *testing.T) {
	tm := metrics.TenantMetrics{Tag: "team-rag", RequestShare: 0.25, TokPerSec: 890}
	b, _ := json.Marshal(tm)
	var got metrics.TenantMetrics
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != tm {
		t.Fatalf("round-trip mismatch: got %#v want %#v", got, tm)
	}
}

// --- ModelGroup.TopTenants invariants (F3) ----------------------------------

func TestModelGroup_TopTenants_NilWhenNoTenants(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{ModelName: "m", Backend: metrics.BackendVLLM, Online: true},
	}
	groups := metrics.GroupWorkersByModel(workers)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0].TopTenants) != 0 {
		t.Fatalf("expected empty TopTenants when no tenant labels, got %v", groups[0].TopTenants)
	}
}

func TestModelGroup_TopTenants_SortedByRequestShareDescending(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{
			ModelName: "m", Backend: metrics.BackendVLLM, Online: true,
			TenantReqs: map[string]float64{
				"team-rag":      250,
				"team-search":   500,
				"team-copilot":  100,
				"team-infra":    50,
				"team-analytics": 25,
			},
		},
	}
	groups := metrics.GroupWorkersByModel(workers)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	tt := groups[0].TopTenants
	if !sort.SliceIsSorted(tt, func(i, j int) bool { return tt[i].RequestShare > tt[j].RequestShare }) {
		t.Fatalf("TopTenants not sorted by RequestShare descending: %v", tt)
	}
}

func TestModelGroup_TopTenants_AtMostFiveEntries(t *testing.T) {
	tenantReqs := map[string]float64{}
	for i, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		tenantReqs[name] = float64((i + 1) * 100)
	}
	workers := []*metrics.WorkerMetrics{
		{
			ModelName: "m", Backend: metrics.BackendVLLM, Online: true,
			TenantReqs: tenantReqs,
		},
	}
	groups := metrics.GroupWorkersByModel(workers)
	if len(groups[0].TopTenants) > 5 {
		t.Fatalf("TopTenants exceeded 5: got %d", len(groups[0].TopTenants))
	}
}

func TestModelGroup_TopTenants_RequestShareSumBoundedByOne(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{
			ModelName: "m", Backend: metrics.BackendVLLM, Online: true,
			TenantReqs: map[string]float64{
				"a": 100, "b": 200, "c": 300,
			},
		},
	}
	groups := metrics.GroupWorkersByModel(workers)
	var sum float64
	for _, tt := range groups[0].TopTenants {
		sum += tt.RequestShare
	}
	// Allow a tiny float tolerance for rounding.
	if sum > 1.0+1e-9 {
		t.Fatalf("sum(RequestShare)=%v exceeds 1.0", sum)
	}
}

func TestModelGroup_TopTenants_RequestShareInUnitInterval(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{
			ModelName: "m", Backend: metrics.BackendVLLM, Online: true,
			TenantReqs: map[string]float64{"a": 100, "b": 200},
		},
	}
	groups := metrics.GroupWorkersByModel(workers)
	for _, tt := range groups[0].TopTenants {
		if tt.RequestShare < 0 || tt.RequestShare > 1.0+1e-9 {
			t.Fatalf("RequestShare=%v out of [0,1] for tag %q", tt.RequestShare, tt.Tag)
		}
	}
}

func TestModelGroup_TopTenants_OfflineWorkersExcluded(t *testing.T) {
	// Spec edge case 5 (F3): offline workers do not contribute to TopTenants.
	workers := []*metrics.WorkerMetrics{
		{
			ModelName: "m", Backend: metrics.BackendVLLM, Online: false,
			TenantReqs: map[string]float64{"only-offline-tag": 9999},
		},
	}
	groups := metrics.GroupWorkersByModel(workers)
	for _, g := range groups {
		for _, tt := range g.TopTenants {
			if tt.Tag == "only-offline-tag" {
				t.Fatalf("offline worker's tenant tag leaked into TopTenants")
			}
		}
	}
}

func TestModelGroup_JSON_TopTenants_OmitemptyWhenEmpty(t *testing.T) {
	g := metrics.ModelGroup{ModelName: "m"}
	b, _ := json.Marshal(g)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["top_tenants"]; ok {
		t.Fatalf("top_tenants should be omitted when empty (omitempty)")
	}
}

func TestModelGroup_JSON_TopTenants_PresentWhenPopulated(t *testing.T) {
	g := metrics.ModelGroup{
		ModelName:  "m",
		TopTenants: []metrics.TenantMetrics{{Tag: "x", RequestShare: 0.5}},
	}
	b, _ := json.Marshal(g)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	tt, ok := raw["top_tenants"]
	if !ok {
		t.Fatalf("top_tenants missing when populated")
	}
	if arr, _ := tt.([]any); len(arr) != 1 {
		t.Fatalf("expected 1-element array, got %v", tt)
	}
}
