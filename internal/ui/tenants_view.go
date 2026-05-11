package ui

import (
	"fmt"
	"strings"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/charmbracelet/lipgloss"
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
	cardWidth := max((width-8)/3, 20)

	var cards []string
	for _, g := range groups {
		cards = append(cards, renderTenantCard(g, cardWidth))
	}

	// Arrange cards 3 per row
	var rows []string
	for i := 0; i < len(cards); i += 3 {
		end := min(i+3, len(cards))
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

// renderTenantCard renders a single tenant card with metrics fields. When
// the model group has TopTenants populated (F3), it appends a "Top tenants"
// sub-block with up to 5 rows ranked by request share.
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

	// F3: top-tenants block. Skip silently when TopTenants is empty so the
	// card layout matches the pre-feature placeholder for backends that
	// don't emit request_tag (TGI, NIM, etc).
	if len(g.TopTenants) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render("Top tenants"))
		// Inner width inside the card border (Padding(1,2) eats 4 cols).
		inner := max(cardWidth-4, 20)
		for _, t := range tenantRowsForCard(g.TopTenants) {
			lines = append(lines, renderTenantRow(t, inner))
		}
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Width(cardWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#21262d")).
		Padding(1, 2).
		Render(content)
}

// tenantRowsForCard returns up to 5 tenants from the (already-sorted) list,
// defensively trimming if the contract is violated upstream.
func tenantRowsForCard(tt []metrics.TenantMetrics) []metrics.TenantMetrics {
	if len(tt) > 5 {
		return tt[:5]
	}
	return tt
}

// renderTenantRow renders a single tenant row in the format:
//
//	team-search    35%   1.2k tok/s
//
// The TAG column flexes to fill the card; SHARE% and TOK/S are
// right-aligned. The "⚠ TTFT" indicator is intentionally omitted: per
// spec Open Q §1, vLLM does not emit per-request_tag histograms, so any
// per-tenant TTFT P99 would be synthesized — explicitly out of scope.
func renderTenantRow(t metrics.TenantMetrics, innerWidth int) string {
	share := fmt.Sprintf("%.0f%%", t.RequestShare*100)
	tok := formatTenantTokPerSec(t.TokPerSec) + " tok/s"
	// 4-char share column + spacer + tok column = right-side reserved width.
	rhs := share + "  " + tok
	tagW := max(innerWidth-len(rhs)-2, 6)
	tag := truncate(t.Tag, tagW)
	tagPad := padRight(tag, tagW)
	tagStyle := lipgloss.NewStyle().Foreground(colorWhite)
	shareStyle := lipgloss.NewStyle().Foreground(colorYellow)
	tokStyle := lipgloss.NewStyle().Foreground(colorGreen)
	return tagStyle.Render(tagPad) + "  " + shareStyle.Render(share) + "  " + tokStyle.Render(tok)
}
