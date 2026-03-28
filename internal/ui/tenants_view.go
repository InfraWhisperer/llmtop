package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// tenantCardColors maps a first-letter hash to a color for tenant card titles.
var tenantCardColors = []lipgloss.Color{colorGreen, colorCyan, colorYellow, colorMagenta}

// tenantCardColor picks a deterministic color from the model/tenant name.
func tenantCardColor(name string) lipgloss.Color {
	if len(name) == 0 {
		return colorCyan
	}
	return tenantCardColors[int(name[0])%len(tenantCardColors)]
}

// RenderTenantsView renders the Tenants tab content. When no tenant data is
// available (empty groups), it shows a centered placeholder card explaining
// how to enable tenant labels. Otherwise it renders a 3-column card grid with
// one card per model group as a tenant proxy.
func RenderTenantsView(groups []metrics.ModelGroup, width int) string {
	if len(groups) == 0 {
		return renderTenantPlaceholder(width)
	}
	return renderTenantCards(groups, width)
}

// renderTenantPlaceholder renders a centered card explaining that no tenant
// labels were detected and how to enable them.
func renderTenantPlaceholder(width int) string {
	const cardWidth = 50

	titleStyle := lipgloss.NewStyle().
		Foreground(colorYellow).
		Bold(true)
	bodyStyle := lipgloss.NewStyle().
		Foreground(colorSubtext)

	title := titleStyle.Render("No tenant labels detected")
	line1 := bodyStyle.Render("Set VLLM_REQUEST_TAG env var on your vLLM")
	line2 := bodyStyle.Render("deployment, or pass X-Request-Tag header.")

	content := title + "\n\n" + line1 + "\n" + line2

	card := lipgloss.NewStyle().
		Width(cardWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#21262d")).
		Padding(1, 2).
		Render(content)

	return lipgloss.Place(width, lipgloss.Height(card)+2,
		lipgloss.Center, lipgloss.Center,
		card)
}

// renderTenantCards renders a 3-column grid of tenant cards, one per model group.
func renderTenantCards(groups []metrics.ModelGroup, width int) string {
	cardWidth := (width - 8) / 3
	if cardWidth < 20 {
		cardWidth = 20
	}

	var cards []string
	for _, g := range groups {
		cards = append(cards, renderTenantCard(g, cardWidth))
	}

	// Arrange cards 3 per row
	var rows []string
	for i := 0; i < len(cards); i += 3 {
		end := i + 3
		if end > len(cards) {
			end = len(cards)
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top, cards[i:end]...)
		rows = append(rows, row)
	}

	return strings.Join(rows, "\n")
}

// formatTenantTokPerSec formats tok/s for tenant cards, using "X.Xk" for values >1000.
func formatTenantTokPerSec(tok float64) string {
	if tok == 0 {
		return "-"
	}
	if tok > 1000 {
		return fmt.Sprintf("%.1fk", tok/1000)
	}
	return fmt.Sprintf("%.0f", tok)
}

// formatTenantTTFT formats TTFT P99 in milliseconds for tenant cards.
func formatTenantTTFT(ms float64) string {
	if ms == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0fms", ms)
}

// formatTenantHitRate formats cache hit rate as a percentage for tenant cards.
func formatTenantHitRate(pct float64) string {
	if pct == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

// renderTenantCard renders a single tenant card with metrics fields.
func renderTenantCard(g metrics.ModelGroup, cardWidth int) string {
	titleColor := tenantCardColor(g.ModelName)
	titleStyle := lipgloss.NewStyle().
		Foreground(titleColor).
		Bold(true)

	labelStyle := lipgloss.NewStyle().Foreground(colorSubtext)
	valueStyle := lipgloss.NewStyle().Foreground(colorWhite)

	title := titleStyle.Render(truncate(g.ModelName, cardWidth-6))

	fields := []struct {
		label string
		value string
	}{
		{"GPU-hrs (5m)", "-"},
		{"Req/s", fmt.Sprintf("%d", g.TotalRunning)},
		{"TTFT p99", formatTenantTTFT(g.AvgTTFTP99)},
		{"Tok/s", formatTenantTokPerSec(g.TotalTokPerSec)},
		{"Est cost/hr", "-"},
		{"Cache hit", formatTenantHitRate(g.AvgHitRate)},
	}

	// Compute label column width for alignment
	maxLabel := 0
	for _, f := range fields {
		if len(f.label) > maxLabel {
			maxLabel = len(f.label)
		}
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for _, f := range fields {
		l := labelStyle.Render(padRight(f.label, maxLabel))
		v := valueStyle.Render(f.value)
		lines = append(lines, l+"  "+v)
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Width(cardWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#21262d")).
		Padding(1, 2).
		Render(content)
}
