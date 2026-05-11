// Package ui provides the Bubbletea TUI for llmtop.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/collector"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View represents the current UI view mode.
type View int

const (
	ViewMain View = iota
	ViewDetail
	ViewHelp
	ViewGPU
	ViewGPUDetail
	ViewModelGroup
	ViewKVCache
	ViewPDPools
	ViewAlerts
	ViewOverlay // detail overlay on top of current view
	// Round-2 views — appended so existing iota positions stay stable.
	ViewBookmarkCompare  // V8
	ViewEvictionTimeline // V10
	ViewCapabilityMatrix // V11
)

// V1 render path audit — currentFrame() callers (canonical list).
//
// Future maintainers: any new render entry point that reads m.workers,
// m.summary, or m.alertMgr.All() MUST go through currentFrame() so V1
// scrubbing stays consistent. detectEvents is the documented exception:
// events are live-only and read m.workers directly. Build-time grep:
//
//   grep -nE 'm\.workers|m\.summary|m\.alertMgr\.All\(\)' internal/ui/app.go
//
// should return only currentFrame(), detectEvents, sortWorkers, and the
// dataMsg handler. Any other hit is a bug.
//
// Routed through currentFrame():
//   - renderMain
//   - renderKVCacheMain
//   - renderModelMain
//   - renderPDPoolsMain
//   - renderAlertsMain
//   - selectedWorker
//   - displayWorkers
//   - scrollViewportToSelection (uses displayWorkers)
//   - renderFooter scroll indicator
//   - renderBookmarkCompareView
//   - renderCapabilityMatrixView
//
// Live-only (must not use currentFrame()):
//   - detectEvents
//   - dataMsg handler (writes the live state, then pushes ring)

// tickMsg is sent on each refresh interval.
type tickMsg time.Time

// refreshMsg is sent on manual refresh request.
type refreshMsg struct{}

// exportDoneMsg signals that a JSON export completed.
type exportDoneMsg struct {
	filename string
	err      error
}

// dataMsg carries a new set of worker metrics.
type dataMsg struct {
	workers    []*metrics.WorkerMetrics
	summary    metrics.FleetSummary
	gpus       []*metrics.GPUInfo
	gpuSummary metrics.GPUSummary
}

// GPUSortColumn represents a column that can be sorted in the GPU view.
type GPUSortColumn int

const (
	GPUSortNone GPUSortColumn = iota
	GPUSortUtil
	GPUSortVRAM
	GPUSortTemp
	GPUSortPower
)

var gpuSortCycle = []GPUSortColumn{GPUSortNone, GPUSortUtil, GPUSortVRAM, GPUSortTemp, GPUSortPower}

// workerSnapshot captures previous-tick state for event delta detection.
type workerSnapshot struct {
	Online          bool
	KVCacheUsagePct float64
	CacheHitRatePct float64
	TTFT_P99        float64
	Role            string
}

// Model is the Bubbletea application model.
type Model struct {
	collector     collector.MetricsSource
	dcgmCollector collector.GPUSource // nil when no GPU source
	workers       []*metrics.WorkerMetrics
	summary       metrics.FleetSummary
	selectedIdx   int
	sortCol       SortColumn
	filterIdx     int // 0=all, 1=vLLM, 2=SGLang, 3=LMCache, 4=NIM
	currentView   View
	width         int
	height        int
	version       string
	intervalSec   int
	lastRefresh   time.Time

	// GPU view state
	gpus           []*metrics.GPUInfo
	gpuSummary     metrics.GPUSummary
	gpuSelectedIdx int
	gpuSortCol     GPUSortColumn

	// Spinner chars for refresh indicator
	spinnerIdx int

	// Kubernetes context name (empty when not using K8s discovery)
	k8sContext string

	// Model-grouped view state
	modelGroups      []metrics.ModelGroup
	modelSelectedIdx int
	modelSortCol     ModelSortColumn
	modelFilter      string // when set, ViewMain shows only workers for this model

	// Event ring buffer for sidebar
	events *metrics.EventRing

	// Previous worker states for event detection
	prevWorkerStates map[string]workerSnapshot

	// Alert manager
	alertMgr         *metrics.AlertManager
	alertSelectedIdx int
	prevClusterHit   float64
	prevClusterHitAt time.Time

	// Filter bar state
	filterText   string
	filterActive bool

	// Overlay: stash the view to return to
	overlayReturnView View

	// F4: configurable alert thresholds (passed from app.New at startup,
	// used by detectEvents for KV% / TTFT event ring entries).
	alertThresholds metrics.AlertThresholds

	// F5: workers tab viewport scrolling state. viewportOffset is the
	// index of the first visible tableRow in the rendered list.
	viewportOffset int

	// V1 Time-Travel Buffer (round-2). Ring is allocated lazily on the
	// first dataMsg using max(900, 1800/intervalSec) capacity. travelOffset
	// counts ticks back from newest (0 = live). travelAnchor records a T0
	// reference set by the user with the 't' key.
	ring         *metrics.RingBuffer
	travelOffset int
	travelAnchor time.Time

	// V3 Anomaly halo. anomaly is nil-able — DIST owns the concrete
	// implementation (*metrics.AnomalyStore); UI consumes the interface.
	// anomalyFilterOn restricts the Workers table to rows with |sigma|>=2.
	anomaly         AnomalyGetter
	anomalyFilterOn bool

	// V6 Workload Pulse. fleetHistory is a 60-slot ring of FleetSummary
	// values pushed every dataMsg; powers the 2-row sparkbar strip.
	fleetHistory     [60]metrics.FleetSummary
	fleetHistoryHead int
	fleetHistoryLen  int

	// V8 Bookmark & Compare. bookmarks may be nil (tests).
	bookmarks       *BookmarkStore
	footerStatus    string
	footerStatusTks int

	// V10 KV Eviction Timeline.
	timelineAlert  *metrics.Alert
	timelineWindow []metrics.FrameSnapshot
	timelineWorker string
	timelineT0Idx  int

	// V12 Sim Demo. switchScenario is the closure DIST passes; nil when
	// not running in --demo mode. demoHintTicks counts down from 5 on the
	// first ticks to surface a one-time long-form footer hint.
	demoMode       bool
	demoScenario   string
	demoHintTicks  int
	switchScenario func(name string)
}

var filterCycle = []metrics.Backend{
	metrics.BackendUnknown, // means "all"
	metrics.BackendVLLM,
	metrics.BackendSGLang,
	metrics.BackendLMCache,
	metrics.BackendNIM,
	metrics.BackendTGI,
	metrics.BackendTRTLLM,
	metrics.BackendTriton,
	metrics.BackendLlamaCpp,
	metrics.BackendLiteLLM,
	metrics.BackendOllama,
}

var sortCycle = []SortColumn{
	SortNone,
	SortKVCache,
	SortQueue,
	SortTTFT,
	SortHitRate,
	SortTokPerSec,
}

// NewModel creates a new application model.
//
// The thresholds parameter (F4) is the resolved set of alert thresholds.
// Callers without a config file should pass metrics.DefaultAlertThresholds().
//
// Round-2 trailing parameters:
//   - demoMode (V12): true when --demo flag was passed; enables S-key
//     scenario cycling and the demo-mode footer.
//   - switchScenario (V12): closure provided by app.New that wraps
//     sim.Simulator.SwitchScenario; nil when not in demo mode.
//
// Bookmarks (V8) load from DefaultBookmarksPath() automatically — failure
// is silent (an empty store is returned). Ring (V1), fleet history (V6),
// and demoHintTicks initialize lazily on the first dataMsg.
func NewModel(c collector.MetricsSource, dc collector.GPUSource, version string, intervalSec int, k8sContext string, thresholds metrics.AlertThresholds, demoMode bool, switchScenario func(string)) Model {
	bookmarks := NewBookmarkStore(DefaultBookmarksPath())
	scenario := ""
	hintTicks := 0
	if demoMode {
		scenario = "demo"
		hintTicks = 5
	}
	return Model{
		collector:        c,
		dcgmCollector:    dc,
		version:          version,
		intervalSec:      intervalSec,
		k8sContext:       k8sContext,
		events:           metrics.NewEventRing(20),
		prevWorkerStates: make(map[string]workerSnapshot),
		alertMgr:         metrics.NewAlertManagerWithThresholds(thresholds),
		alertThresholds:  thresholds,
		bookmarks:        bookmarks,
		demoMode:         demoMode,
		demoScenario:     scenario,
		demoHintTicks:    hintTicks,
		switchScenario:   switchScenario,
	}
}

