// Black-box contract tests for V3 (Anomaly Halo).
// Spec: docs/feature-spec-v2.md §3 V3, §7 glossary.
//
// Pinned contracts:
//   - WelfordState.Update accumulates Count, Mean, M2 via Welford's online algorithm
//   - Variance/Stddev return 0 when Count < 2
//   - Sigma returns 0 when Stddev() == 0
//   - WelfordBank tracks 6 metrics; MaxSigma returns ("", 0) when any has < 30 samples
//   - AnomalyStore implements AnomalyGetter; warm-up gating; eviction after 2× window
//   - Anomalies returns at most 3 entries

package metrics_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// ─── WelfordState ────────────────────────────────────────────────────────────

func TestWelfordState_ZeroValueHasZeroCount(t *testing.T) {
	var w metrics.WelfordState
	if w.Count != 0 {
		t.Errorf("zero-value WelfordState should have Count=0, got %d", w.Count)
	}
}

func TestWelfordState_UpdateIncrementsCount(t *testing.T) {
	var w metrics.WelfordState
	w.Update(1.0)
	w.Update(2.0)
	w.Update(3.0)
	if w.Count != 3 {
		t.Errorf("Count should be 3 after 3 updates, got %d", w.Count)
	}
}

func TestWelfordState_VarianceZeroWhenCountLT2(t *testing.T) {
	var w metrics.WelfordState
	if v := w.Variance(); v != 0 {
		t.Errorf("Variance() with Count=0 should be 0, got %v", v)
	}
	w.Update(5.0)
	if v := w.Variance(); v != 0 {
		t.Errorf("Variance() with Count=1 should be 0, got %v", v)
	}
}

func TestWelfordState_StddevZeroWhenCountLT2(t *testing.T) {
	var w metrics.WelfordState
	w.Update(5.0)
	if s := w.Stddev(); s != 0 {
		t.Errorf("Stddev() with Count=1 should be 0, got %v", s)
	}
}

func TestWelfordState_ConstantInput_StddevIsZero(t *testing.T) {
	var w metrics.WelfordState
	for range 30 {
		w.Update(100.0)
	}
	if s := w.Stddev(); s != 0 {
		t.Errorf("Stddev() with all-equal inputs should be 0, got %v", s)
	}
}

func TestWelfordState_ConstantInput_SigmaIsZero(t *testing.T) {
	var w metrics.WelfordState
	for range 30 {
		w.Update(100.0)
	}
	if s := w.Sigma(100.0); s != 0 {
		t.Errorf("Sigma(100) on constant 100s should be 0, got %v", s)
	}
	// Spec: stddev=0 ⇒ Sigma returns 0, not infinity.
	if s := w.Sigma(120.0); s != 0 {
		t.Errorf("Sigma(120) on constant 100s should be 0 (not Inf), got %v", s)
	}
}

func TestWelfordState_NormalDist_MeanCloseToTrue(t *testing.T) {
	var w metrics.WelfordState
	rng := rand.New(rand.NewSource(42))
	for range 1000 {
		v := rng.NormFloat64()*10.0 + 100.0
		w.Update(v)
	}
	if w.Mean < 99.0 || w.Mean > 101.0 {
		t.Errorf("Mean of N(100,10) over 1000 samples should be in [99,101], got %v", w.Mean)
	}
}

func TestWelfordState_NormalDist_StddevCloseToTrue(t *testing.T) {
	var w metrics.WelfordState
	rng := rand.New(rand.NewSource(42))
	for range 1000 {
		v := rng.NormFloat64()*10.0 + 100.0
		w.Update(v)
	}
	s := w.Stddev()
	if s < 9.5 || s > 10.5 {
		t.Errorf("Stddev of N(100,10) over 1000 samples should be in [9.5,10.5], got %v", s)
	}
}

func TestWelfordState_Sigma_PositiveAboveMean(t *testing.T) {
	var w metrics.WelfordState
	rng := rand.New(rand.NewSource(42))
	for range 1000 {
		v := rng.NormFloat64()*10.0 + 100.0
		w.Update(v)
	}
	// 130 is ~3σ above mean=100, σ=10.
	if s := w.Sigma(130.0); s <= 0 {
		t.Errorf("Sigma above mean should be positive, got %v", s)
	}
}

func TestWelfordState_Sigma_NegativeBelowMean(t *testing.T) {
	var w metrics.WelfordState
	rng := rand.New(rand.NewSource(42))
	for range 1000 {
		v := rng.NormFloat64()*10.0 + 100.0
		w.Update(v)
	}
	if s := w.Sigma(70.0); s >= 0 {
		t.Errorf("Sigma below mean should be negative, got %v", s)
	}
}

func TestWelfordState_Sigma_NotNaNOrInfOnConstant(t *testing.T) {
	var w metrics.WelfordState
	for range 30 {
		w.Update(50.0)
	}
	s := w.Sigma(75.0)
	if math.IsNaN(s) || math.IsInf(s, 0) {
		t.Errorf("Sigma on constant input must be finite, got %v", s)
	}
}

