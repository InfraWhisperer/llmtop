package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// GPU table column widths
const (
	colGPUIdx   = 16
	colGPUName  = 26
	colGPUUtil  = 7
	colGPUVRAM  = 16
	colGPUTemp  = 7
	colGPUPower = 8
	colGPUPod   = 38
)

// GPUSortColumnName returns a human-readable name for the GPU sort column.
func GPUSortColumnName(s GPUSortColumn) string {
	switch s {
	case GPUSortUtil:
		return "Util%"
	case GPUSortVRAM:
		return "VRAM%"
	case GPUSortTemp:
		return "Temp"
	case GPUSortPower:
		return "Power"
	default:
		return "—"
	}
}

// RenderGPUHeader renders the GPU fleet summary header bar.
func RenderGPUHeader(summary metrics.GPUSummary, version string, intervalSec int, width int) string {
	title := StyleHeaderTitle.Render("llmtop " + version + " — GPU")

	gpuCount := StyleHeaderStat.Render(
		fmt.Sprintf("%d GPUs (%d active)", summary.TotalGPUs, summary.ActiveGPUs),
	)

	avgUtil := StyleHeaderValue.Render(fmt.Sprintf("%.0f%%", summary.AvgUtilPct))
	avgUtilLabel := StyleHeaderStat.Render("avg util")

	var memPct float64
	if summary.TotalMemCap > 0 {
		memPct = (summary.TotalMemUsed / summary.TotalMemCap) * 100
	}
	memUsed := StyleHeaderValue.Render(fmt.Sprintf("%.0f/%.0f GiB", summary.TotalMemUsed/1024, summary.TotalMemCap/1024))
	memLabel := StyleHeaderStat.Render("VRAM")
	memPctStr := StyleHeaderValue.Render(fmt.Sprintf("(%.0f%%)", memPct))

	interval := StyleHeaderStat.Render(fmt.Sprintf("↻ %ds", intervalSec))

	dot := StyleHeaderDot.Render("·")

	parts := []string{
		" " + title,
		dot,
		gpuCount,
		dot,
		avgUtilLabel + " " + avgUtil,
		dot,
		memLabel + " " + memUsed + " " + memPctStr,
		dot,
		interval + " ",
	}

	header := ""
	for _, p := range parts {
		header += p + " "
	}

	return lipgloss.NewStyle().
		Width(width).
		Background(colorDark).
		Foreground(colorWhite).
		Render(header)
}