// WithAnomalyStore wires a V3 anomaly source into the model. The model
// is returned by value (Bubbletea convention) so callers chain it onto
// NewModel: `m := ui.NewModel(...).WithAnomalyStore(store)`. DIST owns
// constructing *metrics.AnomalyStore and passing it through here from
// app.New.
func (m Model) WithAnomalyStore(a AnomalyGetter) Model {
	m.anomaly = a
	return m
}

// Init starts the polling loop and initial tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(time.Duration(m.intervalSec)*time.Second),
		refreshCmd(),
	)
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func refreshCmd() tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{}
	}
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		return m, tea.Batch(
			tickCmd(time.Duration(m.intervalSec)*time.Second),
			fetchDataCmd(m.collector, m.dcgmCollector),
		)

	case refreshMsg:
		return m, fetchDataCmd(m.collector, m.dcgmCollector)

	case dataMsg:
		m.workers = msg.workers
		m.summary = msg.summary
		m.gpus = msg.gpus
		m.gpuSummary = msg.gpuSummary
		m.modelGroups = metrics.GroupWorkersByModel(msg.workers)
		m.lastRefresh = time.Now()
		m.spinnerIdx = (m.spinnerIdx + 1) % 4
		// Stable sort: always sort by name first, then apply user sort on top
		sort.SliceStable(m.workers, func(i, j int) bool {
			a, b := m.workers[i], m.workers[j]
			if a.Online != b.Online {
				return a.Online
			}
			return a.Label < b.Label
		})
		if m.sortCol != SortNone {
			m.sortWorkers()
		}
		// Clamp selected indices
		if m.selectedIdx >= len(m.workers) && len(m.workers) > 0 {
			m.selectedIdx = len(m.workers) - 1
		}
		if m.gpuSelectedIdx >= len(m.gpus) && len(m.gpus) > 0 {
			m.gpuSelectedIdx = len(m.gpus) - 1
		}
		if m.modelSelectedIdx >= len(m.modelGroups) && len(m.modelGroups) > 0 {
			m.modelSelectedIdx = len(m.modelGroups) - 1
		}
		// F5: clamp viewport offset against the (possibly resized) row list,
		// and re-anchor it to the selected row so it stays visible.
		{
			rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
			visible := m.workersVisibleRows()
			start, _ := ClampViewport(len(rows), visible, m.viewportOffset)
			m.viewportOffset = start
			m.scrollViewportToSelection(rows)
		}
		// Generate events by comparing with previous state. Always live —
		// must NOT route through currentFrame() (events are live-only).
		m.detectEvents()
		// Evaluate alert rules
		if m.alertMgr != nil {
			m.alertMgr.Evaluate(&metrics.AlertState{
				Workers:        m.workers,
				GPUs:           m.gpus,
				Summary:        m.summary,
				PrevCacheHit:   m.prevClusterHit,
				PrevCacheHitAt: m.prevClusterHitAt,
			})
			m.prevClusterHit = m.summary.AvgCacheHit
			m.prevClusterHitAt = time.Now()
		}

		// V1: lazy-allocate ring with capacity = max(900, 1800/intervalSec).
		if m.ring == nil {
			interval := m.intervalSec
			if interval < 1 {
				interval = 2
			}
			capTicks := max(1800/interval, 900)
			m.ring = metrics.NewRingBuffer(capTicks)
		}
		// Push the snapshot AFTER alert evaluation so resolved alerts are
		// captured. Convert the AlertManager's []*Alert to []Alert for the
		// snapshot's value-slice contract.
		var alertVals []metrics.Alert
		if m.alertMgr != nil {
			ptrs := m.alertMgr.All()
			alertVals = make([]metrics.Alert, 0, len(ptrs))
			for _, a := range ptrs {
				if a != nil {
					alertVals = append(alertVals, *a)
				}
			}
		}
		m.ring.Push(metrics.CaptureFrame(m.workers, m.summary, alertVals))

		// V6: push to fleetHistory ring.
		m.fleetHistory[m.fleetHistoryHead] = m.summary
		m.fleetHistoryHead = (m.fleetHistoryHead + 1) % len(m.fleetHistory)
		if m.fleetHistoryLen < len(m.fleetHistory) {
			m.fleetHistoryLen++
		}

		// V8: reconcile bookmarks against the live cluster.
		if m.bookmarks != nil {
			m.bookmarks.Reconcile(m.k8sContext, m.workers)
		}

		// V12: decrement demo hint countdown (one-shot long footer).
		if m.demoHintTicks > 0 {
			m.demoHintTicks--
		}

		// Footer status countdown (V8 max-bookmark message etc).
		if m.footerStatusTks > 0 {
			m.footerStatusTks--
			if m.footerStatusTks == 0 {
				m.footerStatus = ""
			}
		}

		return m, nil

	case exportDoneMsg:
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func fetchDataCmd(c collector.MetricsSource, dc collector.GPUSource) tea.Cmd {
	return func() tea.Msg {
		workers := c.GetAll()
		summary := metrics.ComputeFleetSummary(workers)
		msg := dataMsg{workers: workers, summary: summary}
		if dc != nil {
			msg.gpus = dc.GetAll()
			msg.gpuSummary = dc.GetSummary()
		}
		return msg
	}
}

func exportJSONCmd(workers []*metrics.WorkerMetrics, summary metrics.FleetSummary, gpus []*metrics.GPUInfo, gpuSummary metrics.GPUSummary) tea.Cmd {
	return func() tea.Msg {
		filename := fmt.Sprintf("llmtop-export-%s.json", time.Now().Format("20060102-150405"))
		envelope := struct {
			Summary     metrics.FleetSummary     `json:"summary"`
			Workers     []*metrics.WorkerMetrics `json:"workers"`
			ModelGroups []metrics.ModelGroup     `json:"model_groups,omitempty"`
			GPUSummary  *metrics.GPUSummary      `json:"gpu_summary,omitempty"`
			GPUs        []*metrics.GPUInfo       `json:"gpus,omitempty"`
		}{
			Summary:     summary,
			Workers:     workers,
			ModelGroups: metrics.GroupWorkersByModel(workers),
		}
		if len(gpus) > 0 {
			s := gpuSummary
			envelope.GPUSummary = &s
			envelope.GPUs = gpus
		}
		data, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return exportDoneMsg{err: err}
		}
		err = os.WriteFile(filename, data, 0o600)
		return exportDoneMsg{filename: filename, err: err}
	}
}

// handleScrubKey processes V1 time-travel keys ([, ], \, t). Returns
// (true, model) when the key was handled. Disabled when the ring is nil
// (test models, --once mode).
func (m Model) handleScrubKey(msg tea.KeyMsg) (bool, Model) {
	if m.ring == nil {
		return false, m
	}
	switch msg.String() {
	case "[":
		// Step backward; clamp at the oldest stored frame.
		if m.travelOffset < m.ring.Len()-1 {
			m.travelOffset++
		}
		return true, m
	case "]":
		// Step forward toward live.
		if m.travelOffset > 0 {
			m.travelOffset--
		}
		return true, m
	case "\\":
		m.travelOffset = 0
		m.travelAnchor = time.Time{}
		return true, m
	case "t":
		// Toggle a T0 anchor at the current scrub position.
		if m.travelOffset > 0 {
			if f, ok := m.ring.At(m.travelOffset); ok {
				m.travelAnchor = f.At
			}
		} else {
			m.travelAnchor = time.Time{}
		}
		return true, m
	}
	return false, m
}

