package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// RenderHeader renders the fleet summary header bar.
func RenderHeader(summary metrics.FleetSummary, version string, intervalSec int, width int) string {
	title := StyleHeaderTitle.Render("llmtop")
	if version != "" {
		title = StyleHeaderTitle.Render("llmtop " + version)
	}

	workers := StyleHeaderStat.Render(
		fmt.Sprintf("%d workers (%d online)",
			summary.TotalWorkers, summary.OnlineWorkers),
	)

	tokPerSec := StyleHeaderValue.Render(fmt.Sprintf("%.0f tok/s", summary.TotalTokPerSec))

	cacheHit := renderCacheHitPill(summary.AvgKVPercGPU)
	cacheLabel := StyleHeaderStat.Render("cache hit")

	ttft := StyleHeaderValue.Render(fmt.Sprintf("%.0fms", summary.P99TTFT))
	ttftLabel := StyleHeaderStat.Render("P99 TTFT")

	poolHealth := renderPoolHealthPill(summary.PrefillCount, summary.DecodeCount)

	interval := StyleHeaderStat.Render(fmt.Sprintf("↻ %ds", intervalSec))

	dot := StyleHeaderDot.Render("·")

	parts := []string{
		" " + title,
		dot,
		workers,
		dot,
		tokPerSec,
		dot,
		cacheLabel + " " + cacheHit,
		dot,
		ttftLabel + " " + ttft,
	}

	// Only show pool health pill when P/D roles are detected
	if summary.PrefillCount > 0 || summary.DecodeCount > 0 {
		parts = append(parts, dot, poolHealth)
	}

	parts = append(parts, dot, interval+" ")

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

// renderCacheHitPill formats the cache hit percentage with color:
// amber if <30%, green if ≥30%.
func renderCacheHitPill(pct float64) string {
	text := fmt.Sprintf("%.0f%%", pct)
	if pct < 30 {
		return StyleHeaderAmber.Render(text)
	}
	return StyleHeaderValue.Render(text)
}

// renderPoolHealthPill renders the "3P·5D" pool health indicator.
// Red if either prefill or decode count is 0.
func renderPoolHealthPill(prefill, decode int) string {
	text := fmt.Sprintf("%dP·%dD", prefill, decode)
	if prefill == 0 || decode == 0 {
		return StyleHeaderRed.Render(text)
	}
	return StyleHeaderValue.Render(text)
}
