package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// RenderBookmarkCompare renders the V8 side-by-side compare overlay.
//
// Layout: up to MaxBookmarks columns, one per bookmark. Each column shows
// label, current TTFT P99 / ITL P99 / KV% / queue values, and a TTFT
// sparkline drawn from the V1 ring (when non-nil). Sparklines share a Y
// axis: the global max across all bookmarked workers' histories sets the
// scale.
//
// ring may be nil — sparklines are simply omitted; current values still
// render. When a bookmarked worker is not in workers (pod deleted but
// Reconcile hasn't run), its column shows "(offline)".
func RenderBookmarkCompare(workers []*metrics.WorkerMetrics, ring *metrics.RingBuffer, bookmarks []WorkerBookmark, width, height int) string {
	var sb strings.Builder

	sb.WriteString(StyleHelpTitle.Render("Bookmark Compare"))
	sb.WriteString("\n\n")

	if len(bookmarks) == 0 {
		sb.WriteString(centeredCard("No bookmarks — press b on a worker", width, height))
		return sb.String()
	}

	n := min(len(bookmarks), MaxBookmarks)
	colWidth := max((width-4)/n, 18)
	sparkWidth := max(colWidth-4, 8)

	// Walk bookmarks to build per-column data and the shared Y max.
	type colData struct {
		bm      WorkerBookmark
		w       *metrics.WorkerMetrics
		spark   []float64
		offline bool
	}
	cols := make([]colData, 0, n)
	var sharedMax float64
	for i := range n {
		bm := bookmarks[i]
		var w *metrics.WorkerMetrics
		for _, candidate := range workers {
			if candidate != nil && candidate.Endpoint == bm.Endpoint {
				w = candidate
				break
			}
		}
		var spark []float64
		if ring != nil {
			spark = ring.SparklineFor(bm.Endpoint, sparkWidth)
		}
		for _, v := range spark {
			if v > sharedMax {
				sharedMax = v
			}
		}
		cols = append(cols, colData{bm: bm, w: w, spark: spark, offline: w == nil})
	}

	// Render row-by-row, joining columns horizontally.
	rowBuilders := make([]strings.Builder, n)
	labelStyle := StyleHeaderTitle
	for i, c := range cols {
		header := truncate(c.bm.Label, colWidth)
		if c.bm.Label == "" {
			header = truncate(c.bm.Endpoint, colWidth)
		}
		rowBuilders[i].WriteString(labelStyle.Render(padRight(header, colWidth)))
		rowBuilders[i].WriteString("\n")

		if c.offline {
			rowBuilders[i].WriteString(StyleMetricBad.Render(padRight("(offline)", colWidth)))
			rowBuilders[i].WriteString("\n")
			continue
		}
		w := c.w
		writeMetric(&rowBuilders[i], "TTFT P99", fmtMs(w.TTFT_P99), colWidth)
		writeMetric(&rowBuilders[i], "ITL  P99", fmtMs(w.ITL_P99), colWidth)
		writeMetric(&rowBuilders[i], "KV%     ", fmt.Sprintf("%.0f%%", w.KVCacheUsagePct), colWidth)
		writeMetric(&rowBuilders[i], "Queue   ", fmt.Sprintf("%d", w.RequestsWaiting), colWidth)
		writeMetric(&rowBuilders[i], "Tok/s   ", fmt.Sprintf("%.0f", w.PromptTokPerSec+w.GenTokPerSec), colWidth)
		rowBuilders[i].WriteString("\n")
		if len(c.spark) > 0 {
			rowBuilders[i].WriteString(StyleMetricNA.Render("TTFT 60s"))
			rowBuilders[i].WriteString("\n")
			rowBuilders[i].WriteString(renderSharedSparkline(c.spark, sharedMax, sparkWidth))
		}
	}

	// Pad each column to equal line count, then horizontal-join.
	maxLines := 0
	colStrings := make([]string, n)
	for i := range cols {
		colStrings[i] = rowBuilders[i].String()
		if l := strings.Count(colStrings[i], "\n"); l > maxLines {
			maxLines = l
		}
	}
	for i := range colStrings {
		for strings.Count(colStrings[i], "\n") < maxLines {
			colStrings[i] += "\n" + strings.Repeat(" ", colWidth)
		}
	}

	// Use lipgloss.JoinHorizontal to align by top.
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, colStrings...))

	sb.WriteString("\n")
	sb.WriteString(StyleFooter.Render("Press esc to return"))
	return sb.String()
}

// renderSharedSparkline draws a sparkline for a single column where the
// vertical scale is fixed across all columns (sharedMax). Lower values
// produce taller bars at the bottom; we want the visual to match the
// detail-view sparkline aesthetic.
func renderSharedSparkline(data []float64, sharedMax float64, width int) string {
	if width <= 0 || len(data) == 0 {
		return ""
	}
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	src := data
	if len(src) > width {
		src = src[len(src)-width:]
	}
	scale := sharedMax
	if scale <= 0 {
		scale = 1
	}
	var sb strings.Builder
	var latest float64
	for _, v := range src {
		idx := int(v / scale * float64(len(blocks)-1))
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		if idx < 0 {
			idx = 0
		}
		sb.WriteRune(blocks[idx])
		latest = v
	}
	if rw := len(src); rw < width {
		sb.WriteString(strings.Repeat(" ", width-rw))
	}
	return TTFTStyle(latest).Render(sb.String())
}

func writeMetric(sb *strings.Builder, label, value string, colWidth int) {
	line := fmt.Sprintf("%s %s", label, value)
	sb.WriteString(StyleMetricNA.Render(label) + " ")
	sb.WriteString(StyleHeaderValue.Render(value))
	// pad to colWidth.
	pad := colWidth - len(line)
	if pad > 0 {
		sb.WriteString(strings.Repeat(" ", pad))
	}
	sb.WriteString("\n")
}

func fmtMs(ms float64) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0fms", ms)
}

// centeredCard renders a single short message centered on screen, used as
// the empty-state for the compare view.
func centeredCard(msg string, width, height int) string {
	card := StyleHelpOverlay.Render(msg)
	return lipgloss.PlaceHorizontal(width, lipgloss.Center,
		lipgloss.PlaceVertical(height, lipgloss.Center, card))
}
