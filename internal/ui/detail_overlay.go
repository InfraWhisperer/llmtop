package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

var styleOverlayBorder = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorCyan).
	Padding(1, 2)

// RenderDetailOverlay renders a kubectl-describe-style overlay for a worker.
func RenderDetailOverlay(w *metrics.WorkerMetrics, gpus []*metrics.GPUInfo, width, height int) string {
	if w == nil {
		return ""
	}

	innerWidth := width - 8
	if innerWidth < 60 {
		innerWidth = 60
	}
	innerHeight := height - 6
	if innerHeight < 20 {
		innerHeight = 20
	}

	var sb strings.Builder

	// Title
	title := fmt.Sprintf("Worker detail: %s", w.Label)
	sb.WriteString(StyleDetailTitle.Render(title))
	sb.WriteString("\n\n")

	// Identity section
	sb.WriteString(overlayRow("Pod", w.Label, innerWidth))
	if strings.HasPrefix(w.Endpoint, "k8s://") {
		parts := strings.TrimPrefix(w.Endpoint, "k8s://")
		if idx := strings.Index(parts, "/"); idx > 0 {
			sb.WriteString(overlayRow("Namespace", parts[:idx], innerWidth))
		}
	}
	sb.WriteString(overlayRow("Node", orDash(w.NodeName), innerWidth))
	role := w.Role
	if role == "" {
		role = "mono"
	}
	sb.WriteString(overlayRow("Role", role, innerWidth))
	sb.WriteString(overlayRow("Backend", string(w.Backend), innerWidth))
	sb.WriteString(overlayRow("Model", orDash(w.ModelName), innerWidth))

	// Find GPU for this worker
	var workerGPU *metrics.GPUInfo
	for _, g := range gpus {
		if g.Hostname == w.NodeName && g.Pod != "" && strings.Contains(w.Label, g.Pod) {
			workerGPU = g
			break
		}
	}
	if workerGPU != nil {
		sb.WriteString(overlayRow("GPU", fmt.Sprintf("%d (%s)", workerGPU.Index, workerGPU.Name), innerWidth))
	}
	sb.WriteString("\n")

	// Metrics section
	sb.WriteString(StyleDetailSection.Render("Metrics (live)"))
	sb.WriteString("\n")

	// F1: KV CPU + KV NVMe rendered side-by-side when NVMe is present.
	if w.KVCacheUsageNVMePct > 0 {
		sb.WriteString(overlayMetricLine(
			"KV GPU", fmt.Sprintf("%.0f%%", w.KVCacheUsagePct),
			"KV CPU", fmt.Sprintf("%.0f%%", w.KVCacheUsageCPUPct),
			innerWidth,
		))
		sb.WriteString(overlayMetricLine(
			"KV NVMe", fmt.Sprintf("%.0f%%", w.KVCacheUsageNVMePct),
			"", "",
			innerWidth,
		))
	} else {
		sb.WriteString(overlayMetricLine(
			"KV GPU", fmt.Sprintf("%.0f%%", w.KVCacheUsagePct),
			"KV CPU", fmt.Sprintf("%.0f%%", w.KVCacheUsageCPUPct),
			innerWidth,
		))
	}
	sb.WriteString(overlayMetricLine(
		"Tok/s", fmt.Sprintf("%.0f", w.PromptTokPerSec+w.GenTokPerSec),
		"Queue", fmt.Sprintf("%d", w.RequestsWaiting),
		innerWidth,
	))
	sb.WriteString(overlayMetricLine(
		"Hit rate", fmt.Sprintf("%.0f%%", w.CacheHitRatePct),
		"Evict/s", fmt.Sprintf("%.0f", w.EvictPerSec),
		innerWidth,
	))
	sb.WriteString("\n")

	// F2: Latency block — P50/P95/P99 + 10-char CDF sparkline per metric.
	sb.WriteString(StyleDetailSection.Render("Latency"))
	sb.WriteString("\n")
	sb.WriteString(renderLatencyBlock("TTFT", w.TTFT, innerWidth))
	sb.WriteString(renderLatencyBlock("ITL ", w.ITL, innerWidth))
	sb.WriteString("\n")

	// GPU section
	if workerGPU != nil {
		sb.WriteString(StyleDetailSection.Render("GPU (DCGM)"))
		sb.WriteString("\n")
		sb.WriteString(overlayMetricLine(
			"SM util", fmt.Sprintf("%.0f%%", workerGPU.UtilPct),
			"Mem BW", fmt.Sprintf("%.0f%%", workerGPU.MemBWUtil),
			innerWidth,
		))
		sb.WriteString(overlayMetricLine(
			"Temp", fmt.Sprintf("%.0f°C", workerGPU.TempC),
			"Power", fmt.Sprintf("%.0fW", workerGPU.PowerW),
			innerWidth,
		))
		sb.WriteString(overlayMetricLine(
			"NVLink", fmt.Sprintf("%.0f GB/s", workerGPU.NVLinkGBs),
			"ECC", fmt.Sprintf("%d", workerGPU.ECCErrors),
			innerWidth,
		))
		sb.WriteString("\n")
	}

	// Footer
	sb.WriteString(StyleFooter.Render("[ESC] close"))

	content := sb.String()

	overlay := styleOverlayBorder.
		Width(innerWidth).
		MaxHeight(innerHeight).
		Render(content)

	return lipgloss.PlaceHorizontal(width, lipgloss.Center,
		lipgloss.PlaceVertical(height, lipgloss.Center, overlay),
	)
}

