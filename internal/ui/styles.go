package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Color palette
	colorGreen   = lipgloss.Color("#00FF7F")
	colorYellow  = lipgloss.Color("#FFD700")
	colorRed     = lipgloss.Color("#FF4444")
	colorBlue    = lipgloss.Color("#4FC3F7")
	colorCyan    = lipgloss.Color("#00BCD4")
	colorMagenta = lipgloss.Color("#CE93D8")
	colorGray    = lipgloss.Color("#9E9E9E")
	colorWhite   = lipgloss.Color("#FFFFFF")
	colorDark    = lipgloss.Color("#1E1E2E")
	colorBg      = lipgloss.Color("#181825")
	colorSurface = lipgloss.Color("#313244")
	colorSubtext = lipgloss.Color("#6C7086")

	// Header styles
	StyleHeaderBar = lipgloss.NewStyle().
			Background(colorDark).
			Foreground(colorWhite).
			Bold(true).
			Padding(0, 1)

	StyleHeaderTitle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	StyleHeaderStat = lipgloss.NewStyle().
			Foreground(colorWhite)

	StyleHeaderValue = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	StyleHeaderDot = lipgloss.NewStyle().
			Foreground(colorSubtext)

	// Table styles
	StyleTableHeader = lipgloss.NewStyle().
				Foreground(colorSubtext).
				Bold(true)

	StyleTableSeparator = lipgloss.NewStyle().
				Foreground(colorSurface)

	StyleTableRowSelected = lipgloss.NewStyle().
				Background(colorSurface).
				Foreground(colorWhite)

	StyleTableRowNormal = lipgloss.NewStyle().
				Foreground(colorWhite)

	StyleTableRowOffline = lipgloss.NewStyle().
				Foreground(colorSubtext).
				Faint(true)

	// Status dots
	StyleDotOnline = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)

	StyleDotOffline = lipgloss.NewStyle().
			Foreground(colorGray)

	// Metric value styles
	StyleMetricGood = lipgloss.NewStyle().
			Foreground(colorGreen)

	StyleMetricWarn = lipgloss.NewStyle().
			Foreground(colorYellow)

	StyleMetricBad = lipgloss.NewStyle().
			Foreground(colorRed)

	StyleMetricNA = lipgloss.NewStyle().
			Foreground(colorSubtext)

	// Footer styles
	StyleFooter = lipgloss.NewStyle().
			Foreground(colorSubtext).
			Padding(0, 1)

	StyleFooterKey = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	// Detail panel styles
	StyleDetailPanel = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorWhite).
				Padding(1, 2)

	StyleDetailTitle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true).
				Underline(true)

	StyleDetailLabel = lipgloss.NewStyle().
				Foreground(colorSubtext)

	StyleDetailValue = lipgloss.NewStyle().
				Foreground(colorWhite).
				Bold(true)

	StyleDetailSection = lipgloss.NewStyle().
				Foreground(colorMagenta).
				Bold(true)

	// Help overlay styles
	StyleHelpOverlay = lipgloss.NewStyle().
				Background(colorDark).
				Foreground(colorWhite).
				Padding(1, 2).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorCyan)

	StyleHelpTitle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	StyleHelpKey = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	StyleHelpDesc = lipgloss.NewStyle().
			Foreground(colorWhite)

	// Backend badge styles
	StyleBadgeVLLM = lipgloss.NewStyle().
			Foreground(colorBlue).
			Bold(true)

	StyleBadgeSGLang = lipgloss.NewStyle().
				Foreground(colorMagenta).
				Bold(true)

	StyleBadgeLMCache = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	StyleBadgeNIM = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	StyleBadgeTGI = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	StyleBadgeTRTLLM = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	StyleBadgeTriton = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true)

	StyleBadgeLlamaCpp = lipgloss.NewStyle().
				Foreground(colorGray).
				Bold(true)

	StyleBadgeUnknown = lipgloss.NewStyle().
				Foreground(colorGray)

	// Sort indicator
	StyleSortIndicator = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)
)

// KVCacheStyle returns the appropriate style based on KV cache usage percentage.
func KVCacheStyle(pct float64) lipgloss.Style {
	if pct >= 85 {
		return StyleMetricBad
	}
	if pct >= 70 {
		return StyleMetricWarn
	}
	return StyleMetricGood
}

// TTFTStyle returns the appropriate style based on TTFT P99 in milliseconds.
func TTFTStyle(ms float64) lipgloss.Style {
	if ms >= 500 {
		return StyleMetricBad
	}
	if ms >= 200 {
		return StyleMetricWarn
	}
	return StyleMetricGood
}

// QueueStyle returns the appropriate style based on queue depth.
func QueueStyle(n int) lipgloss.Style {
	if n >= 20 {
		return StyleMetricBad
	}
	if n >= 10 {
		return StyleMetricWarn
	}
	return StyleMetricGood
}

// GPUUtilStyle returns the appropriate style based on GPU utilization percentage.
func GPUUtilStyle(pct float64) lipgloss.Style {
	if pct >= 80 {
		return StyleMetricBad
	}
	if pct >= 50 {
		return StyleMetricWarn
	}
	return StyleMetricGood
}

// GPUTempStyle returns the appropriate style based on GPU temperature in Celsius.
func GPUTempStyle(c float64) lipgloss.Style {
	if c >= 85 {
		return StyleMetricBad
	}
	if c >= 70 {
		return StyleMetricWarn
	}
	return StyleMetricGood
}

// VRAMStyle returns the appropriate style based on VRAM usage percentage.
func VRAMStyle(pct float64) lipgloss.Style {
	return KVCacheStyle(pct)
}
