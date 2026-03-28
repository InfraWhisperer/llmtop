package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// SidebarWidth is the fixed width of the right sidebar.
const SidebarWidth = 32

// MinWidthForSidebar is the minimum terminal width to show the sidebar.
const MinWidthForSidebar = 100

var (
	styleSidebarBorder = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.Border{Left: "│"}).
				BorderForeground(lipgloss.Color("#21262d")).
				Width(SidebarWidth).
				PaddingLeft(1)

	styleSidebarHeader = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	styleSidebarLabel = lipgloss.NewStyle().
				Foreground(colorSubtext)

	styleSidebarValue = lipgloss.NewStyle().
				Foreground(colorWhite)

	styleSidebarRule = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#21262d"))

	styleSidebarDimmed = lipgloss.NewStyle().
				Foreground(colorSurface)

	styleEventError = lipgloss.NewStyle().Foreground(colorRed)
	styleEventWarn  = lipgloss.NewStyle().Foreground(colorYellow)
	styleEventOK    = lipgloss.NewStyle().Foreground(colorGreen)
	styleEventInfo  = lipgloss.NewStyle().Foreground(colorBlue)
)

// RenderSidebar renders the full sidebar content for the selected worker.
func RenderSidebar(worker *metrics.WorkerMetrics, gpus []*metrics.GPUInfo, events []metrics.Event, height int) string {
	if worker == nil {
		return styleSidebarBorder.Height(height).Render("")
	}

	contentWidth := SidebarWidth - 3 // padding + border
	var sections []string

	// Section A: GPU metrics for the worker's node
	sections = append(sections, renderGPUSection(worker, gpus, contentWidth))

	// Section B: KV tier breakdown
	sections = append(sections, renderKVTierSection(worker, contentWidth))

	// Section C: Event stream
	sections = append(sections, renderEventSection(events, contentWidth, height))

	content := strings.Join(sections, "\n")

	return styleSidebarBorder.Height(height).Render(content)
}

func sidebarRule(width int) string {
	return styleSidebarRule.Render(strings.Repeat("─", width))
}

// --- Section A: GPU metrics ---

func renderGPUSection(worker *metrics.WorkerMetrics, gpus []*metrics.GPUInfo, width int) string {
	var sb strings.Builder

	// Find GPUs on the same node as the selected worker
	nodeGPUs := gpusForNode(worker.NodeName, gpus)

	nodeName := worker.NodeName
	if nodeName == "" {
		nodeName = "local"
	}

	// Header with GPU model badge
	header := "GPU · " + truncate(nodeName, width-6)
	sb.WriteString(styleSidebarHeader.Render(header))
	sb.WriteString("\n")

	if len(nodeGPUs) == 0 {
		sb.WriteString(styleSidebarDimmed.Render("  no DCGM data"))
		sb.WriteString("\n")
		sb.WriteString(sidebarRule(width))
		return sb.String()
	}

	// Show GPU model badge from first GPU
	if nodeGPUs[0].Name != "" {
		badge := truncate(nodeGPUs[0].Name, width)
		sb.WriteString(styleSidebarDimmed.Render(badge))
		sb.WriteString("\n")
	}

	for _, g := range nodeGPUs {
		sb.WriteString(renderGPUCard(g, width))
	}

	sb.WriteString(sidebarRule(width))
	return sb.String()
}

func renderGPUCard(g *metrics.GPUInfo, width int) string {
	var sb strings.Builder

	// GPU N + util%
	label := fmt.Sprintf("GPU %d", g.Index)
	utilStr := fmt.Sprintf("%.0f%%", g.UtilPct)
	utilStyle := GPUUtilStyle(g.UtilPct)

	gap := width - len(label) - len(utilStr)
	if gap < 1 {
		gap = 1
	}
	sb.WriteString(styleSidebarValue.Render(label) + strings.Repeat(" ", gap) + utilStyle.Render(utilStr))
	sb.WriteString("\n")

	// SM util bar
	barWidth := width
	if barWidth > 24 {
		barWidth = 24
	}
	sb.WriteString(RenderKVBar(g.UtilPct, barWidth))
	sb.WriteString("\n")

	// Metric rows
	sb.WriteString(sidebarMetricRow("Mem BW", fmt.Sprintf("%.0f%%", g.MemBWUtil), width))
	sb.WriteString(sidebarMetricRow("Temp", fmt.Sprintf("%.0f °C", g.TempC), width))
	sb.WriteString(sidebarMetricRow("Power", fmt.Sprintf("%.0f W", g.PowerW), width))

	if g.NVLinkGBs > 0 {
		sb.WriteString(sidebarMetricRow("NVLink BW", fmt.Sprintf("%.0f GB/s", g.NVLinkGBs), width))
	}

	if g.ECCErrors > 0 {
		sb.WriteString(styleEventError.Render(sidebarMetricRowPlain("ECC errs", fmt.Sprintf("%d", g.ECCErrors), width)))
	}

	// Scrape age indicator
	scrapeAge := time.Since(g.LastScrape)
	if !g.LastScrape.IsZero() {
		ageStr := fmt.Sprintf("%.0fs", scrapeAge.Seconds())
		var ageStyle lipgloss.Style
		switch {
		case scrapeAge > 30*time.Second:
			ageStr += " !"
			ageStyle = StyleMetricBad
		case scrapeAge > 15*time.Second:
			ageStyle = StyleMetricWarn
		default:
			ageStyle = styleSidebarDimmed
		}
		sb.WriteString(styleSidebarLabel.Render("DCGM age") + " " + ageStyle.Render(ageStr))
		sb.WriteString("\n")
	}

	return sb.String()
}

