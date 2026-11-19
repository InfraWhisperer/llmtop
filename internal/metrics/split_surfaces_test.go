package metrics

import (
	"testing"
	"time"
)

// sglangSplitWorker reuses 25_000 tok/s of prompt tokens from its prefix cache
// while computing 900 and generating 150 — the shape that produced the 25k tok/s
// display of issue #15.
func sglangSplitWorker() *WorkerMetrics {
	return &WorkerMetrics{
		Endpoint:                "http://localhost:8100",
		Backend:                 BackendSGLang,
		ModelName:               "llama-70b",
		Online:                  true,
		LastSeen:                time.Now(),
		PromptTokPerSec:         25900,
		GenTokPerSec:            150,
		PrefillSplit:            true,
		PrefillComputeTokPerSec: 900,
		PrefillCacheTokPerSec:   25000,
		CacheHitRatePct:         96,
	}
}

func vllmUnsplitWorker() *WorkerMetrics {
	return &WorkerMetrics{
		Endpoint:        "http://localhost:8000",
		Backend:         BackendVLLM,
		ModelName:       "llama-70b",
		Online:          true,
		LastSeen:        time.Now(),
		PromptTokPerSec: 900,
		GenTokPerSec:    150,
	}
}

// TestFleetSummarySegmentsExcludeReusedTokens pins the invariant the header tiers
// render against: the blended total is gen + computed prefill, and the reused half
// is reported separately instead of inside the total.
func TestFleetSummarySegmentsExcludeReusedTokens(t *testing.T) {
	s := ComputeFleetSummary([]*WorkerMetrics{sglangSplitWorker(), vllmUnsplitWorker()})

	if got, want := s.TotalTokPerSec, s.GenTokPerSec+s.PrefillComputeTokPerSec; got != want {
		t.Errorf("TotalTokPerSec = %v, want gen+prefill = %v", got, want)
	}
	if s.GenTokPerSec != 300 {
		t.Errorf("GenTokPerSec = %v, want 300", s.GenTokPerSec)
	}
	if s.PrefillComputeTokPerSec != 1800 {
		t.Errorf("PrefillComputeTokPerSec = %v, want 1800 (900 computed + 900 vLLM prompt)", s.PrefillComputeTokPerSec)
	}
	if s.PrefillCacheTokPerSec != 25000 {
		t.Errorf("PrefillCacheTokPerSec = %v, want 25000", s.PrefillCacheTokPerSec)
	}
	if s.TotalTokPerSec >= 25900 {
		t.Errorf("fleet total (%v) still carries the cache-inflated prompt rate", s.TotalTokPerSec)
	}
	// Only the worker whose backend reports the split counts toward the capability
	// gate, so a mixed fleet renders "cached" from real data rather than a 0
	// contributed by backends that never send the metric.
	if s.SplitWorkers != 1 {
		t.Errorf("SplitWorkers = %d, want 1", s.SplitWorkers)
	}
}

// TestFleetSummaryCacheHitWorkersSeparatesNoDataFromZero — surfaces render an em
// dash for a fleet that reports no cache-hit metric and a real 0% for one that
// reports a genuine zero.
func TestFleetSummaryCacheHitWorkersSeparatesNoDataFromZero(t *testing.T) {
	none := ComputeFleetSummary([]*WorkerMetrics{{Online: true, LastSeen: time.Now()}})
	if none.CacheHitWorkers != 0 || none.AvgCacheHit != 0 {
		t.Errorf("no cache-hit metric: CacheHitWorkers=%d AvgCacheHit=%v, want 0/0", none.CacheHitWorkers, none.AvgCacheHit)
	}

	windowOnly := ComputeFleetSummary([]*WorkerMetrics{{
		Online: true, LastSeen: time.Now(), CacheHitWindowPct: 40,
	}})
	if windowOnly.CacheHitWorkers != 1 {
		t.Errorf("worker reporting only a window hit rate should be counted, got %d", windowOnly.CacheHitWorkers)
	}
	if windowOnly.AvgCacheHit != 40 {
		t.Errorf("AvgCacheHit = %v, want the windowed 40 (current activity, not lifetime)", windowOnly.AvgCacheHit)
	}
}

// TestReportsCacheHit is the capability gate for the hit-rate surfaces.
func TestReportsCacheHit(t *testing.T) {
	tests := []struct {
		name string
		w    WorkerMetrics
		want bool
	}{
		{"no metric at all", WorkerMetrics{}, false},
		{"lifetime only", WorkerMetrics{CacheHitRatePct: 12}, true},
		{"window only", WorkerMetrics{CacheHitWindowPct: 12}, true},
	}
	for _, tc := range tests {
		if got := tc.w.ReportsCacheHit(); got != tc.want {
			t.Errorf("%s: ReportsCacheHit() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestGroupWorkersByModelSplitsBothSides — the model view must be able to tell a
// prefill-bound fleet from a decode-bound one, and its two halves must add back up
// to the group total for every backend.
func TestGroupWorkersByModelSplitsBothSides(t *testing.T) {
	groups := GroupWorkersByModel([]*WorkerMetrics{sglangSplitWorker(), vllmUnsplitWorker()})
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(groups))
	}
	g := groups[0]

	if g.PrefillTokPerSec != 1800 {
		t.Errorf("PrefillTokPerSec = %v, want 1800 (compute halves only)", g.PrefillTokPerSec)
	}
	if g.GenTokPerSec != 300 {
		t.Errorf("GenTokPerSec = %v, want 300", g.GenTokPerSec)
	}
	if g.TotalTokPerSec != g.PrefillTokPerSec+g.GenTokPerSec {
		t.Errorf("TotalTokPerSec = %v, want prefill+gen = %v", g.TotalTokPerSec, g.PrefillTokPerSec+g.GenTokPerSec)
	}
	if g.TotalTokPerSec >= 25900 {
		t.Errorf("group total %v still includes reused prompt tokens", g.TotalTokPerSec)
	}
}

// TestGroupWorkersByModelTenantShareFollowsDisplayRate — tenant tok/s is apportioned
// from the same per-worker rate the table shows, so a cache-heavy worker cannot
// inflate the tenant that happened to send its traffic.
func TestGroupWorkersByModelTenantShareFollowsDisplayRate(t *testing.T) {
	split := sglangSplitWorker()
	split.TenantReqs = map[string]float64{"team-a": 1}
	split.ModelName = "split-only"

	groups := GroupWorkersByModel([]*WorkerMetrics{split})
	if len(groups) != 1 || len(groups[0].TopTenants) != 1 {
		t.Fatalf("want one group with one tenant, got %+v", groups)
	}
	tok := groups[0].TopTenants[0]
	if tok.Tag != "team-a" {
		t.Fatalf("tenant tag = %q", tok.Tag)
	}
	if tok.TokPerSec > 1051 { // prefill compute 900 + gen 150, all one tenant
		t.Errorf("tenant tok/s = %v, want the display rate (1050), not %v", tok.TokPerSec, split.PromptTokPerSec+split.GenTokPerSec)
	}
}
