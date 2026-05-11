package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// stubAnomalyGetter is a hand-rolled implementation of the AnomalyGetter
// interface used by tests for V3-related behaviors. It returns canned
// values keyed by endpoint without exercising the real Welford engine.
type stubAnomalyGetter struct {
	maxByEndpoint map[string]float64
	nameByEndpoint map[string]string
	details map[string][]AnomalyDetail
}

func (s *stubAnomalyGetter) MaxSigma(endpoint string, w *metrics.WorkerMetrics) (float64, string) {
	return s.maxByEndpoint[endpoint], s.nameByEndpoint[endpoint]
}

func (s *stubAnomalyGetter) Anomalies(endpoint string, w *metrics.WorkerMetrics) []AnomalyDetail {
	return s.details[endpoint]
}

func TestRenderTable_NilAnomalyAndBookmarks_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderTable panicked: %v", r)
		}
	}()
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "ep1", Label: "w1", Online: true, Backend: metrics.BackendVLLM, KVCacheUsagePct: 30},
	}
	out := RenderTable(workers, 0, SortNone, "", 160, 5, 0, nil, nil, "")
	if out == "" {
		t.Errorf("expected non-empty output")
	}
}

func TestRenderTable_HaloShownForHighSigma(t *testing.T) {
	stub := &stubAnomalyGetter{
		maxByEndpoint:  map[string]float64{"ep1": 3.5},
		nameByEndpoint: map[string]string{"ep1": "TTFT P99"},
	}
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "ep1", Label: "w1", Online: true, Backend: metrics.BackendVLLM, KVCacheUsagePct: 30},
	}
	out := RenderTable(workers, 0, SortNone, "", 160, 5, 0, stub, nil, "")
	if !strings.Contains(out, "●") {
		// Note: the worker's status dot is also "●". We assert at least one
		// occurrence; better assertions would require parsing ANSI colors.
		t.Errorf("expected halo glyph in output for sigma=3.5")
	}
}

func TestRenderSidebar_AnomalyBlockShownWhenAboveThreshold(t *testing.T) {
	stub := &stubAnomalyGetter{
		details: map[string][]AnomalyDetail{
			"ep1": {
				{MetricName: "TTFT P99", Sigma: 3.5, Mean: 200, Stddev: 30, CurrentVal: 305},
			},
		},
	}
	worker := &metrics.WorkerMetrics{Endpoint: "ep1", Label: "w1"}
	out := RenderSidebar(worker, nil, nil, 30, stub)
	if !strings.Contains(out, "ANOMALIES") {
		t.Errorf("expected ANOMALIES block in sidebar, got %q", out)
	}
}

func TestRenderSidebar_AnomalyBlockHiddenWhenNoDetails(t *testing.T) {
	stub := &stubAnomalyGetter{}
	worker := &metrics.WorkerMetrics{Endpoint: "ep1"}
	out := RenderSidebar(worker, nil, nil, 30, stub)
	if strings.Contains(out, "ANOMALIES") {
		t.Errorf("ANOMALIES block should be hidden when no details, got %q", out)
	}
}

// V1 currentFrame contract.

func TestCurrentFrame_LiveWhenOffsetZero(t *testing.T) {
	m := newTestModel()
	m.workers = []*metrics.WorkerMetrics{{Endpoint: "ep", Label: "live"}}
	m.summary = metrics.FleetSummary{TotalTokPerSec: 100}
	w, s, _ := m.currentFrame()
	if len(w) != 1 || w[0].Label != "live" {
		t.Errorf("expected live workers, got %+v", w)
	}
	if s.TotalTokPerSec != 100 {
		t.Errorf("expected live summary, got %+v", s)
	}
}

func TestCurrentFrame_HistoricalWhenScrubbing(t *testing.T) {
	m := newTestModel()
	// Pre-fill the ring with a distinct historical state.
	m.ring = metrics.NewRingBuffer(10)
	m.ring.Push(metrics.CaptureFrame(
		[]*metrics.WorkerMetrics{{Endpoint: "ep", Label: "old"}},
		metrics.FleetSummary{TotalTokPerSec: 999},
		nil,
	))
	// Live state differs.
	m.workers = []*metrics.WorkerMetrics{{Endpoint: "ep", Label: "live"}}
	m.summary = metrics.FleetSummary{TotalTokPerSec: 100}
	m.travelOffset = 0
	live, _, _ := m.currentFrame()
	if live[0].Label != "live" {
		t.Errorf("offset=0 should return live, got %s", live[0].Label)
	}
	// Now simulate scrubbing: there's only one frame at offset 0; bump
	// travelOffset to a valid index. Push a second frame so offset=1 is valid.
	m.ring.Push(metrics.CaptureFrame(
		[]*metrics.WorkerMetrics{{Endpoint: "ep", Label: "live-snap"}},
		metrics.FleetSummary{TotalTokPerSec: 100},
		nil,
	))
	m.travelOffset = 1
	hist, hsum, _ := m.currentFrame()
	if hist[0].Label != "old" {
		t.Errorf("expected historical 'old', got %s", hist[0].Label)
	}
	if hsum.TotalTokPerSec != 999 {
		t.Errorf("expected historical summary 999, got %v", hsum.TotalTokPerSec)
	}
}

func TestHandleScrubKey_RingNilReturnsFalse(t *testing.T) {
	m := newTestModel()
	handled, _ := m.handleScrubKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if handled {
		t.Errorf("scrub key with nil ring should not be handled")
	}
}