// handleGlobalRound2Key processes round-2 keys that work from any primary
// tab: 'C' (capability matrix), 'B' (bookmark compare), 'S' (scenario
// switch in demo mode). Returns (true, model) when the key was handled.
func (m Model) handleGlobalRound2Key(msg tea.KeyMsg) (bool, Model) {
	switch msg.String() {
	case "C":
		m.overlayReturnView = m.currentView
		m.currentView = ViewCapabilityMatrix
		return true, m
	case "B":
		m.overlayReturnView = m.currentView
		m.currentView = ViewBookmarkCompare
		return true, m
	case "S":
		if m.demoMode && m.switchScenario != nil {
			m.demoScenario = nextScenario(m.demoScenario)
			m.switchScenario(m.demoScenario)
		}
		return true, m
	}
	return false, m
}

// isKVEvictionAlert reports whether the alert is one of the KV-eviction
// rule families (kv_critical, kv_pressure). Title-based check matches the
// AlertManager's title format ("KV cache critical on …", "KV pressure …").
func isKVEvictionAlert(a *metrics.Alert) bool {
	if a == nil {
		return false
	}
	title := strings.ToLower(a.Title)
	if !strings.Contains(title, "kv") {
		return false
	}
	if strings.Contains(title, "critical") || strings.Contains(title, "pressure") || strings.Contains(title, "cache") {
		return true
	}
	return false
}

// findT0Idx returns the frame index in window closest to fire time. Used
// to position the T=0 cursor in RenderEvictionTimeline.
func findT0Idx(window []metrics.FrameSnapshot, fireAt time.Time) int {
	best := 0
	var bestDelta time.Duration
	for i, f := range window {
		d := f.At.Sub(fireAt)
		if d < 0 {
			d = -d
		}
		if i == 0 || d < bestDelta {
			best = i
			bestDelta = d
		}
	}
	return best
}

// nextScenario cycles steady → demo → stress → chaos → steady.
func nextScenario(current string) string {
	cycle := []string{"steady", "demo", "stress", "chaos"}
	for i, s := range cycle {
		if s == current {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return "steady"
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Round-2 overlay views first — esc returns to overlayReturnView.
	switch m.currentView {
	case ViewBookmarkCompare, ViewEvictionTimeline, ViewCapabilityMatrix:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc", "B", "C":
			m.currentView = m.overlayReturnView
			return m, nil
		default:
			m.currentView = m.overlayReturnView
			return m, nil
		}
	}

	// V1 time-travel scrub keys — work from any primary view (not from
	// help/detail overlays which have their own dismiss-on-key behavior).
	switch m.currentView {
	case ViewMain, ViewKVCache, ViewModelGroup, ViewPDPools, ViewAlerts, ViewGPU:
		if handled, model := m.handleScrubKey(msg); handled {
			return model, nil
		}
	}

	switch m.currentView {
	case ViewDetail, ViewHelp:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			m.currentView = ViewMain
			return m, nil
		}

	case ViewGPUDetail:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			m.currentView = ViewGPU
			return m, nil
		}

	case ViewGPU:
		return m.handleGPUKey(msg)

	case ViewModelGroup:
		return m.handleModelGroupKey(msg)

	case ViewKVCache:
		return m.handleKVCacheKey(msg)

	case ViewPDPools:
		return m.handlePDPoolsKey(msg)

	case ViewAlerts:
		return m.handleAlertsKey(msg)

	case ViewOverlay:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.currentView = m.overlayReturnView
			return m, nil
		}
		return m, nil
	}

	// Filter bar: / key starts filter mode
	if msg.String() == "/" {
		m.filterActive = true
		m.filterText = ""
		return m, nil
	}

	// If filter is active, handle text input
	if m.filterActive {
		switch msg.String() {
		case "esc":
			m.filterActive = false
			m.filterText = ""
		case "backspace":
			if len(m.filterText) > 0 {
				m.filterText = m.filterText[:len(m.filterText)-1]
			}
		case "enter":
			m.filterActive = false
		default:
			if len(msg.String()) == 1 {
				m.filterText += msg.String()
			}
		}
		// Filter text changes the row count: snap viewport+selection to top
		// so we never strand the cursor in an empty viewport.
		m.viewportOffset = 0
		m.selectedIdx = 0
		return m, nil
	}

	// Tab shortcuts available from main view
	if handled, cmd := m.handleTabSwitch(msg); handled {
		return m, cmd
	}

	// Global round-2 keys (C/B/S).
	if handled, model := m.handleGlobalRound2Key(msg); handled {
		return model, nil
	}

	// Main view
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "a":
		// V3: toggle anomaly filter.
		m.anomalyFilterOn = !m.anomalyFilterOn
		m.viewportOffset = 0
		m.selectedIdx = 0

	case "b":
		// V8: toggle bookmark on the selected worker.
		if m.bookmarks != nil {
			if w := m.selectedWorker(); w != nil {
				added, err := m.bookmarks.Toggle(m.k8sContext, w.Endpoint, w.Label)
				if err != nil {
					m.footerStatus = "(max " + fmt.Sprintf("%d", MaxBookmarks) + " bookmarks)"
					m.footerStatusTks = 1
				} else {
					if err := m.bookmarks.Save(); err != nil {
						if m.events != nil {
							m.events.Push(metrics.SeverityWarn, "bookmark save failed: %v", err)
						}
					}
					if added {
						m.footerStatus = "bookmarked " + w.Label
					} else {
						m.footerStatus = "unbookmarked " + w.Label
					}
					m.footerStatusTks = 1
				}
			}
		}

	case "up", "k":
		rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
		m.selectedIdx = NextDataRow(rows, m.selectedIdx, -1)
		m.scrollViewportToSelection(rows)

	case "down", "j":
		rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
		m.selectedIdx = NextDataRow(rows, m.selectedIdx, +1)
		m.scrollViewportToSelection(rows)

	case "pgup", "ctrl+b":
		m.viewportPageBy(-1)

	case "pgdown", "ctrl+f":
		m.viewportPageBy(+1)

	case "home":
		rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
		if first := FirstDataRowAt(rows, 0); first >= 0 {
			m.selectedIdx = first
		}
		m.viewportOffset = 0

	case "end":
		rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
		if last := LastDataRowAtOrBefore(rows, len(rows)-1); last >= 0 {
			m.selectedIdx = last
		}
		visible := m.workersVisibleRows()
		if visible > 0 && len(rows) > visible {
			m.viewportOffset = len(rows) - visible
		} else {
			m.viewportOffset = 0
		}

	case "s":
		// Cycle sort column
		for i, c := range sortCycle {
			if c == m.sortCol {
				m.sortCol = sortCycle[(i+1)%len(sortCycle)]
				break
			}
		}
		if m.sortCol != SortNone {
			m.sortWorkers()
		}
		// Sort changes physical row order; re-anchor viewport on the
		// selected row so it remains visible.
		{
			rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
			m.scrollViewportToSelection(rows)
		}

	case "f":
		// Cycle filter
		m.filterIdx = (m.filterIdx + 1) % len(filterCycle)
		m.viewportOffset = 0
		m.selectedIdx = 0

	case "d", "enter":
		if len(m.workers) > 0 {
			m.overlayReturnView = ViewMain
			m.currentView = ViewOverlay
		}

	case "r":
		m.collector.PollNow(context.TODO())
		if m.dcgmCollector != nil {
			m.dcgmCollector.PollNow(context.TODO())
		}
		return m, fetchDataCmd(m.collector, m.dcgmCollector)

	case "e":
		return m, exportJSONCmd(m.workers, m.summary, m.gpus, m.gpuSummary)

	case "g":
		if m.dcgmCollector != nil {
			m.currentView = ViewGPU
		}

	case "m":
		if m.modelFilter != "" {
			// Drill-down is active — clear filter and return to model view.
			m.modelFilter = ""
			m.currentView = ViewModelGroup
		} else {
			m.currentView = ViewModelGroup
		}

	case "?":
		m.currentView = ViewHelp
	}

	return m, nil
}

