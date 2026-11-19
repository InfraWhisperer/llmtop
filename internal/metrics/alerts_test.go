package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestAlertManagerFiresITLSpike(t *testing.T) {
	am := NewAlertManager()

	state := &AlertState{
		Workers: []*WorkerMetrics{
			{Label: "w1", Online: true, ITL_P99: 3000, KVCacheUsagePct: 60},
		},
	}

	// First eval — starts pending timer
	am.Evaluate(state)
	alerts := am.All()
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts on first eval (pending), got %d", len(alerts))
	}

	// Simulate 11s passing by backdating the pending entry
	am.mu.Lock()
	for k := range am.pending {
		am.pending[k] = time.Now().Add(-11 * time.Second)
	}
	am.mu.Unlock()

	am.Evaluate(state)
	alerts = am.All()
	found := false
	for _, a := range alerts {
		if a.Severity == AlertCritical && a.Source == "w1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CRITICAL alert for w1 ITL spike, got %v", alerts)
	}
}

func TestAlertManagerFiresScrapeFailure(t *testing.T) {
	am := NewAlertManager()

	state := &AlertState{
		Workers: []*WorkerMetrics{
			{Label: "w1", Online: false},
		},
	}

	// First eval — pending
	am.Evaluate(state)

	// Fast-forward pending timer
	am.mu.Lock()
	for k := range am.pending {
		am.pending[k] = time.Now().Add(-16 * time.Second)
	}
	am.mu.Unlock()

	am.Evaluate(state)
	_, _, c := am.Counts()
	if c == 0 {
		t.Error("expected at least 1 critical alert for scrape failure")
	}
}

func TestAlertManagerResolvesOnRecovery(t *testing.T) {
	am := NewAlertManager()

	// Fire an ECC error alert (no delay required)
	state := &AlertState{
		GPUs: []*GPUInfo{
			{Hostname: "node1", Index: 0, ECCErrors: 5, Pod: "w1"},
		},
	}
	am.Evaluate(state)

	_, _, c := am.Counts()
	if c == 0 {
		t.Fatal("expected ECC alert to fire")
	}

	// Resolve: ECC errors cleared
	state.GPUs[0].ECCErrors = 0
	am.Evaluate(state)

	_, _, c = am.Counts()
	if c != 0 {
		t.Errorf("expected 0 active critical alerts after resolution, got %d", c)
	}

	// Resolved alert should appear in All()
	all := am.All()
	found := false
	for _, a := range all {
		if !a.IsActive() {
			found = true
		}
	}
	if !found {
		t.Error("expected resolved alert in All() output")
	}
}

func TestAlertManagerKVPressure(t *testing.T) {
	am := NewAlertManager()

	state := &AlertState{
		Workers: []*WorkerMetrics{
			{Label: "w1", Online: true, KVCacheUsagePct: 80},
		},
	}

	// KV pressure has 30s delay — first eval starts pending
	am.Evaluate(state)
	_, w, _ := am.Counts()
	if w != 0 {
		t.Fatalf("expected no warnings on first eval, got %d", w)
	}

	// Fast-forward
	am.mu.Lock()
	for k := range am.pending {
		am.pending[k] = time.Now().Add(-31 * time.Second)
	}
	am.mu.Unlock()

	am.Evaluate(state)
	_, w, _ = am.Counts()
	if w == 0 {
		t.Error("expected kv_pressure warning after 30s")
	}
}

func TestAlertManagerPrefillQueue(t *testing.T) {
	am := NewAlertManager()

	state := &AlertState{
		Workers: []*WorkerMetrics{
			{Label: "prefill-1", Online: true, Role: "prefill", RequestsWaiting: 8},
		},
	}

	am.Evaluate(state)
	// Fast-forward 10s delay
	am.mu.Lock()
	for k := range am.pending {
		am.pending[k] = time.Now().Add(-11 * time.Second)
	}
	am.mu.Unlock()

	am.Evaluate(state)
	_, w, _ := am.Counts()
	if w == 0 {
		t.Error("expected prefill_queue warning")
	}
}