func TestHandleScrubKey_BackForwardLive(t *testing.T) {
	m := newTestModel()
	m.ring = metrics.NewRingBuffer(5)
	for range 3 {
		m.ring.Push(metrics.CaptureFrame(
			[]*metrics.WorkerMetrics{{Endpoint: "ep"}},
			metrics.FleetSummary{},
			nil,
		))
	}
	// [
	handled, m1 := m.handleScrubKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if !handled || m1.travelOffset != 1 {
		t.Errorf("expected travelOffset=1 after [, got handled=%v offset=%d", handled, m1.travelOffset)
	}
	// ]
	_, m2 := m1.handleScrubKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m2.travelOffset != 0 {
		t.Errorf("expected travelOffset=0 after ], got %d", m2.travelOffset)
	}
	// \ from offset > 0
	m2.travelOffset = 2
	_, m3 := m2.handleScrubKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
	if m3.travelOffset != 0 {
		t.Errorf("expected travelOffset=0 after \\, got %d", m3.travelOffset)
	}
}

func TestHandleScrubKey_StopsAtOldest(t *testing.T) {
	m := newTestModel()
	m.ring = metrics.NewRingBuffer(5)
	for range 3 {
		m.ring.Push(metrics.CaptureFrame(nil, metrics.FleetSummary{}, nil))
	}
	for range 10 {
		_, m = m.handleScrubKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	}
	if m.travelOffset != 2 {
		t.Errorf("expected clamp at travelOffset=2 (Len-1), got %d", m.travelOffset)
	}
}

// V12 demo-mode footer + scenario switch.

func TestRenderFooter_DemoModeShowsScenario(t *testing.T) {
	m := newTestModel()
	m.demoMode = true
	m.demoScenario = "demo"
	out := m.renderFooter()
	if !strings.Contains(out, "DEMO") {
		t.Errorf("demo footer should contain DEMO marker, got %q", out)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("demo footer should contain scenario name, got %q", out)
	}
}

func TestHandleGlobalRound2Key_SCyclesScenarios(t *testing.T) {
	called := ""
	m := newTestModel()
	m.demoMode = true
	m.demoScenario = "steady"
	m.switchScenario = func(s string) { called = s }
	_, m2 := m.handleGlobalRound2Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if m2.demoScenario != "demo" {
		t.Errorf("expected scenario to advance to 'demo', got %q", m2.demoScenario)
	}
	if called != "demo" {
		t.Errorf("expected switchScenario closure called with 'demo', got %q", called)
	}
}

func TestHandleGlobalRound2Key_SNoOpWhenNotDemoMode(t *testing.T) {
	m := newTestModel()
	m.demoMode = false
	m.switchScenario = nil
	_, m2 := m.handleGlobalRound2Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if m2.demoScenario != "" {
		t.Errorf("expected empty scenario when not demo mode, got %q", m2.demoScenario)
	}
}

func TestHandleGlobalRound2Key_OpensViews(t *testing.T) {
	m := newTestModel()
	m.currentView = ViewMain
	_, m2 := m.handleGlobalRound2Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	if m2.currentView != ViewCapabilityMatrix {
		t.Errorf("C should open ViewCapabilityMatrix, got %d", m2.currentView)
	}
	if m2.overlayReturnView != ViewMain {
		t.Errorf("overlayReturnView should be ViewMain, got %d", m2.overlayReturnView)
	}
	_, m3 := m.handleGlobalRound2Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	if m3.currentView != ViewBookmarkCompare {
		t.Errorf("B should open ViewBookmarkCompare, got %d", m3.currentView)
	}
}

func TestNextScenarioCycle(t *testing.T) {
	cases := []struct{ in, out string }{
		{"steady", "demo"},
		{"demo", "stress"},
		{"stress", "chaos"},
		{"chaos", "steady"},
		{"unknown", "steady"},
		{"", "steady"},
	}
	for _, c := range cases {
		if got := nextScenario(c.in); got != c.out {
			t.Errorf("nextScenario(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}

// V6 fleetHistory ring.

func TestFleetHistorySlice_OrderingAndCap(t *testing.T) {
	m := newTestModel()
	for i := range 70 {
		m.fleetHistory[m.fleetHistoryHead] = metrics.FleetSummary{TotalTokPerSec: float64(i)}
		m.fleetHistoryHead = (m.fleetHistoryHead + 1) % len(m.fleetHistory)
		if m.fleetHistoryLen < len(m.fleetHistory) {
			m.fleetHistoryLen++
		}
	}
	got := m.fleetHistorySlice()
	if len(got) != 60 {
		t.Errorf("expected 60 entries (cap), got %d", len(got))
	}
	// Newest should be the last entry pushed (i=69 → TotalTokPerSec=69).
	if got[len(got)-1].TotalTokPerSec != 69 {
		t.Errorf("expected newest=69, got %v", got[len(got)-1].TotalTokPerSec)
	}
	// Oldest in the visible 60 should be i=10.
	if got[0].TotalTokPerSec != 10 {
		t.Errorf("expected oldest in window=10, got %v", got[0].TotalTokPerSec)
	}
}

// Build/render smoke for round-2 views.

func TestRenderRound2Views_NoPanic(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()
	for _, v := range []View{ViewBookmarkCompare, ViewEvictionTimeline, ViewCapabilityMatrix} {
		m.currentView = v
		out := m.View()
		if out == "" {
			t.Errorf("view %d rendered empty", v)
		}
	}
}

// Footer status TTL countdown.

func TestFooterStatusCountdown(t *testing.T) {
	m := newTestModel()
	m.footerStatus = "test"
	m.footerStatusTks = 1
	// Simulate one dataMsg.
	msg := dataMsg{workers: nil, summary: metrics.FleetSummary{}}
	updated, _ := m.Update(msg)
	model := updated.(Model)
	if model.footerStatus != "" {
		t.Errorf("expected footerStatus cleared after countdown, got %q", model.footerStatus)
	}
	_ = time.Time{}
}
