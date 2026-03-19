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
	"github.com/InfraWhisperer/llmtop/pkg/plugins"
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

// ViewConfirm is the confirmation dialog view for dangerous plugin commands.
const ViewConfirm View = 10

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

	// Plugin system
	plugins        []plugins.Plugin
	readonly       bool
	bgPluginStatus string           // status text for running background plugin
	confirmPlugin  *plugins.Plugin  // plugin pending confirmation
	confirmCmd     string           // resolved command string for confirmation display
	confirmEnv     map[string]string // resolved env for confirmed execution
	prevView       View             // view to return to after confirmation
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
func NewModel(c collector.MetricsSource, dc collector.GPUSource, version string, intervalSec int, k8sContext string, pluginList []plugins.Plugin, readonly bool) Model {
	return Model{
		collector:     c,
		dcgmCollector: dc,
		version:       version,
		intervalSec:   intervalSec,
		k8sContext:    k8sContext,
		plugins:       pluginList,
		readonly:      readonly,
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
		return m, nil

	case exportDoneMsg:
		return m, nil

	case pluginDoneMsg:
		m.bgPluginStatus = ""
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
	case ViewConfirm:
		switch msg.String() {
		case "y":
			if m.confirmPlugin != nil {
				p := *m.confirmPlugin
				args := plugins.InterpolateArgs(p.Args, m.confirmEnv)
				m.confirmPlugin = nil
				m.confirmCmd = ""
				m.confirmEnv = nil
				m.currentView = m.prevView
				if p.Background {
					m.bgPluginStatus = "Running: " + p.Name + "..."
					return m, runPluginBackground(p.Name, p.Command, args)
				}
				return m, runPluginForeground(p.Name, p.Command, args)
			}
			m.currentView = m.prevView
			return m, nil
		default:
			m.confirmPlugin = nil
			m.confirmCmd = ""
			m.confirmEnv = nil
			m.currentView = m.prevView
			return m, nil
		}

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
	}

	// Main view
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		if m.selectedIdx > 0 {
			m.selectedIdx--
		}

	case "down", "j":
		if m.selectedIdx < len(m.workers)-1 {
			m.selectedIdx++
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

	case "f":
		// Cycle filter
		m.filterIdx = (m.filterIdx + 1) % len(filterCycle)

	case "d":
		if len(m.workers) > 0 {
			m.currentView = ViewDetail
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

	default:
		return m.tryPluginKey(msg.String(), "worker")
	}

	return m, nil
}

func (m Model) handleModelGroupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	default:
		return m.tryPluginKey(msg.String(), "model")
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
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "g":
		m.currentView = ViewMain

	case "up", "k":
		if m.gpuSelectedIdx > 0 {
			m.gpuSelectedIdx--
		}

	case "down", "j":
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

	default:
		return m.tryPluginKey(msg.String(), "gpu")
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
	case ViewConfirm:
		return m.renderConfirm()
	}

	return m.renderMain()
}

