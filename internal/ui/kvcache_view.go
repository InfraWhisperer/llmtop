package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// KV Cache tab column widths.
const (
	colKCRole     = 8
	colKCGPUBar   = 8
	colKCGPUPct   = 5
	colKCCPUBar   = 8
	colKCCPUPct   = 5
	colKCNVMeBar  = 8
	colKCNVMePct  = 5
	colKCHit      = 6
	colKCEvict    = 8
	colKCPrestage = 10
)

// Tier bar colors.
var (
	colorTierGPU  = lipgloss.Color("#58a6ff")
	colorTierCPU  = lipgloss.Color("#8b949e")
	colorTierNVMe = lipgloss.Color("#484f58")
	colorBarEmpty = colorSurface
)

// kcFixedWidth is the sum of fixed columns plus inter-column spaces and left margin.
// Layout: "  " (2) + ROLE(8) + " " + WORKER(flex) + " " + GPU bar(8) + " " + GPU%(5) + " " +
//
//	CPU bar(8) + " " + CPU%(5) + " " + HIT%(6) + " " + EVICT/S(8) + " " + PRESTAGE(10)
//
// Fixed cols: 8+8+5+8+5+6+8+10 = 58, spaces between 8 cols = 7, margin = 2 => 67
var kcFixedWidth = colKCRole + colKCGPUBar + colKCGPUPct + colKCCPUBar + colKCCPUPct + colKCHit + colKCEvict + colKCPrestage + 9

// kcFixedWidthNVMe adds the NVMe bar and pct columns when NVMe data is present.
var kcFixedWidthNVMe = kcFixedWidth + colKCNVMeBar + colKCNVMePct + 2

// kcWorkerWidth computes the flex WORKER column width.
func kcWorkerWidth(termWidth int, showNVMe bool) int {
	fixed := kcFixedWidth
	if showNVMe {
		fixed = kcFixedWidthNVMe
	}
	w := termWidth - fixed
	if w < 16 {
		w = 16
	}
	return w
}

