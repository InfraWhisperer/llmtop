package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Tab represents a single tab in the tab bar.
type Tab struct {
	Name    string
	Key     string // keyboard shortcut (e.g., "1")
	Active  bool
	Enabled bool // false = grayed out / placeholder
}

var (
	styleTabActive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#58a6ff")).
			Bold(true).
			Underline(true)

	styleTabInactive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8b949e"))

	styleTabDisabled = lipgloss.NewStyle().
				Foreground(colorSurface)
)

// RenderTabBar renders the tab bar for the given active view.
func RenderTabBar(activeView View, hasGPU bool, width int) string {
	tabs := []Tab{
		{Name: "Workers", Key: "1", Active: activeView == ViewMain, Enabled: true},
		{Name: "GPU", Key: "2", Active: activeView == ViewGPU || activeView == ViewGPUDetail, Enabled: hasGPU},
		{Name: "KV Cache", Key: "3", Active: activeView == ViewKVCache, Enabled: true},
		{Name: "Tenants", Key: "4", Active: activeView == ViewModelGroup, Enabled: true},
		{Name: "P/D Pools", Key: "5", Active: activeView == ViewPDPools, Enabled: true},
		{Name: "Alerts", Key: "6", Active: false, Enabled: false},
	}

	var parts []string
	for _, t := range tabs {
		label := fmt.Sprintf("%s [%s]", t.Name, t.Key)
		if t.Active {
			parts = append(parts, styleTabActive.Render(label))
		} else if !t.Enabled {
			parts = append(parts, styleTabDisabled.Render(label))
		} else {
			parts = append(parts, styleTabInactive.Render(label))
		}
	}

	bar := "  " + strings.Join(parts, "  ")

	return lipgloss.NewStyle().
		Width(width).
		Render(bar)
}