// ─── WelfordBank ─────────────────────────────────────────────────────────────

func TestWelfordBank_MaxSigma_BelowWarmup_ReturnsZero(t *testing.T) {
	var b metrics.WelfordBank
	w := &metrics.WorkerMetrics{Endpoint: "ep"}
	w.TTFT.P99 = 200.0
	for range 5 {
		b.Update(w)
	}
	sigma, name := b.MaxSigma(w)
	if sigma != 0 || name != "" {
		t.Errorf("MaxSigma below 30 samples should return (0, \"\"), got (%v, %q)", sigma, name)
	}
}

func TestWelfordBank_Anomalies_BelowWarmup_ReturnsEmpty(t *testing.T) {
	var b metrics.WelfordBank
	w := &metrics.WorkerMetrics{Endpoint: "ep"}
	w.TTFT.P99 = 200.0
	for range 10 {
		b.Update(w)
	}
	got := b.Anomalies(w, 2.0)
	if len(got) != 0 {
		t.Errorf("Anomalies below warmup should be empty, got len=%d", len(got))
	}
}

func TestWelfordBank_Anomalies_AtMost3(t *testing.T) {
	var b metrics.WelfordBank
	rng := rand.New(rand.NewSource(7))
	// Build baselines for all 6 metrics with low noise.
	for range 60 {
		w := &metrics.WorkerMetrics{Endpoint: "ep"}
		w.KVCacheUsagePct = 50.0 + rng.NormFloat64()*0.5
		w.TTFT.P99 = 100.0 + rng.NormFloat64()*1.0
		w.ITL.P99 = 30.0 + rng.NormFloat64()*0.3
		w.CacheHitRatePct = 80.0 + rng.NormFloat64()*0.5
		w.RequestsWaiting = 2 + rng.Intn(2)
		w.GenTokPerSec = 200.0 + rng.NormFloat64()*1.0
		b.Update(w)
	}
	// Now feed an extreme observation across all 6 metrics.
	wAnom := &metrics.WorkerMetrics{
		Endpoint:        "ep",
		KVCacheUsagePct: 1000.0,
		CacheHitRatePct: 1000.0,
		RequestsWaiting: 1000,
		GenTokPerSec:    1000.0,
	}
	wAnom.TTFT.P99 = 10000.0
	wAnom.ITL.P99 = 10000.0
	got := b.Anomalies(wAnom, 2.0)
	if len(got) > 3 {
		t.Errorf("Anomalies should return at most 3, got %d", len(got))
	}
}

// ─── AnomalyStore (implements AnomalyGetter) ─────────────────────────────────

func TestAnomalyStore_ImplementsAnomalyGetter(t *testing.T) {
	s := metrics.NewAnomalyStore(150)
	var _ metrics.AnomalyGetter = s
}

func TestAnomalyStore_MaxSigma_UnknownEndpoint_ReturnsZero(t *testing.T) {
	s := metrics.NewAnomalyStore(150)
	w := &metrics.WorkerMetrics{Endpoint: "missing"}
	sigma, name := s.MaxSigma("missing", w)
	if sigma != 0 || name != "" {
		t.Errorf("MaxSigma for unknown endpoint should be (0, \"\"), got (%v, %q)", sigma, name)
	}
}

func TestAnomalyStore_Update_DoesNotPanicOnEmptySlice(t *testing.T) {
	s := metrics.NewAnomalyStore(150)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Update on empty slice panicked: %v", r)
		}
	}()
	s.Update([]*metrics.WorkerMetrics{})
}

func TestAnomalyStore_BelowWarmup_MaxSigmaIsZero(t *testing.T) {
	s := metrics.NewAnomalyStore(150)
	w := &metrics.WorkerMetrics{Endpoint: "ep"}
	w.TTFT.P99 = 100.0
	// Feed only 10 ticks — below 30-sample warm-up.
	for range 10 {
		s.Update([]*metrics.WorkerMetrics{w})
	}
	sigma, name := s.MaxSigma("ep", w)
	if sigma != 0 || name != "" {
		t.Errorf("MaxSigma below warmup should return (0, \"\"), got (%v, %q)", sigma, name)
	}
}

func TestAnomalyStore_Anomalies_BelowWarmup_Empty(t *testing.T) {
	s := metrics.NewAnomalyStore(150)
	w := &metrics.WorkerMetrics{Endpoint: "ep"}
	w.TTFT.P99 = 100.0
	for range 10 {
		s.Update([]*metrics.WorkerMetrics{w})
	}
	got := s.Anomalies("ep", w)
	if len(got) != 0 {
		t.Errorf("Anomalies below warmup should be empty, got %d", len(got))
	}
}

func TestNewAnomalyStore_ReturnsNonNil(t *testing.T) {
	s := metrics.NewAnomalyStore(150)
	if s == nil {
		t.Errorf("NewAnomalyStore returned nil")
	}
}
