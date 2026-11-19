package collector

import (
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// TestRateWindowUsesCounterTimestamp locks in issue #15 cause 3: dt comes from
// the snapshot's own timestamp, never prev.LastSeen, which lags one poll behind.
func TestRateWindowUsesCounterTimestamp(t *testing.T) {
	now := time.Now()
	first := counterState{}
	if _, ok := first.rateWindow(now); ok {
		t.Error("first poll must have no usable rate window")
	}
	aged := counterState{ts: now.Add(-2 * time.Second)}
	dt, ok := aged.rateWindow(now)
	if !ok || dt < 1.9 || dt > 2.1 {
		t.Errorf("rateWindow = (%f, %v), want ~2, true", dt, ok)
	}
	sameInstant := counterState{ts: now}
	if _, ok := sameInstant.rateWindow(now); ok {
		t.Error("zero-length window must be reported unusable")
	}
}

// seriesDelta is the issue #15 follow-up (P3): summing series first (Σcur−Σprev)
// lets one restarted series cancel the growth of all its siblings and flatten the
// whole rate window. Per-series deltas clamped at 0 lose only the restarted
// series.
func TestSeriesDeltaClampsPerSeries(t *testing.T) {
	prev := map[string]float64{"a,": 1000, "b,": 1000}
	cur := map[string]float64{"a,": 0, "b,": 1500} // engine a restarted, b grew 500

	if got := seriesDelta(prev, cur, 2000, 1500); got != 500 {
		t.Errorf("seriesDelta = %f, want 500 (restarted series clamps, healthy one counts)", got)
	}
	// The summed form this replaces would have reported max(0, 1500-2000) = 0.
	if got := seriesRate(prev, cur, 2000, 1500, 2); got != 250 {
		t.Errorf("seriesRate = %f, want 250 (500 tokens over a 2s window)", got)
	}
}

// A series that only appears in the current poll has no baseline yet, so it must
// contribute nothing rather than a lifetime-over-dt spike.
func TestSeriesDeltaNewSeriesHasNoRate(t *testing.T) {
	cur := map[string]float64{"new,": 9e6}
	if got := seriesDelta(nil, cur, 0, 9e6); got != 0 {
		t.Errorf("seriesDelta = %f, want 0 for a counter first seen this poll", got)
	}
	if got := seriesDelta(map[string]float64{}, cur, 0, 9e6); got != 0 {
		t.Errorf("seriesDelta = %f, want 0 for a counter first seen this poll", got)
	}
}

// Single-series readers (TGI, Triton) have no breakdown at all and keep using the
// clamped delta of the totals.
func TestSeriesDeltaFallsBackToTotals(t *testing.T) {
	if got := seriesDelta(nil, nil, 1000, 1600); got != 600 {
		t.Errorf("seriesDelta = %f, want 600 (total delta)", got)
	}
	if got := seriesDelta(nil, nil, 5000, 100); got != 0 {
		t.Errorf("seriesDelta = %f, want 0 after a counter reset", got)
	}
	if got := seriesRate(nil, nil, 1000, 2000, 0); got != 0 {
		t.Errorf("seriesRate = %f, want 0 without a window", got)
	}
}

// counterParse is the shared signature of the per-backend poll functions, so one
// helper can run a poll for any collector.
type counterParse func(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState

// prevSnapshot runs one poll over an older exposition and returns the counter
// snapshot the collector would have stored, with its timestamp rewound to `age`
// before now. Rate tests build baselines this way rather than writing scalar
// totals by hand: deltas are keyed per series, and the keys are canonical label
// renderings owned by the metrics package.
func prevSnapshot(t *testing.T, parse counterParse, older string, age time.Duration) counterState {
	t.Helper()
	cs := parse(&metrics.WorkerMetrics{Endpoint: "http://prev", Online: true}, nil, counterState{}, metrics.ParseText(older))
	if cs.ts.IsZero() {
		t.Fatal("poll did not stamp its counter snapshot")
	}
	cs.ts = time.Now().Add(-age)
	return cs
}
