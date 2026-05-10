package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
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
	title := StyleHeaderTitle.Render(" llmtop " + version + " — GPU")

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

	dot := StyleHeaderDot.Render(" · ")

	parts := []string{
		title, dot, gpuCount, dot,
		avgUtilLabel + StyleHeaderStat.Render(" ") + avgUtil, dot,
		memLabel + StyleHeaderStat.Render(" ") + memUsed + StyleHeaderStat.Render(" ") + memPctStr, dot,
		interval,
	}

	var header strings.Builder
	for _, p := range parts {
		header.WriteString(p)
	}

	return renderHeaderBar(header.String(), width)
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

// utilColor returns the lipgloss color for a GPU utilization percentage.
func utilColor(pct float64) lipgloss.Color {
	if pct >= 90 {
		return lipgloss.Color("#FF4444")
	}
	if pct >= 70 {
		return lipgloss.Color("#FFD700")
	}
	return lipgloss.Color("#00FF7F")
}

// scrapeAgeStr formats the DCGM scrape age with color coding.
// Red with "!" if >30s, amber if >15s, dim if fresh.
func scrapeAgeStr(lastScrape time.Time) string {
	if lastScrape.IsZero() {
		return StyleMetricNA.Render("-")
	}
	age := time.Since(lastScrape)
	label := fmt.Sprintf("%ds", int(age.Seconds()))
	if age > 30*time.Second {
		return StyleMetricBad.Render(label + " !")
	}
	if age > 15*time.Second {
		return StyleMetricWarn.Render(label)
	}
	return lipgloss.NewStyle().Foreground(colorSubtext).Render(label)
}

// renderCardMetricRow renders a two-column metric row inside a card.
// Each column is label + value, separated by whitespace to fill cardInner width.
func renderCardMetricRow(leftLabel, leftVal, rightLabel, rightVal string, innerWidth int) string {
	colWidth := innerWidth / 2
	left := padRight(leftLabel, 8) + padRight(leftVal, colWidth-8)
	right := padRight(rightLabel, 8) + padRight(rightVal, colWidth-8)
	row := left + right
	if len(row) > innerWidth {
		row = row[:innerWidth]
	}
	return padRight(row, innerWidth)
}

// renderSingleMetricRow renders a single-column metric row inside a card.
func renderSingleMetricRow(label, value string, innerWidth int) string {
	row := padRight(label, 8) + value
	return padRight(truncate(row, innerWidth), innerWidth)
}

// renderGPUCard renders a single GPU card with border and metrics.
func renderGPUGridCard(g *metrics.GPUInfo, selected bool, cardWidth int) string {
	borderColor := lipgloss.Color("#21262d")
	if selected {
		borderColor = lipgloss.Color("#58a6ff")
	}

	// Inner width accounts for border padding (1 char each side from lipgloss border)
	innerWidth := cardWidth - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	// Title line: "GPU N" left, util% right
	gpuLabel := fmt.Sprintf("GPU %d", g.Index)
	utilPctStr := fmt.Sprintf("%.0f%%", g.UtilPct)
	utilStyle := lipgloss.NewStyle().Foreground(utilColor(g.UtilPct)).Bold(true)

	titlePadding := innerWidth - len(gpuLabel) - len(utilPctStr)
	if titlePadding < 1 {
		titlePadding = 1
	}
	titleLine := lipgloss.NewStyle().Bold(true).Foreground(colorWhite).Render(gpuLabel) +
		strings.Repeat(" ", titlePadding) +
		utilStyle.Render(utilPctStr)

	// Utilization bar
	barWidth := innerWidth
	utilBar := RenderKVBar(g.UtilPct, barWidth)

	// Memory bandwidth
	memBWVal := fmt.Sprintf("%.0f%%", g.MemBWUtil)

	// Temperature
	tempVal := GPUTempStyle(g.TempC).Render(fmt.Sprintf("%.0f°C", g.TempC))

	// Power
	powerVal := fmt.Sprintf("%.0fW", g.PowerW)

	// NVLink
	nvlinkVal := fmt.Sprintf("%.0f GB/s", g.NVLinkGBs)

	// ECC
	eccVal := fmt.Sprintf("%d", g.ECCErrors)
	if g.ECCErrors > 0 {
		eccVal = StyleMetricBad.Render(eccVal)
	}

	// VRAM
	var vramPct float64
	if g.MemTotalMB > 0 {
		vramPct = (g.MemUsedMB / g.MemTotalMB) * 100
	}
	vramStr := fmt.Sprintf("%.0f/%.0f GB", g.MemUsedMB/1024, g.MemTotalMB/1024)
	vramVal := VRAMStyle(vramPct).Render(vramStr)

	// Process / pod
	procName := "-"
	if g.Pod != "" {
		procName = g.Pod
	}
	procVal := truncate(procName, innerWidth-8)

	// Scrape age
	ageVal := scrapeAgeStr(g.LastScrape)

	// Build card content lines
	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, utilBar)
	lines = append(lines, renderCardMetricRow("Mem BW", memBWVal, "Temp", tempVal, innerWidth))
	lines = append(lines, renderCardMetricRow("Power", powerVal, "NVLink", nvlinkVal, innerWidth))
	lines = append(lines, renderCardMetricRow("ECC", eccVal, "VRAM", vramVal, innerWidth))
	lines = append(lines, renderSingleMetricRow("Proc", procVal, innerWidth))
	lines = append(lines, renderSingleMetricRow("Age", ageVal, innerWidth))

	content := strings.Join(lines, "\n")

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(innerWidth).
		Padding(0, 1)

	return cardStyle.Render(content)
}

// RenderGPUCards renders a 2-column grid of GPU cards.
func RenderGPUCards(gpus []*metrics.GPUInfo, selectedIdx int, width int) string {
	cardWidth := (width - 6) / 2
	if cardWidth < 30 {
		cardWidth = 30
	}

	var rows []string
	for i := 0; i < len(gpus); i += 2 {
		left := renderGPUGridCard(gpus[i], i == selectedIdx, cardWidth)

		if i+1 < len(gpus) {
			right := renderGPUGridCard(gpus[i+1], i+1 == selectedIdx, cardWidth)
			row := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
			rows = append(rows, row)
		} else {
			rows = append(rows, left)
		}
	}

	return strings.Join(rows, "\n")
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

	// Card grid
	cards := RenderGPUCards(m.gpus, m.gpuSelectedIdx, m.width)
	sb.WriteString(cards)

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
