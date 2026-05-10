package metrics

import (
	"math"
	"math/rand"
	"sync"
	"testing"
)

func TestWelfordState_ConstantInputZeroSigma(t *testing.T) {
	var w WelfordState
	for i := 0; i < 30; i++ {
		w.Update(100.0)
	}
	if w.Sigma(100.0) != 0 {
		t.Errorf("Sigma(mean) on constant series = %v, want 0", w.Sigma(100.0))
	}
	if w.Sigma(120.0) != 0 {
		t.Errorf("Sigma(other) on constant series should be 0 (stddev=0), got %v", w.Sigma(120.0))
	}
	if !math.IsNaN(0.0) && w.Stddev() != 0 {
		t.Errorf("Stddev on constant series = %v, want 0", w.Stddev())
	}
}

func TestWelfordState_NormalDistributionFixedSeed(t *testing.T) {
	var w WelfordState
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		v := 100.0 + rng.NormFloat64()*10.0
		w.Update(v)
	}
	if w.Mean < 95 || w.Mean > 105 {
		t.Errorf("Mean = %v, want in [95,105]", w.Mean)
	}
	if w.Stddev() < 8 || w.Stddev() > 12 {
		t.Errorf("Stddev = %v, want in [8,12]", w.Stddev())
	}
}

func TestWelfordState_VarianceCountLessThan2(t *testing.T) {
	var w WelfordState
	if w.Variance() != 0 {
		t.Errorf("empty Variance = %v, want 0", w.Variance())
	}
	w.Update(5.0)
	if w.Variance() != 0 {
		t.Errorf("Count=1 Variance = %v, want 0", w.Variance())
	}
}

func TestWelfordBank_MaxSigmaWarmup(t *testing.T) {
	b := newWelfordBank(60)
	w := &WorkerMetrics{
		KVCacheUsagePct: 50,
		TTFT:            LatencySnapshot{P99: 200},
		ITL:             LatencySnapshot{P99: 50},
		CacheHitRatePct: 30,
		RequestsWaiting: 1,
		GenTokPerSec:    100,
	}
	for i := 0; i < 29; i++ {
		b.Update(w)
	}
	if sig, name := b.MaxSigma(w); sig != 0 || name != "" {
		t.Errorf("warm-up incomplete should give (0,\"\"), got (%v, %q)", sig, name)
	}
	b.Update(w)
	// 30 samples now — but values are constant, stddev=0, so sigma is still 0.
	sig, name := b.MaxSigma(w)
	if sig != 0 {
		t.Errorf("constant series MaxSigma = %v, want 0; name=%q", sig, name)
	}
}

func TestWelfordBank_MaxSigmaSpike(t *testing.T) {
	b := newWelfordBank(60)
	rng := rand.New(rand.NewSource(7))
	baseline := &WorkerMetrics{}
	for i := 0; i < 60; i++ {
		baseline.KVCacheUsagePct = 50 + rng.NormFloat64()*2
		baseline.TTFT = LatencySnapshot{P99: 200 + rng.NormFloat64()*10}
		baseline.ITL = LatencySnapshot{P99: 50 + rng.NormFloat64()*2}
		baseline.CacheHitRatePct = 30 + rng.NormFloat64()*1
		baseline.RequestsWaiting = 1
		baseline.GenTokPerSec = 100 + rng.NormFloat64()*5
		b.Update(baseline)
	}
	// Now feed a spike: TTFT 3x baseline.
	spike := &WorkerMetrics{
		KVCacheUsagePct: 50,
		TTFT:            LatencySnapshot{P99: 600},
		ITL:             LatencySnapshot{P99: 50},
		CacheHitRatePct: 30,
		RequestsWaiting: 1,
		GenTokPerSec:    100,
	}
	sig, name := b.MaxSigma(spike)
	if sig < 2.0 {
		t.Errorf("spike sigma = %v, want > 2.0", sig)
	}
	if name != metricTTFTP99 {
		t.Errorf("max metric = %q, want %q", name, metricTTFTP99)
	}
}