func (m Model) handleModelGroupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.handleTabSwitch(msg); handled {
		return m, cmd
	}
	if handled, model := m.handleGlobalRound2Key(msg); handled {
		return model, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "m":
		m.currentView = ViewMain

	case "up", "k":
		if m.modelSelectedIdx > 0 {
			m.modelSelectedIdx--
		}

	case "down", "j":
		if m.modelSelectedIdx < len(m.modelGroups)-1 {
			m.modelSelectedIdx++
		}

	case "s":
		for i, c := range modelSortCycle {
			if c == m.modelSortCol {
				m.modelSortCol = modelSortCycle[(i+1)%len(modelSortCycle)]
				break
			}
		}
		if m.modelSortCol != ModelSortNone {
			m.sortModelGroups()
		}

	case "d", "enter":
		if len(m.modelGroups) > 0 && m.modelSelectedIdx < len(m.modelGroups) {
			m.modelFilter = m.modelGroups[m.modelSelectedIdx].ModelName
			m.selectedIdx = 0
			m.currentView = ViewMain
		}

	case "r":
		m.collector.PollNow(context.TODO())
		if m.dcgmCollector != nil {
			m.dcgmCollector.PollNow(context.TODO())
		}
		return m, fetchDataCmd(m.collector, m.dcgmCollector)

	case "e":
		return m, exportJSONCmd(m.workers, m.summary, m.gpus, m.gpuSummary)

	case "?":
		m.currentView = ViewHelp
	}

	return m, nil
}

func (m *Model) sortModelGroups() {
	sort.SliceStable(m.modelGroups, func(i, j int) bool {
		a, b := m.modelGroups[i], m.modelGroups[j]
		switch m.modelSortCol {
		case ModelSortName:
			return a.ModelName < b.ModelName
		case ModelSortTokPerSec:
			return a.TotalTokPerSec > b.TotalTokPerSec
		case ModelSortAvgKV:
			return a.AvgKVCachePct > b.AvgKVCachePct
		case ModelSortQueue:
			return a.TotalQueue > b.TotalQueue
		case ModelSortRunning:
			return a.TotalRunning > b.TotalRunning
		case ModelSortAvgTTFT:
			return a.AvgTTFTP99 > b.AvgTTFTP99
		}
		return false
	})
}

func (m Model) handleGPUKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.handleTabSwitch(msg); handled {
		return m, cmd
	}
	if handled, model := m.handleGlobalRound2Key(msg); handled {
		return model, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "g":
		m.currentView = ViewMain

	case "up", "k":
		// Move up one row in 2-column grid
		if m.gpuSelectedIdx-2 >= 0 {
			m.gpuSelectedIdx -= 2
		}

	case "down", "j":
		// Move down one row in 2-column grid
		if m.gpuSelectedIdx+2 < len(m.gpus) {
			m.gpuSelectedIdx += 2
		}

	case "left", "h":
		if m.gpuSelectedIdx > 0 {
			m.gpuSelectedIdx--
		}

	case "right", "l":
		if m.gpuSelectedIdx < len(m.gpus)-1 {
			m.gpuSelectedIdx++
		}

	case "s":
		for i, c := range gpuSortCycle {
			if c == m.gpuSortCol {
				m.gpuSortCol = gpuSortCycle[(i+1)%len(gpuSortCycle)]
				break
			}
		}
		if m.gpuSortCol != GPUSortNone {
			m.sortGPUs()
		}

	case "d":
		if len(m.gpus) > 0 {
			m.currentView = ViewGPUDetail
		}

	case "enter":
		// Jump to the worker running on this GPU
		if m.gpuSelectedIdx < len(m.gpus) {
			gpu := m.gpus[m.gpuSelectedIdx]
			if gpu.Pod != "" {
				for i, w := range m.workers {
					if strings.Contains(w.Label, gpu.Pod) || strings.Contains(w.Endpoint, gpu.Pod) {
						m.selectedIdx = i
						m.currentView = ViewMain
						return m, nil
					}
				}
			}
		}

	case "r":
		m.collector.PollNow(context.TODO())
		if m.dcgmCollector != nil {
			m.dcgmCollector.PollNow(context.TODO())
		}
		return m, fetchDataCmd(m.collector, m.dcgmCollector)

	case "e":
		return m, exportJSONCmd(m.workers, m.summary, m.gpus, m.gpuSummary)

	case "?":
		m.currentView = ViewHelp
	}

	return m, nil
}

func (m *Model) sortGPUs() {
	sort.SliceStable(m.gpus, func(i, j int) bool {
		a, b := m.gpus[i], m.gpus[j]
		switch m.gpuSortCol {
		case GPUSortUtil:
			return a.UtilPct > b.UtilPct
		case GPUSortVRAM:
			if a.MemTotalMB > 0 && b.MemTotalMB > 0 {
				return (a.MemUsedMB / a.MemTotalMB) > (b.MemUsedMB / b.MemTotalMB)
			}
			return a.MemUsedMB > b.MemUsedMB
		case GPUSortTemp:
			return a.TempC > b.TempC
		case GPUSortPower:
			return a.PowerW > b.PowerW
		}
		return false
	})
}

func (m *Model) sortWorkers() {
	sort.SliceStable(m.workers, func(i, j int) bool {
		a, b := m.workers[i], m.workers[j]
		// Online workers always before offline
		if a.Online != b.Online {
			return a.Online
		}
		switch m.sortCol {
		case SortKVCache:
			return a.KVCacheUsagePct > b.KVCacheUsagePct
		case SortQueue:
			return a.RequestsWaiting > b.RequestsWaiting
		case SortTTFT:
			return a.TTFT_P99 > b.TTFT_P99
		case SortHitRate:
			return a.CacheHitRatePct > b.CacheHitRatePct
		case SortTokPerSec:
			return (a.PromptTokPerSec + a.GenTokPerSec) > (b.PromptTokPerSec + b.GenTokPerSec)
		}
		return false
	})
}

// handleTabSwitch handles number-key tab switching from any primary view.
// Returns (true, cmd) if the key was a tab switch, (false, nil) otherwise.
func (m *Model) handleTabSwitch(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "1":
		m.currentView = ViewMain
		return true, nil
	case "2":
		if m.dcgmCollector != nil {
			m.currentView = ViewGPU
		}
		return true, nil
	case "3":
		m.currentView = ViewKVCache
		return true, nil
	case "4":
		m.currentView = ViewModelGroup
		return true, nil
	case "5":
		m.currentView = ViewPDPools
		return true, nil
	case "6":
		m.currentView = ViewAlerts
		return true, nil
	}
	return false, nil
}

// handleKVCacheKey handles keys in the KV Cache tab.
func (m Model) handleKVCacheKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.handleTabSwitch(msg); handled {
		return m, cmd
	}
	if handled, model := m.handleGlobalRound2Key(msg); handled {
		return model, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		m.collector.PollNow(context.TODO())
		if m.dcgmCollector != nil {
			m.dcgmCollector.PollNow(context.TODO())
		}
		return m, fetchDataCmd(m.collector, m.dcgmCollector)
	case "e":
		return m, exportJSONCmd(m.workers, m.summary, m.gpus, m.gpuSummary)
	case "?":
		m.currentView = ViewHelp
	}
	return m, nil
}

// handlePDPoolsKey handles keys in the P/D Pools tab.
func (m Model) handlePDPoolsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.handleTabSwitch(msg); handled {
		return m, cmd
	}
	if handled, model := m.handleGlobalRound2Key(msg); handled {
		return model, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		m.collector.PollNow(context.TODO())
		if m.dcgmCollector != nil {
			m.dcgmCollector.PollNow(context.TODO())
		}
		return m, fetchDataCmd(m.collector, m.dcgmCollector)
	case "e":
		return m, exportJSONCmd(m.workers, m.summary, m.gpus, m.gpuSummary)
	case "?":
		m.currentView = ViewHelp
	}
	return m, nil
}

