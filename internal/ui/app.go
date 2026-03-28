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

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/InfraWhisperer/llmtop/internal/collector"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// View represents the current UI view mode.
type View int

const (
	ViewMain       View = iota
	ViewDetail
	ViewHelp
	ViewGPU
	ViewGPUDetail
	ViewModelGroup
	ViewKVCache
	ViewPDPools
	ViewAlerts
	ViewOverlay // detail overlay on top of current view
)

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
	GPUSortNone  GPUSortColumn = iota
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
func NewModel(c collector.MetricsSource, dc collector.GPUSource, version string, intervalSec int, k8sContext string) Model {
	return Model{
		collector:        c,
		dcgmCollector:    dc,
		version:          version,
		intervalSec:      intervalSec,
		k8sContext:       k8sContext,
		events:           metrics.NewEventRing(20),
		prevWorkerStates: make(map[string]workerSnapshot),
		alertMgr:         metrics.NewAlertManager(),
	}
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
		// Generate events by comparing with previous state
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
			Summary     metrics.FleetSummary    `json:"summary"`
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
		err = os.WriteFile(filename, data, 0o644)
		return exportDoneMsg{filename: filename, err: err}
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		return m, nil
	}

	// Tab shortcuts available from main view
	if handled, cmd := m.handleTabSwitch(msg); handled {
		return m, cmd
	}

	// Main view
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
		m.selectedIdx = NextDataRow(rows, m.selectedIdx, -1)

	case "down", "j":
		rows := BuildTableRows(m.displayWorkers(), filterCycle[m.filterIdx])
		m.selectedIdx = NextDataRow(rows, m.selectedIdx, +1)

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

	case "f":
		// Cycle filter
		m.filterIdx = (m.filterIdx + 1) % len(filterCycle)

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
		// Jump to the source worker in Workers tab
		alerts := m.alertMgr.All()
		if m.alertSelectedIdx < len(alerts) {
			a := alerts[m.alertSelectedIdx]
			for i, w := range m.workers {
				if w.Label == a.Source || strings.Contains(w.Label, a.Source) {
					m.selectedIdx = i
					m.currentView = ViewMain
					return m, nil
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
	}

	return m.renderMain()
}

// displayWorkers returns the worker slice to display, applying model drill-down and text filters.
func (m Model) displayWorkers() []*metrics.WorkerMetrics {
	var display []*metrics.WorkerMetrics
	if m.modelFilter != "" {
		for _, w := range m.workers {
			name := w.ModelName
			if name == "" {
				name = "Unknown"
			}
			if name == m.modelFilter {
				display = append(display, w)
			}
		}
	} else {
		display = m.workers
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
		return filtered
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

// detectEvents compares current worker state against previous snapshot and
// pushes events into the ring buffer when thresholds are crossed.
func (m *Model) detectEvents() {
	if m.events == nil {
		return
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
			// KV% crossed 75% threshold
			if hadPrev && prev.KVCacheUsagePct < 75 && w.KVCacheUsagePct >= 75 {
				m.events.Push(metrics.SeverityWarn, "KV eviction spike %s", w.Label)
			}
			// TTFT p99 crossed 5s
			if hadPrev && prev.TTFT_P99 < 5000 && w.TTFT_P99 >= 5000 {
				m.events.Push(metrics.SeverityWarn, "TTFT >5s %s", w.Label)
			}
			// Cache hit rate drop >10pp
			if hadPrev && prev.CacheHitRatePct > 0 && w.CacheHitRatePct > 0 {
				drop := prev.CacheHitRatePct - w.CacheHitRatePct
				if drop > 10 {
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

	// Header (full width, above the split)
	header := RenderHeader(m.summary, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

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

	mainPane.WriteString("\n")

	display := m.displayWorkers()
	table := RenderTable(display, m.selectedIdx, m.sortCol, filter, mainWidth)
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
		sidebar := RenderSidebar(worker, m.gpus, events, contentHeight)
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

	// Header
	header := RenderHeader(m.summary, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

	// Tab bar
	sb.WriteString(RenderTabBar(ViewModelGroup, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	// Tenant card view
	sb.WriteString(RenderTenantsView(m.modelGroups, m.width))

	// Fill remaining space
	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for i := 0; i < remaining; i++ {
		sb.WriteString("\n")
	}

	// Footer
	sb.WriteString(m.renderFooter())

	return sb.String()
}

func (m Model) renderKVCacheMain() string {
	var sb strings.Builder

	header := RenderHeader(m.summary, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

	sb.WriteString(RenderTabBar(ViewKVCache, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	sb.WriteString(RenderKVCacheView(m.workers, m.width))

	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for i := 0; i < remaining; i++ {
		sb.WriteString("\n")
	}

	sb.WriteString(m.renderFooter())
	return sb.String()
}

func (m Model) renderPDPoolsMain() string {
	var sb strings.Builder

	header := RenderHeader(m.summary, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

	sb.WriteString(RenderTabBar(ViewPDPools, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	sb.WriteString(RenderPDPoolsView(m.workers, m.width))

	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for i := 0; i < remaining; i++ {
		sb.WriteString("\n")
	}

	sb.WriteString(m.renderFooter())
	return sb.String()
}

func (m Model) renderAlertsMain() string {
	var sb strings.Builder

	crit, warn, info := m.alertMgr.Counts()
	header := RenderAlertsHeader(crit, warn, info, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

	sb.WriteString(RenderTabBar(ViewAlerts, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n\n")

	alerts := m.alertMgr.All()
	sb.WriteString(RenderAlertsList(alerts, m.alertSelectedIdx, m.width))

	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3
	for i := 0; i < remaining; i++ {
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
	return StyleFooter.Render(footer)
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
	sb.WriteString(renderDetailRow("Cache Hit Rate", fmt.Sprintf("%.1f%%", w.CacheHitRatePct)))
	if w.StoreSizeBytes > 0 {
		sb.WriteString(renderDetailRow("Store Size", formatBytes(w.StoreSizeBytes)))
	}
	sb.WriteString("\n")

	// Latency metrics
	sb.WriteString(StyleDetailSection.Render("Latency") + "\n")
	sb.WriteString(renderDetailRow("TTFT P50", fmt.Sprintf("%.1fms", w.TTFT_P50)))
	sb.WriteString(renderDetailRow("TTFT P99", fmt.Sprintf("%.1fms", w.TTFT_P99)))
	sb.WriteString(renderDetailRow("ITL P50", fmt.Sprintf("%.1fms", w.ITL_P50)))
	sb.WriteString(renderDetailRow("ITL P99", fmt.Sprintf("%.1fms", w.ITL_P99)))
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