func overlayRow(label, value string, width int) string {
	return "  " + StyleDetailLabel.Render(fmt.Sprintf("%-14s", label)) +
		StyleDetailValue.Render(value) + "\n"
}

// cdfBlocks is the 8-step bar set used by both the legacy sparkline and the
// F2 CDF sparkline. The CDF mapping is monotonic so only the rising face of
// the bar is meaningful.
var cdfBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// renderCDFSparkline renders a 10-character bar visualization of the CDF
// fractions. Each fraction in [0,1] maps to one of 8 block heights; an
// all-zero array (data unavailable) produces 10 baseline bars.
func renderCDFSparkline(cdf [10]float64) string {
	var sb strings.Builder
	for _, f := range cdf {
		if f < 0 {
			f = 0
		}
		if f > 1 {
			f = 1
		}
		idx := int(f * float64(len(cdfBlocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(cdfBlocks) {
			idx = len(cdfBlocks) - 1
		}
		sb.WriteRune(cdfBlocks[idx])
	}
	return StyleMetricGood.Render(sb.String())
}

// formatLatencyMs returns "<n>ms" for a positive value or "—" for zero, matching
// the spec's all-zero invariant for absent quantiles.
func formatLatencyMs(ms float64) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0fms", ms)
}

// renderLatencyBlock renders one metric's three-quantile line plus its CDF
// sparkline beneath. When P99 is zero, the values render as "—" and the
// sparkline row is suppressed (per spec edge case 6 + invariant).
//
// The label column is fixed at 6 chars to keep TTFT and ITL aligned. Caller
// passes "TTFT" or "ITL " (trailing space) so the visual register matches.
//
// When innerWidth < 80, the sparkline row is omitted to avoid wrapping.
func renderLatencyBlock(label string, ls metrics.LatencySnapshot, innerWidth int) string {
	labelStyle := StyleDetailLabel
	valueStyle := StyleDetailValue

	headerCol := func(name, val string) string {
		return labelStyle.Render(fmt.Sprintf("%-4s", name)) + " " + valueStyle.Render(fmt.Sprintf("%-8s", val))
	}

	line := "  " + labelStyle.Render(fmt.Sprintf("%-6s", label)) + " " +
		headerCol("P50", formatLatencyMs(ls.P50)) + "  " +
		headerCol("P95", formatLatencyMs(ls.P95)) + "  " +
		headerCol("P99", formatLatencyMs(ls.P99))

	// All-zero invariant: nothing real to plot, skip the sparkline row.
	if ls.P99 <= 0 {
		return line + "\n"
	}
	// Narrow terminals: omit the sparkline row entirely (no truncation hack).
	if innerWidth < 80 {
		return line + "\n"
	}
	spark := "         " + renderCDFSparkline(ls.CDFPoints)
	return line + "\n" + spark + "\n"
}

func overlayMetricLine(label1, val1, label2, val2 string, width int) string {
	col1 := StyleDetailLabel.Render(fmt.Sprintf("  %-12s", label1)) + StyleDetailValue.Render(fmt.Sprintf("%-8s", val1))
	col2 := StyleDetailLabel.Render(fmt.Sprintf("%-12s", label2)) + StyleDetailValue.Render(val2)
	return col1 + col2 + "\n"
}
