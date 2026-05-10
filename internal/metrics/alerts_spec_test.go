// SPDX-License-Identifier: Apache-2.0
// Spec-only contract tests for internal/metrics/alerts.go (F4).
// These tests exercise the AlertThresholds contract and the threshold-driven
// AlertManager constructor specified in docs/feature-spec.md F4.

package metrics_test

import (
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// --- DefaultAlertThresholds --------------------------------------------------

func TestDefaultAlertThresholds_KVCriticalPct(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.KVCriticalPct != 90 {
		t.Fatalf("KVCriticalPct: got %v want 90", d.KVCriticalPct)
	}
}

func TestDefaultAlertThresholds_KVPressurePct(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.KVPressurePct != 75 {
		t.Fatalf("KVPressurePct: got %v want 75", d.KVPressurePct)
	}
}

func TestDefaultAlertThresholds_ITLSpikeMs(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.ITLSpikeMs != 2000 {
		t.Fatalf("ITLSpikeMs: got %v want 2000", d.ITLSpikeMs)
	}
}

func TestDefaultAlertThresholds_PrefillQueueDepth(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.PrefillQueueDepth != 5 {
		t.Fatalf("PrefillQueueDepth: got %v want 5", d.PrefillQueueDepth)
	}
}

func TestDefaultAlertThresholds_CacheHitDropPP(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.CacheHitDropPP != 10 {
		t.Fatalf("CacheHitDropPP: got %v want 10", d.CacheHitDropPP)
	}
}

func TestDefaultAlertThresholds_TTFTEventMs(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.TTFTEventMs != 5000 {
		t.Fatalf("TTFTEventMs: got %v want 5000", d.TTFTEventMs)
	}
}

func TestDefaultAlertThresholds_KVEventPct(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.KVEventPct != 75 {
		t.Fatalf("KVEventPct: got %v want 75", d.KVEventPct)
	}
}

func TestDefaultAlertThresholds_KVCriticalDelay_FiveSeconds(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.KVCriticalDelay != 5*time.Second {
		t.Fatalf("KVCriticalDelay: got %v want 5s", d.KVCriticalDelay)
	}
}

func TestDefaultAlertThresholds_KVPressureDelay_ThirtySeconds(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.KVPressureDelay != 30*time.Second {
		t.Fatalf("KVPressureDelay: got %v want 30s", d.KVPressureDelay)
	}
}

func TestDefaultAlertThresholds_ITLSpikeDelay_TenSeconds(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.ITLSpikeDelay != 10*time.Second {
		t.Fatalf("ITLSpikeDelay: got %v want 10s", d.ITLSpikeDelay)
	}
}

func TestDefaultAlertThresholds_PrefillQueueDelay_TenSeconds(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.PrefillQueueDelay != 10*time.Second {
		t.Fatalf("PrefillQueueDelay: got %v want 10s", d.PrefillQueueDelay)
	}
}

func TestDefaultAlertThresholds_ScrapeFailureDelay_FifteenSeconds(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if d.ScrapeFailureDelay != 15*time.Second {
		t.Fatalf("ScrapeFailureDelay: got %v want 15s", d.ScrapeFailureDelay)
	}
}

func TestDefaultAlertThresholds_CriticalGreaterThanPressure(t *testing.T) {
	d := metrics.DefaultAlertThresholds()
	if !(d.KVCriticalPct > d.KVPressurePct) {
		t.Fatalf("invariant violation: KVCriticalPct (%v) must be > KVPressurePct (%v)",
			d.KVCriticalPct, d.KVPressurePct)
	}
}

// --- NewAlertManagerWithThresholds -------------------------------------------

func TestNewAlertManagerWithThresholds_ReturnsNonNil(t *testing.T) {
	am := metrics.NewAlertManagerWithThresholds(metrics.DefaultAlertThresholds())
	if am == nil {
		t.Fatalf("expected non-nil AlertManager")
	}
}

func TestNewAlertManagerWithThresholds_WithDefaults_StartsEmpty(t *testing.T) {
	// A freshly-constructed AlertManager has no firing alerts before any
	// Evaluate call.
	am := metrics.NewAlertManagerWithThresholds(metrics.DefaultAlertThresholds())
	all := am.All()
	for _, a := range all {
		if a == nil {
			continue
		}
		// We don't depend on the Alert internals. Counts() should be the
		// public accessor for active counts.
	}
	info, warn, crit := am.Counts()
	if info != 0 || warn != 0 || crit != 0 {
		t.Fatalf("expected zero counts on fresh manager, got info=%d warn=%d crit=%d", info, warn, crit)
	}
}

// Spec acceptance criterion (F4):
//
//	NewAlertManagerWithThresholds(DefaultAlertThresholds()) behaves identically
//	to NewAlertManager() — same rule set, same evaluation logic.
//
// We can't hash internal rule lists, but we can drive both with a state and
// observe equivalent active-alert counts after evaluation.
func TestNewAlertManagerWithThresholds_DefaultsMatchNewAlertManager(t *testing.T) {
	state := &metrics.AlertState{
		Workers: []*metrics.WorkerMetrics{},
		GPUs:    []*metrics.GPUInfo{},
		Summary: metrics.FleetSummary{},
	}
	a := metrics.NewAlertManager()
	b := metrics.NewAlertManagerWithThresholds(metrics.DefaultAlertThresholds())
	a.Evaluate(state)
	b.Evaluate(state)
	ai, aw, ac := a.Counts()
	bi, bw, bc := b.Counts()
	if ai != bi || aw != bw || ac != bc {
		t.Fatalf("counts diverged: NewAlertManager=(%d,%d,%d) WithThresholds=(%d,%d,%d)", ai, aw, ac, bi, bw, bc)
	}
}

// Spec acceptance criterion (F4): with kv_critical_pct=80 and a worker at 85,
// the kv_critical rule fires after KVCriticalDelay; with default thresholds (90)
// the same worker does not fire.
func TestAlertManager_KVCritical_FiresAtCustomLowerThreshold(t *testing.T) {
	thr := metrics.DefaultAlertThresholds()
	thr.KVCriticalPct = 80
	thr.KVCriticalDelay = 0 // no grace; must fire on first eval

	am := metrics.NewAlertManagerWithThresholds(thr)
	state := &metrics.AlertState{
		Workers: []*metrics.WorkerMetrics{
			{Endpoint: "w1", Online: true, KVCacheUsagePct: 85},
		},
		GPUs:    []*metrics.GPUInfo{},
		Summary: metrics.FleetSummary{OnlineWorkers: 1},
	}
	am.Evaluate(state)
	_, _, crit := am.Counts()
	if crit < 1 {
		t.Fatalf("expected at least 1 critical alert with KVCriticalPct=80 and worker at 85%%, got %d", crit)
	}
}

func TestAlertManager_KVCritical_DoesNotFireBelowDefaultThreshold(t *testing.T) {
	// Worker at 85% with default critical = 90 → no critical alert.
	am := metrics.NewAlertManagerWithThresholds(metrics.DefaultAlertThresholds())
	state := &metrics.AlertState{
		Workers: []*metrics.WorkerMetrics{
			{Endpoint: "w1", Online: true, KVCacheUsagePct: 85},
		},
		GPUs:    []*metrics.GPUInfo{},
		Summary: metrics.FleetSummary{OnlineWorkers: 1},
	}
	am.Evaluate(state)
	_, _, crit := am.Counts()
	if crit != 0 {
		t.Fatalf("expected 0 critical alerts at default threshold (90) with worker at 85%%, got %d", crit)
	}
}

// Negative test for F4 invariant: zero delay = fire immediately.
func TestAlertThresholds_ZeroDelay_AcceptedByConstructor(t *testing.T) {
	thr := metrics.DefaultAlertThresholds()
	thr.KVCriticalDelay = 0
	thr.KVPressureDelay = 0
	thr.ITLSpikeDelay = 0
	thr.PrefillQueueDelay = 0
	thr.ScrapeFailureDelay = 0
	am := metrics.NewAlertManagerWithThresholds(thr)
	if am == nil {
		t.Fatalf("constructor returned nil for zero-delay thresholds (zero is valid per spec)")
	}
}