// handleAlertsKey handles keys in the Alerts tab.
func (m Model) handleAlertsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.handleTabSwitch(msg); handled {
		return m, cmd
	}
	if handled, model := m.handleGlobalRound2Key(msg); handled {
		return model, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.alertSelectedIdx > 0 {
			m.alertSelectedIdx--
		}
	case "down", "j":
		alerts := m.alertMgr.All()
		if m.alertSelectedIdx < len(alerts)-1 {
			m.alertSelectedIdx++
		}
	case "enter":
		// V10: KV-eviction alert → open timeline view.
		// Other alert types → jump to source worker in Workers tab.
		alerts := m.alertMgr.All()
		if m.alertSelectedIdx < len(alerts) {
			a := alerts[m.alertSelectedIdx]
			if isKVEvictionAlert(a) {
				if m.ring == nil {
					m.footerStatus = "(time-travel buffer not yet initialized)"
					m.footerStatusTks = 2
					return m, nil
				}
				window := m.ring.ExtractTimelineWindow(a.FiredAt, 30)
				if window == nil {
					m.footerStatus = "(alert predates time-travel buffer)"
					m.footerStatusTks = 2
					return m, nil
				}
				ep := a.SourceEndpoint
				m.timelineAlert = a
				m.timelineWindow = window
				m.timelineWorker = ep
				m.timelineT0Idx = findT0Idx(window, a.FiredAt)
				m.overlayReturnView = ViewAlerts
				m.currentView = ViewEvictionTimeline
				return m, nil
			}
			for i, w := range m.workers {
				if w.Label == a.Source || strings.Contains(w.Label, a.Source) {
					m.selectedIdx = i
					m.currentView = ViewMain
					return m, nil
				}
			}
			// No match — show footer hint.
			m.footerStatus = "(timeline only available for KV eviction alerts)"
			m.footerStatusTks = 2
		}
	case "r":
		m.collector.PollNow(context.TODO())
		if m.dcgmCollector != nil {
			m.dcgmCollector.PollNow(context.TODO())
		}
		return m, fetchDataCmd(m.collector, m.dcgmCollector)
	case "e":
		return m, exportJSONCmd(m.workers, m.summary, m.gpus, m.gpuSummary)
	case "?":
		m.currentView = ViewHelp
	}
	return m, nil
}

// View renders the current application state.
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	switch m.currentView {
	case ViewDetail:
		return m.renderDetail()
	case ViewHelp:
		return m.renderHelp()
	case ViewGPU:
		return m.renderGPUMain()
	case ViewGPUDetail:
		return m.renderGPUDetail()
	case ViewModelGroup:
		return m.renderModelMain()
	case ViewKVCache:
		return m.renderKVCacheMain()
	case ViewPDPools:
		return m.renderPDPoolsMain()
	case ViewAlerts:
		return m.renderAlertsMain()
	case ViewOverlay:
		return m.renderOverlay()
	case ViewBookmarkCompare:
		return m.renderBookmarkCompareView()
	case ViewEvictionTimeline:
		return m.renderEvictionTimelineView()
	case ViewCapabilityMatrix:
		return m.renderCapabilityMatrixView()
	}

	return m.renderMain()
}

// renderBookmarkCompareView wraps RenderBookmarkCompare with the live
// worker pointers and ring; entered via 'B' key from any primary tab.
func (m Model) renderBookmarkCompareView() string {
	workers, _, _ := m.currentFrame()
	var bms []WorkerBookmark
	if m.bookmarks != nil {
		bms = m.bookmarks.List(m.k8sContext)
	}
	return RenderBookmarkCompare(workers, m.ring, bms, m.width, m.height)
}

// renderEvictionTimelineView dispatches to RenderEvictionTimeline using
// the cached timeline window captured when the user pressed Enter on a
// KV-eviction alert.
func (m Model) renderEvictionTimelineView() string {
	return RenderEvictionTimeline(m.timelineWindow, m.timelineAlert, m.timelineWorker, m.timelineT0Idx, m.width, m.height)
}

// renderCapabilityMatrixView extracts detected backends from the live
// workers slice and dispatches to RenderCapabilityMatrix.
func (m Model) renderCapabilityMatrixView() string {
	workers, _, _ := m.currentFrame()
	detected := detectedBackends(workers)
	return RenderCapabilityMatrix(detected, m.width, m.height)
}

// alertsCustomized reports whether m.alertThresholds differs from the
// production defaults. Used to surface "alerts: custom" in the header.
// A zero-value AlertThresholds (test-mode Model) is treated as default.
func (m Model) alertsCustomized() bool {
	zero := metrics.AlertThresholds{}
	if m.alertThresholds == zero {
		return false
	}
	return m.alertThresholds != metrics.DefaultAlertThresholds()
}

// workersVisibleRows returns the number of body rows the Workers tab can
// show in the current terminal. The header (header bar + tab bar +
// indicator lines + table header + table separator) plus the footer eat
// fixed lines; the rest is body. The count is clamped to a minimum of 1
// so the table is never empty.
//
// When width == 0 (pre-init), this returns 0 to signal "no clamping yet."
func (m Model) workersVisibleRows() int {
	if m.width == 0 || m.height == 0 {
		return 0
	}
	// header (1) + tab bar (1) + table header (1) + table separator (1) +
	// footer (1) + 2 buffer = 7 lines of chrome. Indicator lines (k8s,
	// model filter, filter, sort) are visible only when set.
	chrome := 7
	// V6: pulse strip adds 2 lines unconditionally when terminal is tall
	// enough for it to render (>= 12 rows). RenderPulseStrip itself returns
	// "" when m.height < 12 so the chrome count must mirror that gate.
	if m.height >= 12 {
		chrome += 2
	}
	if m.k8sContext != "" {
		chrome++
	}
	if m.modelFilter != "" {
		chrome++
	}
	if filterCycle[m.filterIdx] != metrics.BackendUnknown {
		chrome++
	}
	if m.sortCol != SortNone {
		chrome++
	}
	if m.alertsCustomized() {
		chrome++
	}
	if m.anomalyFilterOn {
		chrome++
	}
	return max(m.height-chrome, 1)
}

// scrollViewportToSelection nudges the viewport so the selected data row
// remains visible after a j/k cursor move. The viewport scrolls by exactly
// one row at a time to match the spec invariant.
func (m *Model) scrollViewportToSelection(rows []tableRow) {
	visible := m.workersVisibleRows()
	if visible == 0 {
		return
	}
	disp := DisplayIndexOf(rows, m.selectedIdx)
	if disp < 0 {
		return
	}
	if disp < m.viewportOffset {
		m.viewportOffset = disp
	}
	if disp > m.viewportOffset+visible-1 {
		m.viewportOffset = disp - (visible - 1)
	}
	// Clamp.
	start, _ := ClampViewport(len(rows), visible, m.viewportOffset)
	m.viewportOffset = start
}

// viewportPageBy moves the viewport by direction*visibleRows pages, then
// snaps the cursor to the first selectable data row in the new viewport.
func (m *Model) viewportPageBy(direction int) {
	rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
	if len(rows) == 0 {
		return
	}
	visible := m.workersVisibleRows()
	if visible == 0 {
		return
	}
	m.viewportOffset += direction * visible
	start, _ := ClampViewport(len(rows), visible, m.viewportOffset)
	m.viewportOffset = start
	if next := FirstDataRowAt(rows, m.viewportOffset); next >= 0 {
		m.selectedIdx = next
	}
}

// displayWorkers returns the worker slice to display, applying model
// drill-down, text filter, and V3 anomaly filter on top of currentFrame()
// (V1 time-travel routes here when scrubbing).
func (m Model) displayWorkers() []*metrics.WorkerMetrics {
	source, _, _ := m.currentFrame()
	var display []*metrics.WorkerMetrics
	if m.modelFilter != "" {
		for _, w := range source {
			name := w.ModelName
			if name == "" {
				name = "Unknown"
			}
			if name == m.modelFilter {
				display = append(display, w)
			}
		}
	} else {
		display = source
	}
	if m.filterText != "" {
		var filtered []*metrics.WorkerMetrics
		lower := strings.ToLower(m.filterText)
		for _, w := range display {
			if strings.Contains(strings.ToLower(w.Label), lower) ||
				strings.Contains(strings.ToLower(w.ModelName), lower) ||
				strings.Contains(strings.ToLower(w.Endpoint), lower) {
				filtered = append(filtered, w)
			}
		}
		display = filtered
	}
	// V3: anomaly filter — keep only rows with |sigma| >= 2 across any tracked metric.
	if m.anomalyFilterOn && m.anomaly != nil {
		var keep []*metrics.WorkerMetrics
		for _, w := range display {
			sigma, _ := m.anomaly.MaxSigma(w.Endpoint, w)
			abs := sigma
			if abs < 0 {
				abs = -abs
			}
			if abs >= 2 {
				keep = append(keep, w)
			}
		}
		display = keep
	}
	return display
}

