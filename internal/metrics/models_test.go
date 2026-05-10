package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestComputeFleetSummary(t *testing.T) {
	workers := []*WorkerMetrics{
		{
			Online:          true,
			LastSeen:        time.Now(),
			CacheHitRatePct: 70.0,
			TTFT_P99:        200.0,
			GenTokPerSec:    100.0,
			PromptTokPerSec: 50.0,
		},
		{
			Online:          true,
			LastSeen:        time.Now(),
			CacheHitRatePct: 80.0,
			TTFT_P99:        300.0,
			GenTokPerSec:    200.0,
			PromptTokPerSec: 100.0,
		},
		{
			Online: false, // offline worker
		},
	}

	summary := ComputeFleetSummary(workers)

	if summary.TotalWorkers != 3 {
		t.Errorf("expected TotalWorkers=3, got %d", summary.TotalWorkers)
	}
	if summary.OnlineWorkers != 2 {
		t.Errorf("expected OnlineWorkers=2, got %d", summary.OnlineWorkers)
	}
	if summary.TotalTokPerSec != 450.0 {
		t.Errorf("expected TotalTokPerSec=450.0, got %f", summary.TotalTokPerSec)
	}
	if summary.AvgCacheHit != 75.0 {
		t.Errorf("expected AvgCacheHit=75.0, got %f", summary.AvgCacheHit)
	}
	if summary.P99TTFT != 300.0 {
		t.Errorf("expected P99TTFT=300.0, got %f", summary.P99TTFT)
	}
}

func TestComputeFleetSummary_Empty(t *testing.T) {
	summary := ComputeFleetSummary(nil)
	if summary.TotalWorkers != 0 {
		t.Errorf("expected 0 workers, got %d", summary.TotalWorkers)
	}
}

func TestLatencySnapshot_ZeroJSON(t *testing.T) {
	var ls LatencySnapshot
	out, err := json.Marshal(ls)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"p50":0,"p95":0,"p99":0,"cdf_points":[0,0,0,0,0,0,0,0,0,0]}`
	if string(out) != want {
		t.Errorf("zero LatencySnapshot JSON = %s, want %s", out, want)
	}
}

func TestWorkerMetrics_NewFieldsJSONTags(t *testing.T) {
	w := WorkerMetrics{
		KVCacheUsageNVMePct: 42.5,
		KVTransferP99Ms:     17.0,
		TenantReqs:          map[string]float64{"team-search": 100},
		TTFT:                LatencySnapshot{P50: 100, P95: 200, P99: 300},
	}
	out, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`"kv_cache_usage_nvme_pct":42.5`,
		`"kv_transfer_p99_ms":17`,
		`"tenant_reqs":{"team-search":100}`,
		`"ttft":{"p50":100,"p95":200,"p99":300`,
		`"node_name":""`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("WorkerMetrics JSON missing %q\nfull: %s", want, s)
		}
	}
}

func TestTenantMetrics_JSONTags(t *testing.T) {
	tm := TenantMetrics{Tag: "team-rag", RequestShare: 0.3, TokPerSec: 850, TTFT_P99Ms: 0}
	out, err := json.Marshal(tm)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tag":"team-rag"`, `"request_share":0.3`, `"tok_per_sec":850`, `"ttft_p99_ms":0`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("TenantMetrics JSON missing %q: %s", want, out)
		}
	}
}

func TestModelGroup_TopTenants_TopFiveSortedByShare(t *testing.T) {
	workers := []*WorkerMetrics{
		{
			ModelName: "m", Online: true, Backend: BackendVLLM,
			TenantReqs: map[string]float64{
				"a": 50, "b": 40, "c": 30, "d": 20, "e": 10, "f": 5,
			},
			GenTokPerSec: 1000,
		},
	}
	groups := GroupWorkersByModel(workers)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	tt := groups[0].TopTenants
	if len(tt) != 5 {
		t.Fatalf("expected 5 top tenants, got %d", len(tt))
	}
	if tt[0].Tag != "a" || tt[1].Tag != "b" || tt[2].Tag != "c" || tt[3].Tag != "d" || tt[4].Tag != "e" {
		t.Errorf("expected sorted by share desc, got %+v", tt)
	}
	var sum float64
	for _, v := range tt {
		sum += v.RequestShare
	}
	if sum > 1.000001 {
		t.Errorf("sum of RequestShare must be <= 1.0, got %f", sum)
	}
}

func TestModelGroup_TopTenants_NilWhenAbsent(t *testing.T) {
	workers := []*WorkerMetrics{
		{ModelName: "m", Online: true, Backend: BackendVLLM},
	}
	groups := GroupWorkersByModel(workers)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group")
	}
	if groups[0].TopTenants != nil {
		t.Errorf("expected nil TopTenants when no tenant labels, got %v", groups[0].TopTenants)
	}
}