func TestAlertManagerDCGMStale(t *testing.T) {
	am := NewAlertManager()

	state := &AlertState{
		GPUs: []*GPUInfo{
			{Hostname: "node1", Index: 0, LastScrape: time.Now().Add(-20 * time.Second)},
		},
	}

	am.Evaluate(state)
	_, w, _ := am.Counts()
	if w == 0 {
		t.Error("expected dcgm_stale warning")
	}
}

func TestAlertManagerECCError(t *testing.T) {
	am := NewAlertManager()

	state := &AlertState{
		GPUs: []*GPUInfo{
			{Hostname: "node1", Index: 2, ECCErrors: 3, Pod: "vllm-0"},
		},
	}

	am.Evaluate(state)
	_, _, c := am.Counts()
	if c == 0 {
		t.Error("expected ECC error critical alert")
	}
}

// cacheHitState builds an AlertState whose previous baseline is the given value
// and measured-ness, with a fresh cluster reading. cache_hit_drop has no sustain
// delay, so a single Evaluate is enough to see whether it fires.
func cacheHitState(summary FleetSummary, prev float64, prevMeasured bool, prevAge time.Duration) *AlertState {
	return &AlertState{
		Summary:              summary,
		PrevCacheHit:         prev,
		PrevCacheHitMeasured: prevMeasured,
		PrevCacheHitAt:       time.Now().Add(-prevAge),
		CacheHitWindowAgeSec: 2,
	}
}

// The rule compares like with like: it reads the windowed fleet mean when any
// worker measured a window, and only falls back to the cumulative mean when none
// did (gauge-only backends). See FleetSummary.CacheHitAlertPct.
func TestCacheHitAlertPctBasis(t *testing.T) {
	tests := []struct {
		name    string
		summary FleetSummary
		want    float64
		wantOK  bool
	}{
		{
			name:    "window preferred over cumulative",
			summary: FleetSummary{AvgCacheHit: 89, CacheHitWorkers: 4, AvgCacheHitWindow: 12, CacheHitWindowWorkers: 2},
			want:    12, wantOK: true,
		},
		{
			name:    "windowed zero is a reading, not a fallback",
			summary: FleetSummary{AvgCacheHit: 89, CacheHitWorkers: 4, CacheHitWindowWorkers: 2},
			want:    0, wantOK: true,
		},
		{
			name:    "cumulative fallback when no window",
			summary: FleetSummary{AvgCacheHit: 33, CacheHitWorkers: 1},
			want:    33, wantOK: true,
		},
		{
			name:    "nothing reports the metric",
			summary: FleetSummary{TotalWorkers: 3, OnlineWorkers: 3},
			want:    0, wantOK: false,
		},
	}
	for _, tc := range tests {
		got, ok := tc.summary.CacheHitAlertPct()
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: CacheHitAlertPct() = %v, %v, want %v, %v", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestAlertManagerCacheHitDrop(t *testing.T) {
	tests := []struct {
		name      string
		state     *AlertState
		wantFired bool
	}{
		{
			name: "cumulative drop on a gauge-only fleet",
			state: cacheHitState(FleetSummary{AvgCacheHit: 20, CacheHitWorkers: 2},
				40, true, 30*time.Second),
			wantFired: true,
		},
		{
			// The case the lifetime ratio could never produce: reuse collapsed to
			// nothing this window while the cumulative ratio still reads high.
			name: "window collapses to a measured zero",
			state: cacheHitState(FleetSummary{AvgCacheHit: 89, CacheHitWorkers: 4, CacheHitWindowWorkers: 4},
				89, true, 30*time.Second),
			wantFired: true,
		},
		{
			// A baseline of 0 that was never measured is “no data yet”, and
			// comparing against it would fire on missing data rather than on a drop.
			name: "no baseline yet",
			state: cacheHitState(FleetSummary{AvgCacheHit: 20, CacheHitWorkers: 2},
				0, false, time.Second),
			wantFired: false,
		},
		{
			name: "fleet reports no cache-hit metric",
			state: cacheHitState(FleetSummary{TotalWorkers: 3, OnlineWorkers: 3},
				40, true, 30*time.Second),
			wantFired: false,
		},
		{
			name: "baseline older than the comparison window",
			state: cacheHitState(FleetSummary{AvgCacheHit: 20, CacheHitWorkers: 2},
				40, true, 5*time.Minute),
			wantFired: false,
		},
		{
			name: "drop within threshold",
			state: cacheHitState(FleetSummary{AvgCacheHit: 33, CacheHitWorkers: 2},
				40, true, 30*time.Second),
			wantFired: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			am := NewAlertManager()
			am.Evaluate(tc.state)
			_, w, _ := am.Counts()
			if fired := w > 0; fired != tc.wantFired {
				t.Fatalf("fired = %v, want %v (alerts: %v)", fired, tc.wantFired, titlesOf(am.All()))
			}
		})
	}
}