// selectedWorker returns the currently selected worker, or nil.
func (m Model) selectedWorker() *metrics.WorkerMetrics {
	display := m.displayWorkers()
	rows := BuildTableRows(display, filterCycle[m.filterIdx])
	for _, r := range rows {
		if r.worker != nil && r.dataIdx == m.selectedIdx {
			return r.worker
		}
	}
	if len(display) > 0 && m.selectedIdx < len(display) {
		return display[m.selectedIdx]
	}
	return nil
}

// currentFrame returns the data the render path should consume.
//
// When travelOffset == 0 (live), returns the live workers slice, summary,
// and active alerts. When scrubbing, returns the FrameSnapshot at
// travelOffset ticks back, materialized as ([]*WorkerMetrics, summary,
// []Alert) for render compatibility.
//
// The returned worker pointers are owned by the snapshot — the caller
// must treat them as read-only. SnapshotWorker dropped TTFTHistory and
// GenTokHistory; render paths that need history call ring.SparklineFor.
//
// detectEvents must NOT use currentFrame — events are live-only.
func (m Model) currentFrame() ([]*metrics.WorkerMetrics, metrics.FleetSummary, []metrics.Alert) {
	if m.travelOffset == 0 || m.ring == nil {
		alerts := m.liveAlerts()
		return m.workers, m.summary, alerts
	}
	f, ok := m.ring.At(m.travelOffset)
	if !ok {
		alerts := m.liveAlerts()
		return m.workers, m.summary, alerts
	}
	// Materialize the value slice into a pointer slice the render layer
	// already expects. Pointers reference snapshot storage; safe so long
	// as the ring isn't pushing concurrently — Bubbletea is single-thread.
	out := make([]*metrics.WorkerMetrics, len(f.Workers))
	for i := range f.Workers {
		out[i] = &f.Workers[i]
	}
	return out, f.Summary, f.Alerts
}

// liveAlerts dereferences the AlertManager's pointer slice into a value
// slice; returns nil when alertMgr is nil (test models).
func (m Model) liveAlerts() []metrics.Alert {
	if m.alertMgr == nil {
		return nil
	}
	ptrs := m.alertMgr.All()
	out := make([]metrics.Alert, 0, len(ptrs))
	for _, a := range ptrs {
		if a != nil {
			out = append(out, *a)
		}
	}
	return out
}

// fleetHistorySlice returns the V6 fleet history in oldest-first order.
func (m Model) fleetHistorySlice() []metrics.FleetSummary {
	if m.fleetHistoryLen == 0 {
		return nil
	}
	out := make([]metrics.FleetSummary, m.fleetHistoryLen)
	start := m.fleetHistoryHead - m.fleetHistoryLen
	if start < 0 {
		start += len(m.fleetHistory)
	}
	for i := 0; i < m.fleetHistoryLen; i++ {
		out[i] = m.fleetHistory[(start+i)%len(m.fleetHistory)]
	}
	return out
}

// detectEvents compares current worker state against previous snapshot and
// pushes events into the ring buffer when thresholds are crossed.
func (m *Model) detectEvents() {
	if m.events == nil {
		return
	}

	// Resolve thresholds with default fallback so a zero-value Model (e.g.
	// in tests) does not silently disable event detection.
	t := m.alertThresholds
	if t.KVEventPct == 0 && t.TTFTEventMs == 0 && t.CacheHitDropPP == 0 {
		t = metrics.DefaultAlertThresholds()
	}

	for _, w := range m.workers {
		key := w.Endpoint
		prev, hadPrev := m.prevWorkerStates[key]

		if !hadPrev && w.Online {
			m.events.Push(metrics.SeverityOK, "%s joined pool", w.Label)
		}
		if hadPrev && prev.Online && !w.Online {
			m.events.Push(metrics.SeverityError, "scrape fail %s", w.Label)
		}
		if w.Online {
			// KV% crossed configured threshold (KVEventPct).
			if hadPrev && prev.KVCacheUsagePct < t.KVEventPct && w.KVCacheUsagePct >= t.KVEventPct {
				m.events.Push(metrics.SeverityWarn, "KV eviction spike %s", w.Label)
			}
			// TTFT p99 crossed configured threshold (TTFTEventMs).
			if hadPrev && prev.TTFT_P99 < t.TTFTEventMs && w.TTFT_P99 >= t.TTFTEventMs {
				m.events.Push(metrics.SeverityWarn, "TTFT >%.0fs %s", t.TTFTEventMs/1000, w.Label)
			}
			// Cache hit rate drop > CacheHitDropPP percentage points.
			if hadPrev && prev.CacheHitRatePct > 0 && w.CacheHitRatePct > 0 {
				drop := prev.CacheHitRatePct - w.CacheHitRatePct
				if drop > t.CacheHitDropPP {
					m.events.Push(metrics.SeverityWarn, "cache hit rate fell %.0f→%.0f%%", prev.CacheHitRatePct, w.CacheHitRatePct)
				}
			}
		}

		m.prevWorkerStates[key] = workerSnapshot{
			Online:          w.Online,
			KVCacheUsagePct: w.KVCacheUsagePct,
			CacheHitRatePct: w.CacheHitRatePct,
			TTFT_P99:        w.TTFT_P99,
			Role:            w.Role,
		}
	}

	// DCGM scrape age warnings
	for _, g := range m.gpus {
		if !g.LastScrape.IsZero() {
			age := time.Since(g.LastScrape)
			if age > 30*time.Second {
				m.events.Push(metrics.SeverityWarn, "DCGM stale %s GPU:%d", g.Hostname, g.Index)
			}
		}
	}
}

func (m Model) renderMain() string {
	showSidebar := m.width >= MinWidthForSidebar

	// Compute main pane width
	mainWidth := m.width
	if showSidebar {
		mainWidth = m.width - SidebarWidth - 1
	}

	var sb strings.Builder

	// Header (full width, above the split). V1: header reads currentFrame
	// summary so scrubbing flips to historical numbers in the bar.
	_, summary, _ := m.currentFrame()
	header := RenderHeader(summary, "v"+m.version, m.intervalSec, m.width, m.travelOffset, m.travelAnchor)
	sb.WriteString(header)
	sb.WriteString("\n")

	// V6: pulse strip uses live fleet history, not the historical frame —
	// this gives operators a "where are we now" reference while scrubbing.
	if pulse := RenderPulseStrip(m.fleetHistorySlice(), m.width, m.height); pulse != "" {
		sb.WriteString(pulse)
		sb.WriteString("\n")
	}

	// Tab bar (full width)
	sb.WriteString(RenderTabBar(ViewMain, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n")

	// Count header lines consumed so far
	headerLines := strings.Count(sb.String(), "\n")

	// Build the main table pane content
	var mainPane strings.Builder

	// K8s context indicator
	if m.k8sContext != "" {
		k8sLine := StyleHeaderStat.Render("  K8s: ") + StyleHeaderValue.Render(m.k8sContext)
		mainPane.WriteString(k8sLine + "\n")
	}

	// Model drill-down indicator
	if m.modelFilter != "" {
		filterLine := StyleHeaderStat.Render("  Model: ") + StyleHeaderValue.Render(m.modelFilter)
		mainPane.WriteString(filterLine + "\n")
	}

	// Filter indicator
	filter := filterCycle[m.filterIdx]
	if filter != metrics.BackendUnknown {
		filterLine := StyleHeaderStat.Render("  Filter: ") + StyleHeaderValue.Render(string(filter))
		mainPane.WriteString(filterLine + "\n")
	}

	// Sort indicator
	if m.sortCol != SortNone {
		sortLine := StyleHeaderStat.Render("  Sort: ") + StyleSortIndicator.Render(SortColumnName(m.sortCol)+" ↓")
		mainPane.WriteString(sortLine + "\n")
	}

	// F4: alerts indicator when thresholds differ from production defaults.
	if m.alertsCustomized() {
		alertsLine := StyleHeaderStat.Render("  alerts: ") + StyleSortIndicator.Render("custom")
		mainPane.WriteString(alertsLine + "\n")
	}

	mainPane.WriteString("\n")

	// V3 anomaly indicator
	if m.anomalyFilterOn {
		mainPane.WriteString(StyleHeaderStat.Render("  Anomaly: ") + StyleMetricWarn.Render("ON") + "\n")
	}

	display := m.displayWorkers()
	visibleRows := m.workersVisibleRows()
	table := RenderTable(display, m.selectedIdx, m.sortCol, filter, mainWidth, visibleRows, m.viewportOffset, m.anomaly, m.bookmarks, m.k8sContext)
	mainPane.WriteString(table)

	// Available height for the content area (below header, above footer)
	contentHeight := m.height - headerLines - 2 // reserve for footer

	// Fill main pane to content height
	mainLines := strings.Count(mainPane.String(), "\n")
	for i := 0; i < contentHeight-mainLines; i++ {
		mainPane.WriteString("\n")
	}

	mainContent := mainPane.String()

	if showSidebar {
		worker := m.selectedWorker()
		var events []metrics.Event
		if m.events != nil {
			events = m.events.All()
		}
		sidebar := RenderSidebar(worker, m.gpus, events, contentHeight, m.anomaly)
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, mainContent, sidebar))
	} else {
		sb.WriteString(mainContent)
	}

	sb.WriteString("\n")
	if m.filterActive {
		sb.WriteString(m.renderFilterBar())
	} else {
		sb.WriteString(m.renderFooter())
	}

	return sb.String()
}

