package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

var (
	styleAlertCrit = lipgloss.NewStyle().
			Background(lipgloss.Color("#FF4444")).
			Foreground(colorWhite).
			Bold(true).
			Padding(0, 1)

	styleAlertWarn = lipgloss.NewStyle().
			Background(lipgloss.Color("#FFD700")).
			Foreground(colorDark).
			Bold(true).
			Padding(0, 1)

	styleAlertInfo = lipgloss.NewStyle().
			Background(lipgloss.Color("#4FC3F7")).
			Foreground(colorDark).
			Bold(true).
			Padding(0, 1)

	styleAlertResolved = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#484f58"))

	styleAlertTime = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8b949e"))

	styleAlertTitle = lipgloss.NewStyle().
			Foreground(colorWhite).
			Bold(true)

	styleAlertDetail = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6e7681"))

	styleAlertSelected = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.Border{Left: "▌"}).
				BorderForeground(lipgloss.Color("#58a6ff")).
				PaddingLeft(0)
)

// RenderAlertsHeader renders the alerts header with severity pill counts.
func RenderAlertsHeader(crit, warn, info int, version string, intervalSec int, width int) string {
	title := StyleHeaderTitle.Render("llmtop " + version)

	alertLabel := StyleHeaderStat.Render("Alerts")

	var pills []string
	if crit > 0 {
		pills = append(pills, StyleMetricBad.Bold(true).Render(fmt.Sprintf("%d critical", crit)))
	}
	if warn > 0 {
		pills = append(pills, StyleMetricWarn.Bold(true).Render(fmt.Sprintf("%d warning", warn)))
	}
	if info > 0 {
		pills = append(pills, lipgloss.NewStyle().Foreground(colorBlue).Bold(true).Render(fmt.Sprintf("%d info", info)))
	}

	interval := StyleHeaderStat.Render(fmt.Sprintf("↻ %ds", intervalSec))
	dot := StyleHeaderDot.Render("·")

	parts := []string{" " + title, dot, alertLabel}
	for _, p := range pills {
		parts = append(parts, p)
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

// RenderAlertsList renders the alert list with active and resolved sections.
func RenderAlertsList(alerts []*metrics.Alert, selectedIdx int, width int) string {
	if len(alerts) == 0 {
		return StyleMetricNA.Render("  No alerts")
	}

	var sb strings.Builder
	for i, a := range alerts {
		row := renderAlertRow(a, i == selectedIdx, width)
		sb.WriteString(row)
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderAlertRow(a *metrics.Alert, selected bool, width int) string {
	resolved := !a.IsActive()

	// Severity badge
	badge := severityBadge(a.Severity)

	// Age / resolved time
	var ageStr string
	if resolved && a.ResolvedAt != nil {
		ago := time.Since(*a.ResolvedAt)
		ageStr = fmt.Sprintf("resolved %s ago", formatDuration(ago))
	} else {
		ageStr = formatDuration(time.Since(a.FiredAt))
	}

	// Title line
	titleText := truncate(a.Title, width-35)
	gap := width - 14 - len(titleText) - len(ageStr)
	if gap < 2 {
		gap = 2
	}

	var titleLine string
	if resolved {
		titleLine = " " + badge + "  " +
			styleAlertResolved.Render(titleText) +
			strings.Repeat(" ", gap) +
			styleAlertResolved.Render(ageStr)
	} else {
		titleLine = " " + badge + "  " +
			styleAlertTitle.Render(titleText) +
			strings.Repeat(" ", gap) +
			styleAlertTime.Render(ageStr)
	}

	// Detail line (indented under the badge)
	detailText := truncate(a.Detail, width-14)
	detailLine := "              " + styleAlertDetail.Render(detailText)

	combined := titleLine + "\n" + detailLine

	if selected {
		return styleAlertSelected.Render(combined)
	}
	return combined
}

func severityBadge(s metrics.AlertSeverity) string {
	switch s {
	case metrics.AlertCritical:
		return styleAlertCrit.Render("CRITICAL")
	case metrics.AlertWarning:
		return styleAlertWarn.Render("WARNING")
	default:
		return styleAlertInfo.Render("INFO")
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	return fmt.Sprintf("%.0fh", d.Hours())
}