func sidebarMetricRow(label, value string, width int) string {
	return sidebarMetricRowPlain(label, value, width)
}

func sidebarMetricRowPlain(label, value string, width int) string {
	gap := width - len(label) - len(value)
	if gap < 1 {
		gap = 1
	}
	return styleSidebarLabel.Render(label) + strings.Repeat(" ", gap) + styleSidebarValue.Render(value) + "\n"
}

func gpusForNode(nodeName string, gpus []*metrics.GPUInfo) []*metrics.GPUInfo {
	if nodeName == "" {
		return nil
	}
	var result []*metrics.GPUInfo
	for _, g := range gpus {
		if g.Hostname == nodeName {
			result = append(result, g)
		}
	}
	return result
}

// --- Section B: KV tier breakdown ---

func renderKVTierSection(worker *metrics.WorkerMetrics, width int) string {
	var sb strings.Builder

	// Header
	shortName := worker.Label
	if shortName == "" {
		shortName = worker.Endpoint
	}
	if len(shortName) > width-12 {
		shortName = shortName[:width-12] + "…"
	}
	sb.WriteString(styleSidebarHeader.Render("KV TIERS · " + shortName))
	sb.WriteString("\n")

	barWidth := width - 12 // room for label + pct
	if barWidth < 6 {
		barWidth = 6
	}

	// GPU HBM tier (always shown)
	sb.WriteString(renderTierRow("GPU HBM", worker.KVCacheUsagePct, barWidth, width))

	// CPU DRAM tier (only if present)
	if worker.KVCacheUsageCPUPct > 0 {
		sb.WriteString(renderTierRow("CPU DRAM", worker.KVCacheUsageCPUPct, barWidth, width))
	}

	// Below tiers: hit rate, evict/s, prestage lat
	sb.WriteString(sidebarMetricRow("Hit rate", formatPctOrDash(worker.CacheHitRatePct), width))

	if worker.EvictPerSec > 0 {
		sb.WriteString(sidebarMetricRow("Evict/s", fmt.Sprintf("%.0f", worker.EvictPerSec), width))
	} else {
		sb.WriteString(sidebarMetricRow("Evict/s", "-", width))
	}

	// Prestage latency: use TTFT p50 as proxy
	if worker.TTFT_P50 > 0 {
		sb.WriteString(sidebarMetricRow("Prestage lat", fmt.Sprintf("%.1fms", worker.TTFT_P50), width))
	} else {
		sb.WriteString(sidebarMetricRow("Prestage lat", "-", width))
	}

	sb.WriteString(sidebarRule(width))
	return sb.String()
}

func renderTierRow(label string, pct float64, barWidth, _ int) string {
	bar := RenderKVBar(pct, barWidth)
	pctStr := fmt.Sprintf("%.0f%%", pct)

	return styleSidebarLabel.Render(padRight(label, 9)) + bar + " " + styleSidebarValue.Render(pctStr) + "\n"
}

func formatPctOrDash(pct float64) string {
	if pct <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

// --- Section C: Event stream ---

func renderEventSection(events []metrics.Event, width, maxHeight int) string {
	var sb strings.Builder

	sb.WriteString(styleSidebarHeader.Render("RECENT EVENTS"))
	sb.WriteString("\n")

	if len(events) == 0 {
		sb.WriteString(styleSidebarDimmed.Render("  no events"))
		sb.WriteString("\n")
		return sb.String()
	}

	// Show events in reverse chronological order (newest first)
	maxEvents := maxHeight / 2 // rough estimate of space available
	if maxEvents > len(events) {
		maxEvents = len(events)
	}
	if maxEvents < 1 {
		maxEvents = 1
	}

	for i := len(events) - 1; i >= len(events)-maxEvents; i-- {
		e := events[i]
		ts := e.Time.Format("15:04:05")
		dot := eventDot(e.Severity)
		msg := truncate(e.Message, width-11) // 8 (time) + 3 (dot + spaces)
		sb.WriteString(styleSidebarDimmed.Render(ts) + " " + dot + " " + msg)
		sb.WriteString("\n")
	}

	return sb.String()
}

func eventDot(severity metrics.EventSeverity) string {
	switch severity {
	case metrics.SeverityError:
		return styleEventError.Render("●")
	case metrics.SeverityWarn:
		return styleEventWarn.Render("●")
	case metrics.SeverityOK:
		return styleEventOK.Render("●")
	default:
		return styleEventInfo.Render("●")
	}
}