func (m Model) renderFilterBar() string {
	cursor := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	prompt := StyleFooterKey.Render("/") + " " + m.filterText + cursor.Render("_")
	return StyleFooter.Render("  " + prompt)
}

func (m Model) renderModelMain() string {
	var sb strings.Builder

	// Header — V1 currentFrame summary.
	_, summary, _ := m.currentFrame()
	header := RenderHeader(summary, "v"+m.version, m.intervalSec, m.width, m.travelOffset, m.travelAnchor)
	sb.WriteString(header)
	sb.WriteString("\n")
	if pulse := RenderPulseStrip(m.fleetHistorySlice(), m.width, m.height); pulse != "" {
		sb.WriteString(pulse)
		sb.WriteString("\n")
	}

	// Tab bar
	sb.WriteString(RenderTabBar(ViewModelGroup, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	// Tenant card view
	sb.WriteString(RenderTenantsView(m.modelGroups, m.width))

	// Fill remaining space
	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for range remaining {
		sb.WriteString("\n")
	}

	// Footer
	sb.WriteString(m.renderFooter())

	return sb.String()
}

func (m Model) renderKVCacheMain() string {
	var sb strings.Builder

	workers, summary, _ := m.currentFrame()
	header := RenderHeader(summary, "v"+m.version, m.intervalSec, m.width, m.travelOffset, m.travelAnchor)
	sb.WriteString(header)
	sb.WriteString("\n")
	if pulse := RenderPulseStrip(m.fleetHistorySlice(), m.width, m.height); pulse != "" {
		sb.WriteString(pulse)
		sb.WriteString("\n")
	}

	sb.WriteString(RenderTabBar(ViewKVCache, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	sb.WriteString(RenderKVCacheView(workers, m.width))

	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for range remaining {
		sb.WriteString("\n")
	}

	sb.WriteString(m.renderFooter())
	return sb.String()
}

func (m Model) renderPDPoolsMain() string {
	var sb strings.Builder

	workers, summary, _ := m.currentFrame()
	header := RenderHeader(summary, "v"+m.version, m.intervalSec, m.width, m.travelOffset, m.travelAnchor)
	sb.WriteString(header)
	sb.WriteString("\n")
	if pulse := RenderPulseStrip(m.fleetHistorySlice(), m.width, m.height); pulse != "" {
		sb.WriteString(pulse)
		sb.WriteString("\n")
	}

	sb.WriteString(RenderTabBar(ViewPDPools, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	sb.WriteString(RenderPDPoolsView(workers, m.width))

	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for range remaining {
		sb.WriteString("\n")
	}

	sb.WriteString(m.renderFooter())
	return sb.String()
}

func (m Model) renderAlertsMain() string {
	var sb strings.Builder

	// V1: when scrubbing, show the historical alert state at that tick;
	// counts in the header reflect snapshot data, not live AlertManager.
	_, _, frameAlerts := m.currentFrame()
	var crit, warn, info int
	if m.travelOffset > 0 {
		for _, a := range frameAlerts {
			if a.ResolvedAt != nil {
				continue
			}
			switch a.Severity {
			case metrics.AlertCritical:
				crit++
			case metrics.AlertWarning:
				warn++
			case metrics.AlertInfo:
				info++
			}
		}
	} else if m.alertMgr != nil {
		info, warn, crit = m.alertMgr.Counts()
	}
	header := RenderAlertsHeader(crit, warn, info, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")
	if pulse := RenderPulseStrip(m.fleetHistorySlice(), m.width, m.height); pulse != "" {
		sb.WriteString(pulse)
		sb.WriteString("\n")
	}

	sb.WriteString(RenderTabBar(ViewAlerts, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	var alerts []*metrics.Alert
	if m.travelOffset > 0 {
		alerts = make([]*metrics.Alert, len(frameAlerts))
		for i := range frameAlerts {
			alerts[i] = &frameAlerts[i]
		}
	} else if m.alertMgr != nil {
		alerts = m.alertMgr.All()
	}
	sb.WriteString(RenderAlertsList(alerts, m.alertSelectedIdx, m.width))

	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for range remaining {
		sb.WriteString("\n")
	}

	sb.WriteString(m.renderFooter())
	return sb.String()
}

func (m Model) renderOverlay() string {
	worker := m.selectedWorker()
	return RenderDetailOverlay(worker, m.gpus, m.width, m.height)
}

func (m Model) renderFooter() string {
	// V12: full-form footer for the first 5 ticks of demo mode.
	if m.demoMode && m.demoHintTicks > 0 {
		hint := fmt.Sprintf("DEMO mode — [S] scenario: %s   [?] help   [q] quit   (running --demo; press S to switch scenario)", m.demoScenario)
		return StyleFooter.Render("  " + hint)
	}
	if m.demoMode {
		hint := fmt.Sprintf("DEMO  [S] scenario: %s  [?] help  [q] quit", m.demoScenario)
		return StyleFooter.Render("  " + hint)
	}
	if m.footerStatus != "" {
		return StyleFooter.Render("  " + StyleMetricWarn.Render(m.footerStatus))
	}

	var keys []struct{ key, desc string }

	switch m.currentView {
	case ViewGPU:
		keys = []struct{ key, desc string }{
			{"q", "quit"},
			{"g", "workers"},
			{"s", "sort"},
			{"d", "details"},
			{"r", "refresh"},
			{"e", "export"},
			{"?", "help"},
		}
	case ViewModelGroup:
		keys = []struct{ key, desc string }{
			{"q", "quit"},
			{"m", "workers"},
			{"s", "sort"},
			{"d", "expand"},
			{"r", "refresh"},
			{"e", "export"},
			{"?", "help"},
		}
	default:
		keys = []struct{ key, desc string }{
			{"q", "quit"},
			{"m", "models"},
			{"s", "sort"},
			{"f", "filter"},
			{"d", "details"},
			{"r", "refresh"},
			{"e", "export"},
			{"?", "help"},
		}
		if m.dcgmCollector != nil {
			keys = append(keys[:1], append([]struct{ key, desc string }{{"g", "gpus"}}, keys[1:]...)...)
		}
	}

	var parts []string
	for _, k := range keys {
		part := "[" + StyleFooterKey.Render(k.key) + "] " + k.desc
		parts = append(parts, part)
	}

	footer := "  " + strings.Join(parts, "   ")

	// V1: scrub footer suffix.
	if m.travelOffset > 0 {
		footer += "   " + StyleHeaderAmber.Render("[[] back  []] fwd  [\\] live  [t] anchor")
	}

	// F5: scroll indicator in the bottom-right of the Workers tab footer
	// when not all rows are visible. We compute against the live row list
	// rather than the cached lengths so it stays accurate after sorts.
	if m.currentView == ViewMain {
		if ind := m.scrollIndicator(); ind != "" {
			footer = footer + "   " + ind
		}
	}

	return StyleFooter.Render(footer)
}

// scrollIndicator returns a "[start-end/total]" indicator string when the
// Workers tab viewport doesn't show every row, or an empty string when all
// rows fit. Indices are 1-based row numbers in the display row list.
func (m Model) scrollIndicator() string {
	rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
	total := len(rows)
	visible := m.workersVisibleRows()
	if visible <= 0 || total <= visible {
		return ""
	}
	start, end := ClampViewport(total, visible, m.viewportOffset)
	// Display 1-indexed inclusive range.
	return fmt.Sprintf("[%d-%d/%d]", start+1, end, total)
}

func (m Model) renderDetail() string {
	if m.selectedIdx >= len(m.workers) {
		return "No worker selected"
	}
	w := m.workers[m.selectedIdx]

	var sb strings.Builder
	sb.WriteString(StyleDetailTitle.Render("Worker Detail") + "\n\n")

	// Endpoint info
	sb.WriteString(StyleDetailSection.Render("Endpoint") + "\n")
	sb.WriteString(renderDetailRow("URL", w.Endpoint))
	if w.Label != "" {
		sb.WriteString(renderDetailRow("Label", w.Label))
	}
	sb.WriteString(renderDetailRow("Backend", string(w.Backend)))
	sb.WriteString(renderDetailRow("Model", orDash(w.ModelName)))
	role := w.Role
	if role == "" {
		role = "mono"
	}
	sb.WriteString(renderDetailRow("Role", role))
	status := "● Online"
	if !w.Online {
		status = "○ Offline"
	}
	sb.WriteString(renderDetailRow("Status", status))
	sb.WriteString(renderDetailRow("Last Seen", w.LastSeen.Format("15:04:05")))
	sb.WriteString("\n")

	// Load metrics
	sb.WriteString(StyleDetailSection.Render("Load") + "\n")
	sb.WriteString(renderDetailRow("Requests Running", fmt.Sprintf("%d", w.RequestsRunning)))
	sb.WriteString(renderDetailRow("Requests Waiting", fmt.Sprintf("%d", w.RequestsWaiting)))
	sb.WriteString("\n")

	// Cache metrics
	sb.WriteString(StyleDetailSection.Render("KV Cache") + "\n")
	sb.WriteString(renderDetailRow("KV Cache GPU", fmt.Sprintf("%.1f%%", w.KVCacheUsagePct)))
	if w.KVCacheUsageCPUPct > 0 {
		sb.WriteString(renderDetailRow("KV Cache CPU", fmt.Sprintf("%.1f%%", w.KVCacheUsageCPUPct)))
	}
	if w.KVCacheUsageNVMePct > 0 {
		sb.WriteString(renderDetailRow("KV Cache NVMe", fmt.Sprintf("%.1f%%", w.KVCacheUsageNVMePct)))
	}
	sb.WriteString(renderDetailRow("Cache Hit Rate", fmt.Sprintf("%.1f%%", w.CacheHitRatePct)))
	if w.StoreSizeBytes > 0 {
		sb.WriteString(renderDetailRow("Store Size", formatBytes(w.StoreSizeBytes)))
	}
	sb.WriteString("\n")

	// Latency metrics — F2 multi-percentile snapshot
	sb.WriteString(StyleDetailSection.Render("Latency") + "\n")
	sb.WriteString(renderDetailRow("TTFT P50", formatLatencyMs(w.TTFT.P50)))
	sb.WriteString(renderDetailRow("TTFT P95", formatLatencyMs(w.TTFT.P95)))
	sb.WriteString(renderDetailRow("TTFT P99", formatLatencyMs(w.TTFT.P99)))
	if w.TTFT.P99 > 0 && m.width >= 80 {
		sb.WriteString("  " + renderCDFSparkline(w.TTFT.CDFPoints) + "\n")
	}
	sb.WriteString(renderDetailRow("ITL P50", formatLatencyMs(w.ITL.P50)))
	sb.WriteString(renderDetailRow("ITL P95", formatLatencyMs(w.ITL.P95)))
	sb.WriteString(renderDetailRow("ITL P99", formatLatencyMs(w.ITL.P99)))
	if w.ITL.P99 > 0 && m.width >= 80 {
		sb.WriteString("  " + renderCDFSparkline(w.ITL.CDFPoints) + "\n")
	}
	sb.WriteString("\n")

	// Throughput metrics
	sb.WriteString(StyleDetailSection.Render("Throughput") + "\n")
	sb.WriteString(renderDetailRow("Prompt Tokens/s", fmt.Sprintf("%.1f", w.PromptTokPerSec)))
	sb.WriteString(renderDetailRow("Generation Tokens/s", fmt.Sprintf("%.1f", w.GenTokPerSec)))
	sb.WriteString("\n")

	// TTFT sparkline history
	if len(w.TTFTHistory) > 1 {
		sb.WriteString(StyleDetailSection.Render("TTFT History (P99 ms)") + "\n")
		sb.WriteString("  " + renderSparkline(w.TTFTHistory) + "\n\n")
	}

	// GenTok sparkline
	if len(w.GenTokHistory) > 1 {
		sb.WriteString(StyleDetailSection.Render("Gen Tokens/s History") + "\n")
		sb.WriteString("  " + renderSparkline(w.GenTokHistory) + "\n\n")
	}

	sb.WriteString(StyleFooter.Render("Press any key to return"))

	content := sb.String()

	return lipgloss.NewStyle().
		Padding(1, 2).
		Width(m.width).
		Render(content)
}

func (m Model) renderHelp() string {
	shortcuts := []struct{ key, desc string }{
		{"q / ctrl+c", "Quit"},
		{"↑ / ↓ (or k/j)", "Navigate rows"},
		{"PgUp / PgDn", "Page up/down through workers (or ctrl+b / ctrl+f)"},
		{"Home / End", "Jump to first / last worker"},
		{"s", "Cycle sort column"},
		{"f", "Cycle filter by backend (All, vLLM, SGLang, LMCache, NIM)"},
		{"d", "Open detail view / expand model to workers"},
		{"m", "Toggle model-grouped view; drill-down clears filter and returns to model view"},
		{"g", "Toggle between worker and GPU views"},
		{"r", "Force immediate refresh"},
		{"e", "Export current snapshot to JSON file"},
		{"?", "Show this help"},
	}

	var sb strings.Builder
	sb.WriteString(StyleHelpTitle.Render("llmtop — Keyboard Shortcuts") + "\n\n")

	for _, s := range shortcuts {
		sb.WriteString("  " + StyleHelpKey.Render(fmt.Sprintf("%-22s", s.key)) +
			StyleHelpDesc.Render(s.desc) + "\n")
	}

	sb.WriteString("\n" + StyleFooter.Render("Press any key to close"))

	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center,
		lipgloss.PlaceVertical(m.height, lipgloss.Center,
			StyleHelpOverlay.Render(sb.String()),
		),
	)
}

func renderDetailRow(label, value string) string {
	return "  " + StyleDetailLabel.Render(fmt.Sprintf("%-22s", label)) +
		StyleDetailValue.Render(value) + "\n"
}

func renderSparkline(data []float64) string {
	// Unicode block chars for sparkline
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

	if len(data) == 0 {
		return ""
	}

	var min, max float64
	min = data[0]
	max = data[0]
	for _, v := range data {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	r := max - min
	var spark strings.Builder
	for _, v := range data {
		var idx int
		if r > 0 {
			idx = int((v - min) / r * float64(len(blocks)-1))
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		spark.WriteRune(blocks[idx])
	}
	return StyleMetricGood.Render(spark.String())
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func formatBytes(b float64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%.0f B", b)
	}
	div, exp := float64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", b/div, "KMGTPE"[exp])
}
