package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// RenderPDPoolsView renders the P/D Pools tab showing prefill/decode pool health.
func RenderPDPoolsView(workers []*metrics.WorkerMetrics, width int) string {
	var prefill, decode []*metrics.WorkerMetrics
	for _, w := range workers {
		if !w.Online {
			continue
		}
		switch w.Role {
		case "prefill":
			prefill = append(prefill, w)
		case "decode":
			decode = append(decode, w)
		}
	}

	if len(prefill) == 0 && len(decode) == 0 {
		msg := "No P/D disaggregation detected"
		pad := max((width-len(msg))/2, 0)
		return "\n" + strings.Repeat(" ", pad) + StyleMetricNA.Render(msg) + "\n"
	}

	var sections []string
	sections = append(sections, renderPoolRatioBar(len(prefill), len(decode), width))
	sections = append(sections, renderPrefillPool(prefill, width))
	sections = append(sections, renderDecodeAndHealth(prefill, decode, width))

	return strings.Join(sections, "\n\n")
}

// renderPoolRatioBar renders the POOL RATIO section with a horizontal split bar.
func renderPoolRatioBar(nPrefill, nDecode, width int) string {
	header := StyleDetailSection.Render("POOL RATIO")

	barWidth := max(width-4, 10)

	total := nPrefill + nDecode
	prefillPct := 0
	decodePct := 0
	prefillChars := 0
	if total > 0 {
		prefillPct = nPrefill * 100 / total
		decodePct = 100 - prefillPct
		prefillChars = nPrefill * barWidth / total
	}
	decodeChars := barWidth - prefillChars

	colorPrefillBar := lipgloss.Color("#1a3a6e")
	colorDecodeBar := lipgloss.Color("#1a4429")

	bar := lipgloss.NewStyle().Foreground(colorPrefillBar).Render(strings.Repeat("█", prefillChars)) +
		lipgloss.NewStyle().Foreground(colorDecodeBar).Render(strings.Repeat("█", decodeChars))

	prefillLabel := lipgloss.NewStyle().Foreground(colorRolePrefill).Render(
		fmt.Sprintf("prefill %d workers (%d%%)", nPrefill, prefillPct),
	)
	decodeLabel := lipgloss.NewStyle().Foreground(colorRoleDecode).Render(
		fmt.Sprintf("decode %d workers (%d%%)", nDecode, decodePct),
	)

	return "  " + header + "\n  " + bar + "\n  " + prefillLabel + "    " + decodeLabel
}

// renderPrefillPool renders the PREFILL POOL section with per-worker cards.
func renderPrefillPool(prefill []*metrics.WorkerMetrics, width int) string {
	header := StyleDetailSection.Render("PREFILL POOL")

	if len(prefill) == 0 {
		return "  " + header + "\n  " + StyleMetricNA.Render("no prefill workers")
	}

	cardWidth := max((width-8)/3, 20)

	var rows []string
	for i := 0; i < len(prefill); i += 3 {
		end := min(i+3, len(prefill))
		var cards []string
		for _, w := range prefill[i:end] {
			cards = append(cards, renderPrefillCard(w, cardWidth))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cards...))
	}

	return "  " + header + "\n" + strings.Join(rows, "\n")
}

// renderPrefillCard renders a single prefill worker card with rounded border.
func renderPrefillCard(w *metrics.WorkerMetrics, cardWidth int) string {
	borderColor := lipgloss.Color("#21262d")
	if w.RequestsWaiting > 10 {
		borderColor = lipgloss.Color("#FF4444")
	} else if w.RequestsWaiting > 5 || w.TTFT_P99 > 2000 {
		borderColor = lipgloss.Color("#FFD700")
	}

	innerWidth := max(cardWidth-4, 10)

	// Title
	name := workerShortName(w)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	if w.RequestsWaiting > 10 {
		titleStyle = titleStyle.Foreground(colorRed)
		name = name + "!"
	}
	title := titleStyle.Render(truncate(name, innerWidth))

	// Fields
	lines := []string{title}
	lines = append(lines, cardRow("Queue depth", fmt.Sprintf("%d", w.RequestsWaiting), innerWidth))
	lines = append(lines, cardRow("Active reqs", fmt.Sprintf("%d", w.RequestsRunning), innerWidth))

	ttftStr := formatMsValue(w.TTFT_P99)
	ttftStyled := ttftStr
	if w.TTFT_P99 > 5000 {
		ttftStyled = StyleMetricBad.Render(ttftStr)
	} else if w.TTFT_P99 > 2000 {
		ttftStyled = StyleMetricWarn.Render(ttftStr)
	}
	lines = append(lines, cardRowStyled("TTFT p99", ttftStyled, innerWidth))

	// F6: real KV transfer P99 in ms; "—" when absent.
	xferStr := "—"
	if w.KVTransferP99Ms > 0 {
		xferStr = formatMsValue(w.KVTransferP99Ms)
	}
	lines = append(lines, cardRow("KV xfer lat", xferStr, innerWidth))

	tokTotal := w.PromptTokPerSec + w.GenTokPerSec
	lines = append(lines, cardRow("Tok/s out", formatThroughput(tokTotal), innerWidth))

	content := strings.Join(lines, "\n")

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardWidth-2).
		Padding(0, 1)

	return cardStyle.Render(content)
}