func (m Model) renderMain() string {
	var sb strings.Builder

	// Header
	header := RenderHeader(m.summary, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

	// K8s context indicator
	if m.k8sContext != "" {
		k8sLine := StyleHeaderStat.Render("  K8s: ") + StyleHeaderValue.Render(m.k8sContext)
		sb.WriteString(k8sLine + "\n")
	}

	// Model drill-down indicator
	if m.modelFilter != "" {
		filterLine := StyleHeaderStat.Render("  Model: ") + StyleHeaderValue.Render(m.modelFilter)
		sb.WriteString(filterLine + "\n")
	}

	// Filter indicator
	filter := filterCycle[m.filterIdx]
	if filter != metrics.BackendUnknown {
		filterLine := StyleHeaderStat.Render("  Filter: ") + StyleHeaderValue.Render(string(filter))
		sb.WriteString(filterLine + "\n")
	}

	// Sort indicator
	if m.sortCol != SortNone {
		sortLine := StyleHeaderStat.Render("  Sort: ") + StyleSortIndicator.Render(SortColumnName(m.sortCol)+" ↓")
		sb.WriteString(sortLine + "\n")
	}

	// Background plugin status
	if m.bgPluginStatus != "" {
		sb.WriteString(StyleHeaderStat.Render("  ") + StyleMetricWarn.Render(m.bgPluginStatus) + "\n")
	}

	sb.WriteString("\n")

	// Build the worker slice to display — apply model drill-down filter.
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

	// Table
	table := RenderTable(display, m.selectedIdx, m.sortCol, filter, m.width)
	sb.WriteString(table)

	// Fill remaining space
	lines := strings.Count(sb.String(), "\n")
	remaining := m.height - lines - 3 // reserve for footer
	for i := 0; i < remaining; i++ {
		sb.WriteString("\n")
	}

	// Footer
	sb.WriteString(m.renderFooter())

	return sb.String()
}

func (m Model) renderModelMain() string {
	var sb strings.Builder

	// Header
	header := RenderModelHeader(m.modelGroups, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

	// Sort indicator
	if m.modelSortCol != ModelSortNone {
		sortLine := StyleHeaderStat.Render("  Sort: ") + StyleSortIndicator.Render(ModelSortColumnName(m.modelSortCol)+" ↓")
		sb.WriteString(sortLine + "\n")
	}

	sb.WriteString("\n")

	// Table
	table := RenderModelTable(m.modelGroups, m.modelSelectedIdx, m.modelSortCol, m.width)
	sb.WriteString(table)

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
	sb.WriteString(renderDetailRow("KV Cache Usage", fmt.Sprintf("%.1f%%", w.KVCacheUsagePct)))
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

	// Show plugins for the current view scope
	scope := viewScope(m.currentView)
	if scope == "" {
		scope = "worker" // help is reachable from main view
	}
	scopePlugins := plugins.ForScope(m.plugins, scope)
	if len(scopePlugins) > 0 {
		sb.WriteString("\n" + StyleHelpTitle.Render("Plugins ("+scope+" scope)") + "\n\n")
		for _, p := range scopePlugins {
			desc := p.Description
			if p.Dangerous {
				desc += " ⚠"
			}
			if m.readonly && p.Dangerous {
				desc += " (disabled: readonly)"
			}
			sb.WriteString("  " + StyleHelpKey.Render(fmt.Sprintf("%-22s", p.ShortCut)) +
				StyleHelpDesc.Render(desc) + "\n")
		}
	}

	sb.WriteString("\n" + StyleFooter.Render("Press any key to close"))

	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center,
		lipgloss.PlaceVertical(m.height, lipgloss.Center,
			StyleHelpOverlay.Render(sb.String()),
		),
	)
}

// tryPluginKey checks if a key press matches a plugin shortcut for the given
// scope and dispatches the plugin command. Returns the model unchanged if no
// plugin matches.
func (m Model) tryPluginKey(key string, scope string) (tea.Model, tea.Cmd) {
	for i := range m.plugins {
		p := &m.plugins[i]
		if plugins.ShortcutKey(*p) != key {
			continue
		}
		// Check scope match
		matched := false
		for _, s := range p.Scopes {
			if s == scope {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// Dangerous plugins disabled in readonly mode
		if p.Dangerous && m.readonly {
			return m, nil
		}

		// Build environment from currently selected resource
		env := m.pluginEnv(scope)

		// Confirmation required — show dialog
		if p.Confirm {
			m.confirmPlugin = p
			m.confirmCmd = resolvePluginCmd(*p, env)
			m.confirmEnv = env
			m.prevView = m.currentView
			m.currentView = ViewConfirm
			return m, nil
		}

		// Execute directly
		args := plugins.InterpolateArgs(p.Args, env)
		if p.Background {
			m.bgPluginStatus = "Running: " + p.Name + "..."
			return m, runPluginBackground(p.Name, p.Command, args)
		}
		return m, runPluginForeground(p.Name, p.Command, args)
	}
	return m, nil
}

func (m Model) renderConfirm() string {
	var sb strings.Builder
	sb.WriteString(StyleHelpTitle.Render("Confirm Plugin Execution") + "\n\n")

	if m.confirmPlugin != nil {
		sb.WriteString("  " + StyleDetailLabel.Render("Plugin:  ") + StyleDetailValue.Render(m.confirmPlugin.Name) + "\n")
		sb.WriteString("  " + StyleDetailLabel.Render("Command: ") + StyleDetailValue.Render(m.confirmCmd) + "\n")
		if m.confirmPlugin.Dangerous {
			sb.WriteString("\n  " + StyleMetricBad.Render("⚠ This plugin is marked as dangerous") + "\n")
		}
	}

	sb.WriteString("\n  Press " + StyleFooterKey.Render("y") + " to execute, any other key to cancel")

	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center,
		lipgloss.PlaceVertical(m.height, lipgloss.Center,
			StyleHelpOverlay.Render(sb.String()),
		),
	)
}

// pluginEnv returns the variable environment for the currently selected
// resource in the given scope.
func (m Model) pluginEnv(scope string) map[string]string {
	switch scope {
	case "worker":
		if m.selectedIdx < len(m.workers) {
			return workerEnv(m.workers[m.selectedIdx], m.k8sContext)
		}
	case "gpu":
		if m.gpuSelectedIdx < len(m.gpus) {
			return gpuEnv(m.gpus[m.gpuSelectedIdx], m.k8sContext)
		}
	case "model":
		if m.modelSelectedIdx < len(m.modelGroups) {
			return modelEnv(m.modelGroups[m.modelSelectedIdx], m.k8sContext)
		}
	}
	return map[string]string{}
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
