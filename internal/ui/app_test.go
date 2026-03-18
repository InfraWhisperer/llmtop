package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/InfraWhisperer/llmtop/internal/collector"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// stubDCGMCollector returns a *collector.DCGMCollector that won't hit the network.
// Used only to make dcgmCollector non-nil so the 'g' key handler activates.
func stubDCGMCollector() *collector.DCGMCollector {
	return collector.NewDCGMCollector("http://stub:9400", 1*time.Hour)
}

func testWorkers() []*metrics.WorkerMetrics {
	return []*metrics.WorkerMetrics{
		{
			Endpoint:        "http://localhost:8000",
			Label:           "worker-1",
			Backend:         metrics.BackendVLLM,
			ModelName:       "llama-70b",
			Online:          true,
			KVCacheUsagePct: 45,
			RequestsRunning: 3,
			RequestsWaiting: 1,
			TTFT_P99:        120,
			GenTokPerSec:    500,
		},
		{
			Endpoint:        "http://localhost:8001",
			Label:           "worker-2",
			Backend:         metrics.BackendVLLM,
			ModelName:       "llama-70b",
			Online:          true,
			KVCacheUsagePct: 72,
			RequestsRunning: 5,
			RequestsWaiting: 0,
			TTFT_P99:        85,
			GenTokPerSec:    620,
		},
		{
			Endpoint:  "http://localhost:8002",
			Label:     "worker-3",
			Backend:   metrics.BackendSGLang,
			ModelName: "mistral-7b",
			Online:    false,
		},
	}
}

func testGPUs() []*metrics.GPUInfo {
	return []*metrics.GPUInfo{
		{
			Index:      0,
			Name:       "NVIDIA H100 80GB HBM3",
			Hostname:   "node-1",
			UtilPct:    85,
			MemUsedMB:  65000,
			MemTotalMB: 81920,
			TempC:      72,
			PowerW:     650,
			Pod:        "vllm-worker-0",
			Namespace:  "inference",
		},
	}
}

// newTestModel builds a Model suitable for testing without real collectors.
// The concrete collector fields are nil; tests must avoid triggering tickMsg,
// refreshMsg, or the 'r' key handler which dereference them.
func newTestModel() Model {
	return Model{
		version:     "0.1.0",
		intervalSec: 2,
		width:       120,
		height:      40,
	}
}

func TestNewModel(t *testing.T) {
	m := NewModel(nil, nil, "0.1.0", 2, "test-ctx")

	if m.version != "0.1.0" {
		t.Errorf("expected version=0.1.0, got %s", m.version)
	}
	if m.intervalSec != 2 {
		t.Errorf("expected intervalSec=2, got %d", m.intervalSec)
	}
	if m.k8sContext != "test-ctx" {
		t.Errorf("expected k8sContext=test-ctx, got %s", m.k8sContext)
	}
	if m.currentView != ViewMain {
		t.Errorf("expected currentView=ViewMain, got %d", m.currentView)
	}
	if m.selectedIdx != 0 {
		t.Errorf("expected selectedIdx=0, got %d", m.selectedIdx)
	}
}

func TestDataMsgUpdatesState(t *testing.T) {
	m := newTestModel()

	workers := testWorkers()
	summary := metrics.ComputeFleetSummary(workers)
	msg := dataMsg{workers: workers, summary: summary}

	updated, _ := m.Update(msg)
	model := updated.(Model)

	if len(model.workers) != 3 {
		t.Errorf("expected 3 workers, got %d", len(model.workers))
	}
	if model.summary.TotalWorkers != 3 {
		t.Errorf("expected TotalWorkers=3, got %d", model.summary.TotalWorkers)
	}
	if model.summary.OnlineWorkers != 2 {
		t.Errorf("expected OnlineWorkers=2, got %d", model.summary.OnlineWorkers)
	}
	if len(model.modelGroups) == 0 {
		t.Error("expected modelGroups to be populated")
	}
	if model.lastRefresh.IsZero() {
		t.Error("expected lastRefresh to be set")
	}
}

func TestDataMsgSortsOnlineFirst(t *testing.T) {
	m := newTestModel()

	workers := testWorkers()
	summary := metrics.ComputeFleetSummary(workers)
	msg := dataMsg{workers: workers, summary: summary}

	updated, _ := m.Update(msg)
	model := updated.(Model)

	// Offline worker (worker-3) should be last after stable sort.
	last := model.workers[len(model.workers)-1]
	if last.Online {
		t.Errorf("expected last worker to be offline, got online worker %s", last.Label)
	}
}

func TestKeyNavigation(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()

	// j → down
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model := updated.(Model)
	if model.selectedIdx != 1 {
		t.Errorf("expected selectedIdx=1 after j, got %d", model.selectedIdx)
	}

	// j → down again
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(Model)
	if model.selectedIdx != 2 {
		t.Errorf("expected selectedIdx=2 after second j, got %d", model.selectedIdx)
	}

	// j at bottom should clamp
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(Model)
	if model.selectedIdx != 2 {
		t.Errorf("expected selectedIdx=2 at bottom, got %d", model.selectedIdx)
	}

	// k → up
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if model.selectedIdx != 1 {
		t.Errorf("expected selectedIdx=1 after k, got %d", model.selectedIdx)
	}

	// k → up to 0
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if model.selectedIdx != 0 {
		t.Errorf("expected selectedIdx=0 after second k, got %d", model.selectedIdx)
	}

	// k at top should clamp
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if model.selectedIdx != 0 {
		t.Errorf("expected selectedIdx=0 at top, got %d", model.selectedIdx)
	}
}