// renderDecodeAndHealth renders DECODE POOL AGGREGATE (left) and P/D health (right).
func renderDecodeAndHealth(prefill, decode []*metrics.WorkerMetrics, width int) string {
	leftWidth := (width - 4) * 60 / 100
	rightWidth := (width - 4) - leftWidth

	left := renderDecodeAggregate(decode, leftWidth)
	right := renderPDHealth(prefill, decode, rightWidth)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// renderDecodeAggregate renders the DECODE POOL AGGREGATE card.
func renderDecodeAggregate(decode []*metrics.WorkerMetrics, cardWidth int) string {
	innerWidth := max(cardWidth-4, 10)

	title := StyleDetailSection.Render("DECODE POOL AGGREGATE")

	nDecode := len(decode)
	onlineCount := 0
	var totalQueue int
	var itlSum float64
	var itlCount int
	var kvSum float64
	var kvCount int
	var hotName string
	var hotITL float64

	for _, w := range decode {
		if w.Online {
			onlineCount++
		}
		totalQueue += w.RequestsWaiting
		if w.ITL_P99 > 0 {
			itlSum += w.ITL_P99
			itlCount++
		}
		if w.KVCacheUsagePct > 0 {
			kvSum += w.KVCacheUsagePct
			kvCount++
		}
		if w.ITL_P99 > hotITL {
			hotITL = w.ITL_P99
			hotName = workerShortName(w)
		}
	}

	var lines []string

	// Workers healthy
	healthStr := fmt.Sprintf("%d workers healthy", onlineCount)
	if onlineCount == nDecode {
		lines = append(lines, StyleMetricGood.Render(healthStr))
	} else {
		lines = append(lines, StyleMetricWarn.Render(healthStr))
	}

	lines = append(lines, cardRow("Total queue", fmt.Sprintf("%d", totalQueue), innerWidth))

	avgITL := 0.0
	if itlCount > 0 {
		avgITL = itlSum / float64(itlCount)
	}
	lines = append(lines, cardRow("Avg ITL p99", formatMsValue(avgITL), innerWidth))

	// Hot ITL outlier
	if hotName != "" && hotITL > 0 {
		hotStr := fmt.Sprintf("%s %s", hotName, formatMsValue(hotITL))
		lines = append(lines, cardRowStyled("Hot ITL outlier", StyleMetricBad.Render(hotStr), innerWidth))
	} else {
		lines = append(lines, cardRow("Hot ITL outlier", "-", innerWidth))
	}

	avgKV := 0.0
	if kvCount > 0 {
		avgKV = kvSum / float64(kvCount)
	}
	lines = append(lines, cardRow("Avg KV fill", fmt.Sprintf("%.0f%%", avgKV), innerWidth))

	content := title + "\n" + strings.Join(lines, "\n")

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#21262d")).
		Width(cardWidth-2).
		Padding(0, 1)

	return cardStyle.Render(content)
}

// renderPDHealth renders the P/D health card.
func renderPDHealth(prefill, decode []*metrics.WorkerMetrics, cardWidth int) string {
	innerWidth := max(cardWidth-4, 10)

	title := StyleDetailSection.Render("P/D health")

	var lines []string

	// Ratio
	lines = append(lines, cardRow("Ratio", fmt.Sprintf("%d:%d", len(prefill), len(decode)), innerWidth))

	// Ideal (est)
	idealStr := computeIdealRatio(prefill, decode)
	lines = append(lines, cardRow("Ideal (est)", idealStr, innerWidth))

	// Decode slack
	slackStr, slackStyle := computeDecodeSlack(decode)
	lines = append(lines, cardRowStyled("Decode slack", slackStyle.Render(slackStr), innerWidth))

	// Prefill sat.
	satStr, satStyle := computePrefillSaturation(prefill)
	lines = append(lines, cardRowStyled("Prefill sat.", satStyle.Render(satStr), innerWidth))

	content := title + "\n" + strings.Join(lines, "\n")

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#21262d")).
		Width(cardWidth-2).
		Padding(0, 1)

	return cardStyle.Render(content)
}

// computeIdealRatio estimates the ideal prefill:decode ratio based on throughput.
func computeIdealRatio(prefill, decode []*metrics.WorkerMetrics) string {
	if len(decode) == 0 {
		return "-"
	}

	var sumPrefillTok, sumDecodeTok float64
	for _, w := range prefill {
		sumPrefillTok += w.PromptTokPerSec + w.GenTokPerSec
	}
	for _, w := range decode {
		sumDecodeTok += w.PromptTokPerSec + w.GenTokPerSec
	}

	if sumDecodeTok == 0 {
		return "-"
	}

	idealPrefill := int(math.Ceil(sumPrefillTok / sumDecodeTok))
	return fmt.Sprintf("%d:%d", idealPrefill, len(decode))
}

// computeDecodeSlack computes the decode pool slack as a percentage.
func computeDecodeSlack(decode []*metrics.WorkerMetrics) (string, lipgloss.Style) {
	if len(decode) == 0 {
		return "-", StyleMetricNA
	}

	var totalCap, totalLoad float64
	for _, w := range decode {
		load := float64(w.RequestsRunning)
		cap := float64(w.RequestsRunning + w.RequestsWaiting + 10)
		totalLoad += load
		totalCap += cap
	}

	if totalCap == 0 {
		return "-", StyleMetricNA
	}

	slack := (totalCap - totalLoad) / totalCap
	if slack < 0.2 {
		return "low", StyleMetricBad
	}
	if slack <= 0.5 {
		return "med", StyleMetricWarn
	}
	return "high", StyleMetricGood
}

// computePrefillSaturation counts prefill workers with queue depth > 5.
func computePrefillSaturation(prefill []*metrics.WorkerMetrics) (string, lipgloss.Style) {
	total := len(prefill)
	saturated := 0
	for _, w := range prefill {
		if w.RequestsWaiting > 5 {
			saturated++
		}
	}

	if saturated > 0 {
		return fmt.Sprintf("%d/%d bottleneck", saturated, total), StyleMetricBad
	}
	return fmt.Sprintf("0/%d", total), StyleMetricGood
}

// cardRow renders a label + right-aligned value within the given width.
func cardRow(label, value string, width int) string {
	labelRendered := lipgloss.NewStyle().Foreground(colorSubtext).Render(label)
	labelLen := len(label)
	valueLen := len(value)
	gap := max(width-labelLen-valueLen, 1)
	return labelRendered + strings.Repeat(" ", gap) + value
}

// cardRowStyled renders a label + right-aligned pre-styled value.
func cardRowStyled(label, styledValue string, width int) string {
	labelRendered := lipgloss.NewStyle().Foreground(colorSubtext).Render(label)
	// styledValue contains ANSI codes so we can't measure it directly for alignment;
	// use label length to compute left padding and let lipgloss handle the rest.
	labelLen := len(label)
	gap := max(width-labelLen-estimatePlainLen(styledValue), 1)
	return labelRendered + strings.Repeat(" ", gap) + styledValue
}

// estimatePlainLen strips ANSI escape sequences to estimate visible string length.
func estimatePlainLen(s string) int {
	inEsc := false
	n := 0
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		n++
	}
	return n
}

// workerShortName extracts a short display name from the worker endpoint or label.
func workerShortName(w *metrics.WorkerMetrics) string {
	if w.Label != "" {
		return w.Label
	}
	ep := w.Endpoint
	ep = strings.TrimPrefix(ep, "http://")
	ep = strings.TrimPrefix(ep, "https://")
	ep = strings.TrimPrefix(ep, "k8s://")
	// Take just the hostname/pod portion
	if idx := strings.Index(ep, "/"); idx > 0 {
		ep = ep[:idx]
	}
	if idx := strings.Index(ep, ":"); idx > 0 {
		ep = ep[:idx]
	}
	return ep
}

// formatMsValue formats a millisecond value for display.
func formatMsValue(ms float64) string {
	if ms == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0fms", ms)
}

// formatThroughput formats tokens/sec as "X.Xk" if above 1000, otherwise plain integer.
func formatThroughput(tok float64) string {
	if tok == 0 {
		return "-"
	}
	if tok >= 1000 {
		return fmt.Sprintf("%.1fk", tok/1000)
	}
	return fmt.Sprintf("%.0f", tok)
}
