package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func init() {
	// Respect NO_COLOR convention: https://no-color.org/
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

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

	// Role colors
	colorRolePrefill = lipgloss.Color("#79c0ff")
	colorRoleDecode  = lipgloss.Color("#3fb950")
	colorRoleMono    = lipgloss.Color("#b388ff")

	// KV bar colors
	colorKVBarLow  = lipgloss.Color("#58a6ff") // <75%
	colorKVBarMid  = lipgloss.Color("#e3b341") // 75-89%
	colorKVBarHigh = lipgloss.Color("#f85149") // ≥90%

	// Pool separator color
	colorSeparator = lipgloss.Color("#484f58")

	// Header styles
	StyleHeaderBar = lipgloss.NewStyle().
			Background(colorDark).
			Foreground(colorWhite)

	StyleHeaderTitle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true).
				Inherit(StyleHeaderBar)

	StyleHeaderStat = lipgloss.NewStyle().
			Foreground(colorWhite).
			Inherit(StyleHeaderBar)

	StyleHeaderValue = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true).
				Inherit(StyleHeaderBar)

	StyleHeaderDot = lipgloss.NewStyle().
			Foreground(colorSubtext).
			Inherit(StyleHeaderBar)

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

	StyleBadgeLiteLLM = lipgloss.NewStyle().
				Foreground(colorMagenta).
				Bold(true)

	StyleBadgeOllama = lipgloss.NewStyle().
				Foreground(colorWhite).
				Bold(true)

	StyleBadgeUnknown = lipgloss.NewStyle().
				Foreground(colorGray)

	// Sort indicator
	StyleSortIndicator = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)

	// Pool separator row
	StylePoolSeparator = lipgloss.NewStyle().
				Foreground(colorSeparator).
				Faint(true)

	// Role badge styles
	StyleRolePrefill = lipgloss.NewStyle().
				Foreground(colorRolePrefill).
				Bold(true)

	StyleRoleDecode = lipgloss.NewStyle().
			Foreground(colorRoleDecode).
			Bold(true)

	StyleRoleMono = lipgloss.NewStyle().
			Foreground(colorRoleMono).
			Bold(true)

	// Header amber pill
	StyleHeaderAmber = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true).
				Inherit(StyleHeaderBar)

	// Header red pill
	StyleHeaderRed = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true).
			Inherit(StyleHeaderBar)
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

// RoleStyle returns the appropriate style for a worker role.
func RoleStyle(role string) lipgloss.Style {
	switch role {
	case "prefill":
		return StyleRolePrefill
	case "decode":
		return StyleRoleDecode
	default:
		return StyleRoleMono
	}
}

// KVBarColor returns the lipgloss color for a KV cache bar segment.
func KVBarColor(pct float64) lipgloss.Color {
	if pct >= 90 {
		return colorKVBarHigh
	}
	if pct >= 75 {
		return colorKVBarMid
	}
	return colorKVBarLow
}

// RenderKVBar renders an inline bar of width chars representing a percentage (0-100).
func RenderKVBar(pct float64, width int) string {
	if pct <= 0 {
		return lipgloss.NewStyle().Foreground(colorSubtext).Render(strings.Repeat("─", width))
	}
	filled := min(int(pct/100*float64(width)), width)
	if filled < 1 && pct > 0 {
		filled = 1
	}
	empty := width - filled
	color := KVBarColor(pct)
	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	if empty > 0 {
		bar += lipgloss.NewStyle().Foreground(colorSurface).Render(strings.Repeat("░", empty))
	}
	return bar
}
