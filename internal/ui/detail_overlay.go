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
	sb.WriteString(overlayMetricLine(
		"KV GPU", fmt.Sprintf("%.0f%%", w.KVCacheUsagePct),
		"KV CPU", fmt.Sprintf("%.0f%%", w.KVCacheUsageCPUPct),
		innerWidth,
	))
	sb.WriteString(overlayMetricLine(
		"TTFT p99", fmt.Sprintf("%.0fms", w.TTFT_P99),
		"ITL p99", fmt.Sprintf("%.0fms", w.ITL_P99),
		innerWidth,
	))
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

func overlayMetricLine(label1, val1, label2, val2 string, width int) string {
	col1 := StyleDetailLabel.Render(fmt.Sprintf("  %-12s", label1)) + StyleDetailValue.Render(fmt.Sprintf("%-8s", val1))
	col2 := StyleDetailLabel.Render(fmt.Sprintf("%-12s", label2)) + StyleDetailValue.Render(val2)
	return col1 + col2 + "\n"
}