func TestWelfordBank_AnomaliesSorted(t *testing.T) {
	b := newWelfordBank(60)
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 60; i++ {
		w := &WorkerMetrics{
			KVCacheUsagePct: 50 + rng.NormFloat64(),
			TTFT:            LatencySnapshot{P99: 200 + rng.NormFloat64()*5},
			ITL:             LatencySnapshot{P99: 50 + rng.NormFloat64()},
			CacheHitRatePct: 30 + rng.NormFloat64(),
			RequestsWaiting: 1,
			GenTokPerSec:    100 + rng.NormFloat64()*2,
		}
		b.Update(w)
	}
	// Two anomalies: TTFT very high, ITL moderately high.
	current := &WorkerMetrics{
		KVCacheUsagePct: 50,
		TTFT:            LatencySnapshot{P99: 600},
		ITL:             LatencySnapshot{P99: 70},
		CacheHitRatePct: 30,
		RequestsWaiting: 1,
		GenTokPerSec:    100,
	}
	anoms := b.Anomalies(current, 2.0)
	if len(anoms) < 1 {
		t.Fatalf("expected at least 1 anomaly, got %d", len(anoms))
	}
	if anoms[0].MetricName != metricTTFTP99 {
		t.Errorf("first anomaly = %q, want %q (largest sigma should sort first)", anoms[0].MetricName, metricTTFTP99)
	}
}

func TestWelfordBank_AnomaliesWindowEviction(t *testing.T) {
	b := newWelfordBank(10)
	// Fill window with v=100.
	w := &WorkerMetrics{TTFT: LatencySnapshot{P99: 100}}
	for i := 0; i < 10; i++ {
		b.Update(w)
	}
	if b.TTFTP99.Count != 10 {
		t.Errorf("Count after fill = %d, want 10", b.TTFTP99.Count)
	}
	// Push 5 more — buffer is full, oldest should evict.
	for i := 0; i < 5; i++ {
		b.Update(w)
	}
	if b.TTFTP99.Count != 10 {
		t.Errorf("Count after eviction-only updates = %d, want 10 (window cap)", b.TTFTP99.Count)
	}
	// Mean should still be 100 (constant series).
	if b.TTFTP99.Mean != 100 {
		t.Errorf("Mean = %v, want 100", b.TTFTP99.Mean)
	}
}

func TestAnomalyStore_EvictsAbsentEndpoint(t *testing.T) {
	s := NewAnomalyStore(5)
	w := &WorkerMetrics{Endpoint: "http://w1", Online: true}
	for i := 0; i < 30; i++ {
		s.Update([]*WorkerMetrics{w})
	}
	// Now w disappears.
	for i := 0; i < 11; i++ { // 11 > 2 * window
		s.Update(nil)
	}
	if sig, name := s.MaxSigma("http://w1", w); sig != 0 || name != "" {
		t.Errorf("evicted bank should yield (0,\"\"), got (%v, %q)", sig, name)
	}
}

func TestAnomalyStore_OfflineDoesNotPolluteBaseline(t *testing.T) {
	s := NewAnomalyStore(60)
	w := &WorkerMetrics{
		Endpoint:        "http://w1",
		Online:          true,
		TTFT:            LatencySnapshot{P99: 200},
		KVCacheUsagePct: 50,
		ITL:             LatencySnapshot{P99: 50},
		CacheHitRatePct: 30,
		RequestsWaiting: 1,
		GenTokPerSec:    100,
	}
	for i := 0; i < 30; i++ {
		s.Update([]*WorkerMetrics{w})
	}
	wOff := *w
	wOff.Online = false
	wOff.TTFT = LatencySnapshot{P99: 99999}
	for i := 0; i < 30; i++ {
		s.Update([]*WorkerMetrics{&wOff})
	}
	// Baseline should not have absorbed the offline garbage.
	sig, _ := s.MaxSigma("http://w1", w)
	if math.Abs(sig) > 0.5 {
		t.Errorf("baseline poisoned by offline samples — sigma at baseline value = %v", sig)
	}
}

func TestAnomalyStore_NoBankReturnsZero(t *testing.T) {
	s := NewAnomalyStore(10)
	w := &WorkerMetrics{Endpoint: "http://nope"}
	if sig, name := s.MaxSigma("http://missing", w); sig != 0 || name != "" {
		t.Errorf("no bank should give (0,\"\"), got (%v,%q)", sig, name)
	}
	if got := s.Anomalies("http://missing", w); got != nil {
		t.Errorf("no bank should give nil anomalies, got %v", got)
	}
}

func TestAnomalyStore_ConcurrentUpdate(t *testing.T) {
	s := NewAnomalyStore(50)
	w := &WorkerMetrics{Endpoint: "http://w1", Online: true}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Update([]*WorkerMetrics{w})
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = s.MaxSigma("http://w1", w)
			}
		}()
	}
	wg.Wait()
}