func TestViewSwitching(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()

	// d → detail (requires workers)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model := updated.(Model)
	if model.currentView != ViewDetail {
		t.Errorf("expected ViewDetail after d, got %d", model.currentView)
	}

	// any key returns from detail to main
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(Model)
	if model.currentView != ViewMain {
		t.Errorf("expected ViewMain after escape from detail, got %d", model.currentView)
	}

	// ? → help
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(Model)
	if model.currentView != ViewHelp {
		t.Errorf("expected ViewHelp after ?, got %d", model.currentView)
	}

	// any key returns from help to main
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(Model)
	if model.currentView != ViewMain {
		t.Errorf("expected ViewMain after escape from help, got %d", model.currentView)
	}

	// m → model group
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(Model)
	if model.currentView != ViewModelGroup {
		t.Errorf("expected ViewModelGroup after m, got %d", model.currentView)
	}

	// m from model group returns to main
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(Model)
	if model.currentView != ViewMain {
		t.Errorf("expected ViewMain after m from model group, got %d", model.currentView)
	}
}

func TestViewSwitchingDetailRequiresWorkers(t *testing.T) {
	m := newTestModel()
	// No workers — d should not switch view.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model := updated.(Model)
	if model.currentView != ViewMain {
		t.Errorf("expected ViewMain when d pressed with no workers, got %d", model.currentView)
	}
}

func TestSortCycling(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()

	if m.sortCol != SortNone {
		t.Fatalf("expected initial sortCol=SortNone, got %d", m.sortCol)
	}

	// Each s press should advance through sortCycle.
	expected := []SortColumn{SortKVCache, SortQueue, SortTTFT, SortHitRate, SortTokPerSec, SortNone}
	model := m
	for i, want := range expected {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
		model = updated.(Model)
		if model.sortCol != want {
			t.Errorf("sort cycle step %d: expected %d, got %d", i, want, model.sortCol)
		}
	}
}

func TestFilterCycling(t *testing.T) {
	m := newTestModel()

	if m.filterIdx != 0 {
		t.Fatalf("expected initial filterIdx=0, got %d", m.filterIdx)
	}

	expectedFilters := []metrics.Backend{
		metrics.BackendVLLM,
		metrics.BackendSGLang,
		metrics.BackendLMCache,
		metrics.BackendNIM,
		metrics.BackendTGI,
		metrics.BackendTRTLLM,
		metrics.BackendTriton,
		metrics.BackendLlamaCpp,
		metrics.BackendUnknown, // wraps back to "all"
	}

	model := m
	for i, want := range expectedFilters {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		model = updated.(Model)
		got := filterCycle[model.filterIdx]
		if got != want {
			t.Errorf("filter cycle step %d: expected %s, got %s", i, want, got)
		}
	}
}

func TestGPUViewWithSource(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()
	// dcgmCollector must be non-nil for g key to switch view.
	m.dcgmCollector = stubDCGMCollector()
	m.gpus = testGPUs()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model := updated.(Model)
	if model.currentView != ViewGPU {
		t.Errorf("expected ViewGPU after g with dcgmCollector, got %d", model.currentView)
	}

	// g from GPU view returns to main
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(Model)
	if model.currentView != ViewMain {
		t.Errorf("expected ViewMain after g from GPU view, got %d", model.currentView)
	}
}

func TestGPUViewWithoutSource(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()
	// dcgmCollector is nil — g should be a no-op.

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model := updated.(Model)
	if model.currentView != ViewMain {
		t.Errorf("expected ViewMain when g pressed with nil dcgmCollector, got %d", model.currentView)
	}
}

func TestWindowSizeMsg(t *testing.T) {
	m := newTestModel()
	m.width = 0
	m.height = 0

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	model := updated.(Model)
	if model.width != 200 {
		t.Errorf("expected width=200, got %d", model.width)
	}
	if model.height != 50 {
		t.Errorf("expected height=50, got %d", model.height)
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()
	m.summary = metrics.ComputeFleetSummary(m.workers)
	m.modelGroups = metrics.GroupWorkersByModel(m.workers)

	// Main view should not panic.
	out := m.View()
	if out == "" {
		t.Error("expected non-empty View output")
	}

	// Detail view.
	m.currentView = ViewDetail
	out = m.View()
	if out == "" {
		t.Error("expected non-empty detail View output")
	}

	// Help view.
	m.currentView = ViewHelp
	out = m.View()
	if out == "" {
		t.Error("expected non-empty help View output")
	}

	// Model group view.
	m.currentView = ViewModelGroup
	out = m.View()
	if out == "" {
		t.Error("expected non-empty model group View output")
	}
}

func TestDataMsgClampsSelectedIdx(t *testing.T) {
	m := newTestModel()
	m.selectedIdx = 10 // beyond any worker list

	workers := testWorkers()
	summary := metrics.ComputeFleetSummary(workers)
	msg := dataMsg{workers: workers, summary: summary}

	updated, _ := m.Update(msg)
	model := updated.(Model)

	if model.selectedIdx >= len(model.workers) {
		t.Errorf("expected selectedIdx to be clamped, got %d with %d workers",
			model.selectedIdx, len(model.workers))
	}
}
