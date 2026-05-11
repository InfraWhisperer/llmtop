package ui

import (
	"fmt"
	"strings"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// RenderEvictionTimeline renders the V10 KV-eviction timeline view.
//
// frames is an oldest-first slice from RingBuffer.ExtractTimelineWindow;
// nil triggers the "(data unavailable)" placeholder. alert anchors the
// header label and provides the rule name. workerEndpoint identifies the
// affected worker — we match each frame's WorkerMetrics by Endpoint with a
// fall-through to Label match for backwards compat. alertTickIdx is the
// index in frames where the alert fired (used to draw the T=0 cursor).
func RenderEvictionTimeline(frames []metrics.FrameSnapshot, alert *metrics.Alert, workerEndpoint string, alertTickIdx int, width, height int) string {
	var sb strings.Builder

	title := "KV Eviction Timeline"
	if alert != nil {
		title = fmt.Sprintf("%s · %s", title, alert.Title)
	}
	sb.WriteString(StyleHelpTitle.Render(title))
	sb.WriteString("\n")

	if len(frames) == 0 {
		sb.WriteString(StyleMetricBad.Render("(data unavailable — alert predates buffer or ring not yet recording)"))
		sb.WriteString("\n\n")
		sb.WriteString(StyleFooter.Render("Press any key to return"))
		return sb.String()
	}

	// Header line: source + window size + T=0 tick.
	source := workerEndpoint
	if alert != nil && source == "" {
		source = alert.Source
	}
	sb.WriteString(StyleMetricNA.Render(fmt.Sprintf("Source: %s · %d ticks · T=0 at idx %d",
		source, len(frames), alertTickIdx)))
	sb.WriteString("\n\n")

	// Extract per-frame series for the affected worker.
	kvSeries := make([]float64, len(frames))
	queueSeries := make([]float64, len(frames))
	evictSeries := make([]float64, len(frames))
	hitSeries := make([]float64, len(frames))
	for i, f := range frames {
		w := matchWorker(f.Workers, workerEndpoint, alert)
		if w == nil {
			continue
		}
		kvSeries[i] = w.KVCacheUsagePct
		queueSeries[i] = float64(w.RequestsWaiting)
		evictSeries[i] = w.EvictPerSec
		hitSeries[i] = w.CacheHitRatePct
	}

	trackWidth := max(width-14, 30)

	sb.WriteString(renderTimelineTrack("KV%      ", kvSeries, alertTickIdx, trackWidth, kvAnnotations()))
	sb.WriteString(renderTimelineTrack("Queue    ", queueSeries, alertTickIdx, trackWidth, nil))
	sb.WriteString(renderTimelineTrack("Evict/s  ", evictSeries, alertTickIdx, trackWidth, nil))
	sb.WriteString(renderTimelineTrack("Hit %    ", hitSeries, alertTickIdx, trackWidth, nil))

	// X-axis labels: -1m at left, T=0 marker, +1m at right.
	axis := renderTimelineAxis(alertTickIdx, len(frames), trackWidth)
	sb.WriteString(StyleMetricNA.Render(strings.Repeat(" ", 9)))
	sb.WriteString(axis)
	sb.WriteString("\n\n")

	sb.WriteString(StyleFooter.Render("Press esc or any non-navigation key to return"))
	return sb.String()
}

// matchWorker returns the WorkerMetrics in the frame matching either the
// endpoint or the alert's source label.
func matchWorker(workers []metrics.WorkerMetrics, endpoint string, alert *metrics.Alert) *metrics.WorkerMetrics {
	for i := range workers {
		if endpoint != "" && workers[i].Endpoint == endpoint {
			return &workers[i]
		}
	}
	if alert != nil {
		for i := range workers {
			if workers[i].Label == alert.Source {
				return &workers[i]
			}
		}
	}
	return nil
}

// kvAnnotations returns horizontal threshold guides at 75% (warn) and 90%
// (crit). Currently informational; renderTimelineTrack does not draw them
// inline (terminal sparkline can't easily plot Y-axis guides). Kept as a
// hook for future overlay.
func kvAnnotations() []timelineAnnotation {
	return []timelineAnnotation{
		{value: 75, glyph: '─', label: "warn"},
		{value: 90, glyph: '─', label: "crit"},
	}
}

type timelineAnnotation struct {
	value float64
	glyph rune
	label string
}

// renderTimelineTrack renders one 3-line track: label + sparkline +
// (currently a blank axis line per row to keep the layout tall enough to
// read at a glance). The sparkline blocks-encode the series; the T=0
// position is marked with the cursor character ┊.
func renderTimelineTrack(label string, series []float64, t0Idx, width int, _ []timelineAnnotation) string {
	var sb strings.Builder
	sb.WriteString(StyleMetricNA.Render(label))

	if len(series) == 0 {
		sb.WriteString(StyleMetricNA.Render(strings.Repeat("·", width)))
		sb.WriteString("\n\n")
		return sb.String()
	}

	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var min, max float64
	min = series[0]
	max = series[0]
	for _, v := range series {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	r := max - min
	// Downsample to fit width.
	src := series
	startIdx := 0
	if len(src) > width {
		startIdx = len(src) - width
		src = src[startIdx:]
	}
	for i, v := range src {
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
		// Mark T=0 with vertical cursor character.
		if startIdx+i == t0Idx {
			sb.WriteRune('┊')
			continue
		}
		sb.WriteRune(blocks[idx])
	}
	sb.WriteString("\n")
	// Range annotation.
	rangeStr := fmt.Sprintf("%9s", "")
	rangeStr += StyleMetricNA.Render(fmt.Sprintf("min=%.1f  max=%.1f", min, max))
	sb.WriteString(rangeStr)
	sb.WriteString("\n")
	return sb.String()
}

// renderTimelineAxis renders the per-frame time axis with -1m / T=0 / +1m
// labels positioned proportionally.
func renderTimelineAxis(t0Idx, total, width int) string {
	if total == 0 || width == 0 {
		return ""
	}
	left := "-1m"
	right := "+1m"
	mid := "T=0"
	// Place T=0 marker proportional to t0Idx.
	pos := t0Idx * width / total
	var sb strings.Builder
	sb.WriteString(StyleMetricNA.Render(left))
	pad := max(pos-len(left)-len(mid)/2, 1)
	sb.WriteString(strings.Repeat(" ", pad))
	sb.WriteString(StyleHeaderAmber.Render(mid))
	tail := max(width-pos-len(mid)/2-len(right), 1)
	sb.WriteString(strings.Repeat(" ", tail))
	sb.WriteString(StyleMetricNA.Render(right))
	return sb.String()
}