// RenderGPUTable renders the GPU metrics table.
func RenderGPUTable(gpus []*metrics.GPUInfo, selectedIdx int, width int) string {
	var sb strings.Builder

	// Header row
	header := renderGPUTableHeader()
	sb.WriteString(header)
	sb.WriteString("\n")

	// Separator
	sep := StyleTableSeparator.Render(strings.Repeat("─", max(width-2, 80)))
	sb.WriteString("  " + sep)
	sb.WriteString("\n")

	// Rows
	for i, g := range gpus {
		row := renderGPURow(g, i == selectedIdx)
		sb.WriteString(row)
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderGPUTableHeader() string {
	cols := []struct {
		name  string
		width int
	}{
		{"GPU#", colGPUIdx},
		{"NAME", colGPUName},
		{"UTIL%", colGPUUtil},
		{"VRAM GiB", colGPUVRAM},
		{"TEMP", colGPUTemp},
		{"POWER", colGPUPower},
		{"POD", colGPUPod},
	}

	var parts []string
	for _, c := range cols {
		parts = append(parts, StyleTableHeader.Render(padRight(c.name, c.width)))
	}
	return "  " + strings.Join(parts, " ")
}

func renderGPURow(g *metrics.GPUInfo, selected bool) string {
	// GPU index with hostname for multi-node disambiguation
	idxLabel := fmt.Sprintf("%d", g.Index)
	if g.Hostname != "" {
		idxLabel = g.Hostname + "/" + idxLabel
	}
	idxStr := padRight(truncate(idxLabel, colGPUIdx), colGPUIdx)
	nameStr := padRight(truncate(g.Name, colGPUName), colGPUName)
	utilPlain := padRight(fmt.Sprintf("%.0f%%", g.UtilPct), colGPUUtil)

	var vramPct float64
	if g.MemTotalMB > 0 {
		vramPct = (g.MemUsedMB / g.MemTotalMB) * 100
	}
	vramPlain := padRight(truncate(fmt.Sprintf("%.1f/%.1f GiB", g.MemUsedMB/1024, g.MemTotalMB/1024), colGPUVRAM), colGPUVRAM)
	tempPlain := padRight(fmt.Sprintf("%.0f°C", g.TempC), colGPUTemp)
	powerPlain := padRight(fmt.Sprintf("%.0fW", g.PowerW), colGPUPower)
	podStr := padRight(truncate(gpuPodLabel(g), colGPUPod), colGPUPod)

	if selected {
		plain := "  " + idxStr + " " + nameStr + " " +
			utilPlain + " " + vramPlain + " " +
			tempPlain + " " + powerPlain + " " + podStr
		return StyleTableRowSelected.Render(plain)
	}

	return "  " + idxStr + " " + nameStr + " " +
		GPUUtilStyle(g.UtilPct).Render(utilPlain) + " " +
		VRAMStyle(vramPct).Render(vramPlain) + " " +
		GPUTempStyle(g.TempC).Render(tempPlain) + " " +
		StyleMetricGood.Render(powerPlain) + " " + podStr
}

func gpuPodLabel(g *metrics.GPUInfo) string {
	if g.Pod == "" {
		return "-"
	}
	if g.Namespace != "" {
		return g.Namespace + "/" + g.Pod
	}
	return g.Pod
}

func (m Model) renderGPUMain() string {
	var sb strings.Builder

	// Header
	header := RenderGPUHeader(m.gpuSummary, "v"+m.version, m.intervalSec, m.width)
	sb.WriteString(header)
	sb.WriteString("\n")

	// Tab bar
	sb.WriteString(RenderTabBar(ViewGPU, m.dcgmCollector != nil, m.width))
	sb.WriteString("\n")

	// Sort indicator
	if m.gpuSortCol != GPUSortNone {
		sortLine := StyleHeaderStat.Render("  Sort: ") + StyleSortIndicator.Render(GPUSortColumnName(m.gpuSortCol)+" ↓")
		sb.WriteString(sortLine + "\n")
	}

	sb.WriteString("\n")

	// Table
	table := RenderGPUTable(m.gpus, m.gpuSelectedIdx, m.width)
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

func (m Model) renderGPUDetail() string {
	if m.gpuSelectedIdx >= len(m.gpus) {
		return "No GPU selected"
	}
	g := m.gpus[m.gpuSelectedIdx]

	var sb strings.Builder
	sb.WriteString(StyleDetailTitle.Render("GPU Detail") + "\n\n")

	// Identity
	sb.WriteString(StyleDetailSection.Render("Identity") + "\n")
	sb.WriteString(renderDetailRow("Index", fmt.Sprintf("%d", g.Index)))
	sb.WriteString(renderDetailRow("Name", orDash(g.Name)))
	sb.WriteString(renderDetailRow("UUID", orDash(g.UUID)))
	sb.WriteString(renderDetailRow("Hostname", orDash(g.Hostname)))
	sb.WriteString("\n")

	// Compute
	sb.WriteString(StyleDetailSection.Render("Compute") + "\n")
	sb.WriteString(renderDetailRow("Utilization", fmt.Sprintf("%.1f%%", g.UtilPct)))
	sb.WriteString(renderDetailRow("SM Clock", fmt.Sprintf("%.0f MHz", g.SMClockMHz)))
	sb.WriteString(renderDetailRow("Mem Clock", fmt.Sprintf("%.0f MHz", g.MemClockMHz)))
	sb.WriteString("\n")

	// Memory
	sb.WriteString(StyleDetailSection.Render("Memory") + "\n")
	sb.WriteString(renderDetailRow("VRAM Used", fmt.Sprintf("%.0f MiB", g.MemUsedMB)))
	sb.WriteString(renderDetailRow("VRAM Total", fmt.Sprintf("%.0f MiB", g.MemTotalMB)))
	var vramPct float64
	if g.MemTotalMB > 0 {
		vramPct = (g.MemUsedMB / g.MemTotalMB) * 100
	}
	sb.WriteString(renderDetailRow("VRAM Usage", fmt.Sprintf("%.1f%%", vramPct)))
	sb.WriteString("\n")

	// Thermals
	sb.WriteString(StyleDetailSection.Render("Thermals") + "\n")
	sb.WriteString(renderDetailRow("Temperature", fmt.Sprintf("%.0f°C", g.TempC)))
	sb.WriteString(renderDetailRow("Power Draw", fmt.Sprintf("%.0f W", g.PowerW)))
	sb.WriteString("\n")

	// Pod binding
	sb.WriteString(StyleDetailSection.Render("Pod Binding") + "\n")
	sb.WriteString(renderDetailRow("Pod", orDash(g.Pod)))
	sb.WriteString(renderDetailRow("Namespace", orDash(g.Namespace)))
	sb.WriteString(renderDetailRow("Container", orDash(g.Container)))
	sb.WriteString("\n")

	// Utilization history sparkline
	if len(g.UtilHistory) > 1 {
		sb.WriteString(StyleDetailSection.Render("Utilization History (%)") + "\n")
		sb.WriteString("  " + renderSparkline(g.UtilHistory) + "\n\n")
	}

	sb.WriteString(StyleFooter.Render("Press any key to return"))

	content := sb.String()

	return lipgloss.NewStyle().
		Padding(1, 2).
		Width(m.width).
		Render(content)
}