// renderTierBar renders an inline bar of barWidth chars filled proportionally to pct,
// using the specified color for filled blocks and dim for empty blocks.
func renderTierBar(pct float64, barWidth int, color lipgloss.Color) string {
	if pct <= 0 {
		return lipgloss.NewStyle().Foreground(colorBarEmpty).Render(strings.Repeat("░", barWidth))
	}
	filled := int(pct / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 1 && pct > 0 {
		filled = 1
	}
	empty := barWidth - filled
	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	if empty > 0 {
		bar += lipgloss.NewStyle().Foreground(colorBarEmpty).Render(strings.Repeat("░", empty))
	}
	return bar
}

// hitRateColor returns the appropriate style for a cache hit rate percentage.
func hitRateColor(pct float64) lipgloss.Style {
	if pct > 40 {
		return StyleMetricGood
	}
	if pct >= 25 {
		return StyleMetricWarn
	}
	return StyleMetricBad
}

// evictColor returns the appropriate style for an eviction rate (per second).
func evictColor(rate float64) lipgloss.Style {
	if rate == 0 {
		return StyleMetricGood
	}
	if rate <= 5 {
		return StyleMetricWarn
	}
	return StyleMetricBad
}

// prestageColor returns the appropriate style for prestage latency in milliseconds.
func prestageColor(ms float64) lipgloss.Style {
	if ms < 5 {
		return StyleMetricGood
	}
	if ms <= 15 {
		return StyleMetricWarn
	}
	return StyleMetricBad
}

// RenderKVCacheView renders the KV Cache [3] tab showing per-worker KV cache tier breakdown.
func RenderKVCacheView(workers []*metrics.WorkerMetrics, width int) string {
	// Filter to online workers and sort by GPU HBM fill descending.
	var online []*metrics.WorkerMetrics
	for _, w := range workers {
		if w.Online {
			online = append(online, w)
		}
	}
	sort.Slice(online, func(i, j int) bool {
		return online[i].KVCacheUsagePct > online[j].KVCacheUsagePct
	})

	// NVMe column visibility: show only if any worker has >0 NVMe usage.
	// No NVMe field exists yet, so this is always false.
	showNVMe := false

	workerW := kcWorkerWidth(width, showNVMe)

	var sb strings.Builder

	// Header row
	sb.WriteString(renderKCHeader(workerW, showNVMe))
	sb.WriteString("\n")

	// Separator
	sep := StyleTableSeparator.Render(strings.Repeat("─", max(width-2, 80)))
	sb.WriteString("  " + sep)
	sb.WriteString("\n")

	// Cluster summary row (bold, above the per-worker rows)
	sb.WriteString(renderKCSummaryRow(online, workerW, showNVMe))
	sb.WriteString("\n")

	// Summary-to-data separator
	sb.WriteString("  " + StyleTableSeparator.Render(strings.Repeat("─", max(width-2, 80))))
	sb.WriteString("\n")

	// Per-worker rows
	for _, w := range online {
		sb.WriteString(renderKCWorkerRow(w, workerW, showNVMe))
		sb.WriteString("\n")
	}

	return sb.String()
}

// renderKCHeader renders the column header row for the KV cache table.
func renderKCHeader(workerW int, showNVMe bool) string {
	type col struct {
		name  string
		width int
	}

	cols := []col{
		{"WORKER", workerW},
		{"ROLE", colKCRole},
		{"GPU HBM", colKCGPUBar},
		{"GPU%", colKCGPUPct},
		{"CPU DRAM", colKCCPUBar},
		{"CPU%", colKCCPUPct},
	}
	if showNVMe {
		cols = append(cols, col{"NVMe", colKCNVMeBar})
		cols = append(cols, col{"NVM%", colKCNVMePct})
	}
	cols = append(cols,
		col{"HIT%", colKCHit},
		col{"EVICT/S", colKCEvict},
		col{"PRESTAGE", colKCPrestage},
	)

	var parts []string
	for _, c := range cols {
		parts = append(parts, StyleTableHeader.Render(padRight(c.name, c.width)))
	}
	return "  " + strings.Join(parts, " ")
}

// renderKCSummaryRow renders the bold cluster summary row with averaged metrics.
func renderKCSummaryRow(online []*metrics.WorkerMetrics, workerW int, showNVMe bool) string {
	n := len(online)
	if n == 0 {
		return "  " + lipgloss.NewStyle().Bold(true).Render("all workers · 0 tiers")
	}

	// Count tiers present.
	tiers := 1 // GPU HBM always counts
	hasCPU := false
	for _, w := range online {
		if w.KVCacheUsageCPUPct > 0 {
			hasCPU = true
			break
		}
	}
	if hasCPU {
		tiers++
	}
	if showNVMe {
		tiers++
	}

	// Compute averages.
	var gpuSum, cpuSum, hitSum, evictSum, prestageSum float64
	var hitCount int
	for _, w := range online {
		gpuSum += w.KVCacheUsagePct
		cpuSum += w.KVCacheUsageCPUPct
		if w.CacheHitRatePct > 0 {
			hitSum += w.CacheHitRatePct
			hitCount++
		}
		evictSum += w.EvictPerSec
		prestageSum += w.TTFT_P50
	}
	fn := float64(n)
	avgGPU := gpuSum / fn
	avgCPU := cpuSum / fn
	avgEvict := evictSum / fn
	avgPrestage := prestageSum / fn
	var avgHit float64
	if hitCount > 0 {
		avgHit = hitSum / float64(hitCount)
	}

	bold := lipgloss.NewStyle().Bold(true)

	label := bold.Render(padRight(fmt.Sprintf("all workers · %d tiers", tiers), workerW))
	role := bold.Render(padRight("—", colKCRole))
	gpuBar := renderTierBar(avgGPU, colKCGPUBar, colorTierGPU)
	gpuPct := bold.Render(padRight(fmt.Sprintf("%.0f%%", avgGPU), colKCGPUPct))
	cpuBar := renderTierBar(avgCPU, colKCCPUBar, colorTierCPU)
	cpuPct := bold.Render(padRight(fmt.Sprintf("%.0f%%", avgCPU), colKCCPUPct))

	parts := []string{label, role, gpuBar, gpuPct, cpuBar, cpuPct}

	if showNVMe {
		parts = append(parts, renderTierBar(0, colKCNVMeBar, colorTierNVMe))
		parts = append(parts, bold.Render(padRight("0%", colKCNVMePct)))
	}

	hitStr := formatKCPct(avgHit, colKCHit)
	evictStr := padRight(fmt.Sprintf("%.1f", avgEvict), colKCEvict)
	prestageStr := formatKCLatency(avgPrestage, colKCPrestage)

	parts = append(parts,
		bold.Render(hitStr),
		bold.Render(evictStr),
		bold.Render(prestageStr),
	)

	return "  " + strings.Join(parts, " ")
}

// renderKCWorkerRow renders a single worker row in the KV cache table.
func renderKCWorkerRow(w *metrics.WorkerMetrics, workerW int, showNVMe bool) string {
	label := w.Label
	if label == "" {
		label = w.Endpoint
	}
	workerStr := padRight(truncate(label, workerW), workerW)

	role := w.Role
	if role == "" {
		role = "mono"
	}
	roleStr := RoleStyle(role).Render(padRight(role, colKCRole))

	gpuBar := renderTierBar(w.KVCacheUsagePct, colKCGPUBar, colorTierGPU)
	gpuPct := padRight(fmt.Sprintf("%.0f%%", w.KVCacheUsagePct), colKCGPUPct)

	cpuBar := renderTierBar(w.KVCacheUsageCPUPct, colKCCPUBar, colorTierCPU)
	cpuPct := padRight(fmt.Sprintf("%.0f%%", w.KVCacheUsageCPUPct), colKCCPUPct)

	parts := []string{workerStr, roleStr, gpuBar, gpuPct, cpuBar, cpuPct}

	if showNVMe {
		parts = append(parts, renderTierBar(0, colKCNVMeBar, colorTierNVMe))
		parts = append(parts, padRight("0%", colKCNVMePct))
	}

	// HIT%
	hitPlain := formatKCPct(w.CacheHitRatePct, colKCHit)
	if w.CacheHitRatePct == 0 {
		parts = append(parts, StyleMetricNA.Render(hitPlain))
	} else {
		parts = append(parts, hitRateColor(w.CacheHitRatePct).Render(hitPlain))
	}

	// EVICT/S
	evictPlain := padRight(fmt.Sprintf("%.1f", w.EvictPerSec), colKCEvict)
	parts = append(parts, evictColor(w.EvictPerSec).Render(evictPlain))

	// PRESTAGE (using TTFT_P50 as proxy)
	prestagePlain := formatKCLatency(w.TTFT_P50, colKCPrestage)
	if w.TTFT_P50 == 0 {
		parts = append(parts, StyleMetricNA.Render(prestagePlain))
	} else {
		parts = append(parts, prestageColor(w.TTFT_P50).Render(prestagePlain))
	}

	return "  " + strings.Join(parts, " ")
}

// formatKCPct formats a percentage value for display, showing "-" for zero.
func formatKCPct(pct float64, width int) string {
	if pct == 0 {
		return padRight("-", width)
	}
	return padRight(fmt.Sprintf("%.0f%%", pct), width)
}

// formatKCLatency formats a latency value in milliseconds for display.
func formatKCLatency(ms float64, width int) string {
	if ms == 0 {
		return padRight("-", width)
	}
	return padRight(fmt.Sprintf("%.0fms", ms), width)
}