func titlesOf(alerts []*Alert) []string {
	out := make([]string, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, a.Title)
	}
	return out
}

// The alert must state what it compared, or an operator cannot tell a windowed
// collapse from a slow cumulative drift.
func TestAlertManagerCacheHitDropStatesBasis(t *testing.T) {
	am := NewAlertManager()
	am.Evaluate(cacheHitState(FleetSummary{AvgCacheHit: 89, CacheHitWorkers: 4, AvgCacheHitWindow: 12, CacheHitWindowWorkers: 2},
		89, true, 30*time.Second))
	alerts := am.All()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if !strings.Contains(alerts[0].Title, "89% → 12%") {
		t.Errorf("title %q, want the windowed before → after rates", alerts[0].Title)
	}
	if !strings.Contains(alerts[0].Detail, "2s window") {
		t.Errorf("detail %q, want the window length it compared over", alerts[0].Detail)
	}
}

func TestAlertSeverityString(t *testing.T) {
	tests := []struct {
		sev  AlertSeverity
		want string
	}{
		{AlertInfo, "INFO"},
		{AlertWarning, "WARN"},
		{AlertCritical, "CRIT"},
	}
	for _, tt := range tests {
		if got := tt.sev.String(); got != tt.want {
			t.Errorf("AlertSeverity(%d).String() = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

func TestDefaultAlertThresholds_Production(t *testing.T) {
	def := DefaultAlertThresholds()
	if def.KVCriticalPct != 90 || def.KVPressurePct != 75 {
		t.Errorf("default KV thresholds wrong: %+v", def)
	}
	if def.ITLSpikeMs != 2000 || def.PrefillQueueDepth != 5 {
		t.Errorf("default rule thresholds wrong: %+v", def)
	}
	if def.KVCriticalDelay != 5*time.Second || def.KVPressureDelay != 30*time.Second {
		t.Errorf("default delays wrong: %+v", def)
	}
}

func TestNewAlertManagerWithThresholds_RespectsCustomKVCritical(t *testing.T) {
	t1 := DefaultAlertThresholds()
	t1.KVCriticalPct = 80
	t1.KVCriticalDelay = 0
	am := NewAlertManagerWithThresholds(t1)
	state := &AlertState{
		Workers: []*WorkerMetrics{{Label: "w", Online: true, KVCacheUsagePct: 85}},
	}
	am.Evaluate(state)
	_, _, crit := am.Counts()
	if crit < 1 {
		t.Errorf("expected critical alert at 85 with threshold 80, got %d", crit)
	}
}

func TestAlertManager_Thresholds_ReturnsCopy(t *testing.T) {
	thr := DefaultAlertThresholds()
	thr.KVCriticalPct = 88
	am := NewAlertManagerWithThresholds(thr)
	if am.Thresholds().KVCriticalPct != 88 {
		t.Errorf("expected stored threshold 88, got %f", am.Thresholds().KVCriticalPct)
	}
}

func TestAlertManagerMaxResolved(t *testing.T) {
	am := NewAlertManager()
	am.maxResolved = 3

	// Fire and resolve 5 ECC alerts on different GPUs
	for i := range 5 {
		state := &AlertState{
			GPUs: []*GPUInfo{
				{Hostname: "node1", Index: i, ECCErrors: 1},
			},
		}
		am.Evaluate(state)
	}
	// Resolve all by clearing
	am.Evaluate(&AlertState{})

	am.mu.Lock()
	resolved := len(am.resolved)
	am.mu.Unlock()

	if resolved > 3 {
		t.Errorf("expected max 3 resolved alerts, got %d", resolved)
	}
}
