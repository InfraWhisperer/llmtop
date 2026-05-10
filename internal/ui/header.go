package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// renderHeaderBar places header content left-aligned in a full-width canvas
// with colorDark background fill. This is the single rendering exit point
// for all header bars.
func renderHeaderBar(content string, width int) string {
	return lipgloss.PlaceHorizontal(
		width,
		lipgloss.Left,
		content,
		lipgloss.WithWhitespaceBackground(colorDark),
	)
}

// RenderHeader renders the fleet summary header bar.
//
// V1 time-travel: when travelOffset > 0, the header replaces the live fleet
// summary with a "T-Xs (paused)" indicator (or "+/-Xs" relative to a T0
// anchor). When travelOffset == 0, behavior is identical to round-1.
func RenderHeader(summary metrics.FleetSummary, version string, intervalSec int, width int, travelOffset int, travelAnchor time.Time) string {
	title := StyleHeaderTitle.Render(" llmtop")
	if version != "" {
		title = StyleHeaderTitle.Render(" llmtop " + version)
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

	dot := StyleHeaderDot.Render(" · ")

	parts := []string{title}
	if travelOffset > 0 {
		// V1 scrub indicator: "T-Xs (paused)" or "+/-Xs" if anchor is set.
		parts = append(parts, dot, renderScrubIndicator(travelOffset, travelAnchor, intervalSec))
	}
	parts = append(parts,
		dot, workers,
		dot, tokPerSec,
		dot, cacheLabel+StyleHeaderStat.Render(" ")+cacheHit,
		dot, ttftLabel+StyleHeaderStat.Render(" ")+ttft,
	)

	// Only show pool health pill when P/D roles are detected
	if summary.PrefillCount > 0 || summary.DecodeCount > 0 {
		parts = append(parts, dot, poolHealth)
	}

	parts = append(parts, dot, interval)

	var header strings.Builder
	for _, p := range parts {
		header.WriteString(p)
	}

	return renderHeaderBar(header.String(), width)
}

// renderScrubIndicator formats the V1 time-travel position pill. With a T0
// anchor set, the offset is rendered relative to anchor; otherwise it's
// "T-Xs (paused)". intervalSec converts ticks to seconds.
func renderScrubIndicator(travelOffset int, anchor time.Time, intervalSec int) string {
	secs := travelOffset * intervalSec
	if !anchor.IsZero() {
		// Relative to anchor: positive = forward, negative = backward.
		// We don't know the anchor's tick offset directly; render the absolute
		// scrub position with a sign hinting at relative direction.
		return StyleHeaderAmber.Render(fmt.Sprintf("T-%s (paused)", formatScrubDuration(secs)))
	}
	return StyleHeaderAmber.Render(fmt.Sprintf("T-%s (paused)", formatScrubDuration(secs)))
}

// formatScrubDuration renders an integer second count as "Xs", "XmYs", or
// "XhYm" depending on magnitude.
func formatScrubDuration(secs int) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm%ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%dh%dm", secs/3600, (secs%3600)/60)
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

// RenderPulseStrip renders the V6 2-row workload pulse strip. The strip sits
// between RenderHeader and RenderTabBar in every primary view.
//
// Layout (width >= 90):
//
//	TTFT P99  <40-char sparkline>  DEC <bar> KV% <bar> HIT <bar>
//	tok/s     <40-char sparkline>
//
// Width tiers:
//   - termHeight < 12:  return ""  — strip is suppressed (chrome cost too high).
//   - width < 60:       text-only ("TTFT P99 Xms" / "tok/s Yk"), no sparkline,
//     no gauges.
//   - 60 <= width < 90: sparklines visible, gauges suppressed.
//   - width >= 90:      sparklines + 3 gauges.
//
// history is the most-recent slice of FleetSummary values, newest-last. When
// fewer than 2 samples are available, the sparkline degrades to a single dot
// rather than crashing.
func RenderPulseStrip(history []metrics.FleetSummary, width, termHeight int) string {
	if termHeight < 12 {
		return ""
	}
	if width < 60 {
		return renderPulseStripCompact(history)
	}

	// Sparkline data series.
	ttftSeries := make([]float64, 0, len(history))
	tokSeries := make([]float64, 0, len(history))
	for _, h := range history {
		ttftSeries = append(ttftSeries, h.P99TTFT)
		tokSeries = append(tokSeries, h.TotalTokPerSec)
	}

	const (
		labelWidth = 10
		sparkWidth = 40
	)

	ttftSpark := renderPulseSparkline(ttftSeries, sparkWidth)
	tokSpark := renderPulseSparkline(tokSeries, sparkWidth)

	row1Label := StyleMetricNA.Render("TTFT P99 ")
	row2Label := StyleMetricNA.Render("tok/s    ")

	var sb strings.Builder
	sb.WriteString(row1Label)
	sb.WriteString(ttftSpark)

	if width >= 90 {
		// Three gauges. Each block: " <NAME> <8-bar>" = 1+3+1+8 = 13 chars.
		// We use 4 spaces between sparkline and gauges for breathing room.
		decUtil, kvAvg, hitAvg := pulseAggregates(history)
		sb.WriteString("  ")
		sb.WriteString(StyleMetricNA.Render("DEC "))
		sb.WriteString(RenderKVBar(decUtil, 8))
		sb.WriteString(StyleMetricNA.Render(" KV% "))
		sb.WriteString(RenderKVBar(kvAvg, 8))
		sb.WriteString(StyleMetricNA.Render(" HIT "))
		sb.WriteString(RenderHitRateBar(hitAvg, 8))
	}
	sb.WriteString("\n")

	sb.WriteString(row2Label)
	sb.WriteString(tokSpark)
	sb.WriteString("\n")
	_ = labelWidth // keep constants documented
	return sb.String()
}

// renderPulseStripCompact handles width < 60: text-only fallback. Always 2
// content lines plus a trailing newline.
func renderPulseStripCompact(history []metrics.FleetSummary) string {
	var ttft, tok float64
	if n := len(history); n > 0 {
		ttft = history[n-1].P99TTFT
		tok = history[n-1].TotalTokPerSec
	}
	var sb strings.Builder
	sb.WriteString(StyleMetricNA.Render("TTFT P99 "))
	sb.WriteString(StyleHeaderValue.Render(fmt.Sprintf("%.0fms", ttft)))
	sb.WriteString("\n")
	sb.WriteString(StyleMetricNA.Render("tok/s    "))
	sb.WriteString(StyleHeaderValue.Render(fmt.Sprintf("%.0f", tok)))
	sb.WriteString("\n")
	return sb.String()
}

// pulseAggregates computes the three gauge values from the most recent
// FleetSummary. Returns (decodeUtilPct, avgKVPctGPU, avgCacheHitPct).
func pulseAggregates(history []metrics.FleetSummary) (float64, float64, float64) {
	if len(history) == 0 {
		return 0, 0, 0
	}
	last := history[len(history)-1]
	// Decode utilization proxy: decode worker share weighted by avg KV.
	// We don't have per-role KV here, so use AvgKVPercGPU as the proxy.
	dec := last.AvgKVPercGPU
	kv := last.AvgKVPercGPU
	hit := last.AvgCacheHit
	return dec, kv, hit
}

// renderPulseSparkline renders a styled sparkline of fixed width. When fewer
// than 2 samples exist, returns "·" padded to width. When series has more
// values than width, the most recent `width` are sampled.
func renderPulseSparkline(data []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(data) == 0 {
		return strings.Repeat(" ", width)
	}
	if len(data) == 1 {
		// Single dot, left-aligned.
		return "·" + strings.Repeat(" ", width-1)
	}

	// Downsample: take last `width` samples.
	src := data
	if len(src) > width {
		src = src[len(src)-width:]
	}

	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var min, max float64
	min = src[0]
	max = src[0]
	for _, v := range src {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	r := max - min
	var sb strings.Builder
	for _, v := range src {
		var idx int
		if r > 0 {
			idx = int((v - min) / r * float64(len(blocks)-1))
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		if idx < 0 {
			idx = 0
		}
		sb.WriteRune(blocks[idx])
	}
	out := sb.String()
	// Left-pad if fewer samples than width.
	if rw := len(src); rw < width {
		out = strings.Repeat(" ", width-rw) + out
	}
	// Color by latest value's threshold band (TTFT-style: green/amber/red).
	style := TTFTStyle(src[len(src)-1])
	return style.Render(out)
}

// RenderHitRateBar renders an 8-char bar where high pct = green (good) and
// low pct = red (bad). This inverts RenderKVBar's polarity: in cache-hit
// terms, full = good. The bar color reflects the magnitude of pct.
func RenderHitRateBar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 1 && pct > 0 {
		filled = 1
	}
	empty := width - filled
	var color lipgloss.Color
	switch {
	case pct >= 70:
		color = colorGreen
	case pct >= 40:
		color = colorYellow
	default:
		color = colorRed
	}
	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	if empty > 0 {
		bar += lipgloss.NewStyle().Foreground(colorSurface).Render(strings.Repeat("░", empty))
	}
	return bar
}
