package metrics

import (
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

func TestAlertManagerCacheHitDrop(t *testing.T) {
	am := NewAlertManager()

	state := &AlertState{
		Summary:        FleetSummary{AvgCacheHit: 20},
		PrevCacheHit:   40,
		PrevCacheHitAt: time.Now().Add(-30 * time.Second),
	}

	am.Evaluate(state)
	_, w, _ := am.Counts()
	if w == 0 {
		t.Error("expected cache_hit_drop warning")
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
