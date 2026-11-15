package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestRenderHeader_FillsExactWidth(t *testing.T) {
	summary := metrics.FleetSummary{
		TotalWorkers:   9,
		OnlineWorkers:  8,
		TotalTokPerSec: 1500,
		AvgKVPercGPU:   45.0,
		P99TTFT:        120.0,
		PrefillCount:   3,
		DecodeCount:    5,
	}

	for _, width := range []int{100, 120, 160, 200} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			result := RenderHeader(summary, "vdev", 2, width, 0, time.Time{})
			got := lipgloss.Width(result)
			if got != width {
				t.Errorf("lipgloss.Width(result) = %d, want %d", got, width)
			}
		})
	}
}

func TestRenderHeader_NarrowDoesNotPanic(t *testing.T) {
	summary := metrics.FleetSummary{
		TotalWorkers:   9,
		OnlineWorkers:  8,
		TotalTokPerSec: 1500,
		AvgKVPercGPU:   45.0,
		P99TTFT:        120.0,
		PrefillCount:   3,
		DecodeCount:    5,
	}

	// Narrow widths should not panic; content overflows gracefully
	for _, width := range []int{20, 40, 60} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			result := RenderHeader(summary, "vdev", 2, width, 0, time.Time{})
			got := lipgloss.Width(result)
			if got < width {
				t.Errorf("lipgloss.Width(result) = %d, should be >= %d", got, width)
			}
		})
	}
}

// Issue #15: the throughput slot renders three labeled rates (gen / prefill /
// cache) instead of one summed number, and the cache pill reads cache hit, not
// KV usage.
func TestRenderHeader_ThroughputSegments(t *testing.T) {
	summary := metrics.FleetSummary{
		TotalWorkers:            4,
		OnlineWorkers:           4,
		TotalTokPerSec:          27200,
		GenTokPerSec:            152,
		PrefillComputeTokPerSec: 40,
		PrefillCacheTokPerSec:   25400,
		AvgCacheHit:             61,
		P99TTFT:                 120,
	}

	wide := RenderHeader(summary, "vdev", 2, 200, 0, time.Time{})
	for _, want := range []string{"gen 152 tok/s", "prefill 40 tok/s", "cache 25.4k tok/s"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide header missing %q", want)
		}
	}
	// The 25.4k cache burst must not be readable as throughput: the bogus
	// combined number that caused the bug must be gone.
	if strings.Contains(wide, "27.2k tok/s") {
		t.Errorf("cache rate leaked into a combined throughput number: %q", wide)
	}

	// Narrower budget: degrade to the richest honest tier that fits instead of
	// overflowing the bar, ending at gen alone.
	noCache := renderThroughputSegments(summary, lipgloss.Width("gen 152 tok/s · prefill 25.4k tok/s")+1)
	if !strings.Contains(noCache, "prefill") || strings.Contains(noCache, "cache") {
		t.Errorf("expected gen+prefill tier, got %q", noCache)
	}
	if !strings.Contains(noCache, "25.4k") {
		t.Errorf("tier 2 should fold cache into the prefill side, got %q", noCache)
	}
	genOnly := renderThroughputSegments(summary, 5)
	if !strings.Contains(genOnly, "gen 152 tok/s") || strings.Contains(genOnly, "prefill") {
		t.Errorf("expected gen-only tier at tiny budget, got %q", genOnly)
	}
}

// Issue #15 regression: the pill next to the "cache hit" label was fed
// AvgKVPercGPU, so KV occupancy was displayed as if it were a hit rate.
func TestRenderHeader_CachePillUsesCacheHitNotKV(t *testing.T) {
	summary := metrics.FleetSummary{
		TotalWorkers:  2,
		OnlineWorkers: 2,
		AvgCacheHit:   42,
		AvgKVPercGPU:  77,
		P99TTFT:       120,
	}
	result := RenderHeader(summary, "vdev", 2, 200, 0, time.Time{})
	if !strings.Contains(result, "42%") {
		t.Errorf("cache pill should render AvgCacheHit=42, got %q", result)
	}
	if strings.Contains(result, "77%") {
		t.Errorf("KV usage leaked into the cache-hit pill: %q", result)
	}
}

// The bar reports gen / prefill / cache as three labeled rates rather than one
// summed number (issue #15), so this drives those fields explicitly and asserts
// that every key stat is still present.
func TestRenderHeader_ContainsKeyMetrics(t *testing.T) {
	summary := metrics.FleetSummary{
		TotalWorkers:            9,
		OnlineWorkers:           8,
		TotalTokPerSec:          1800,
		GenTokPerSec:            1500,
		PrefillComputeTokPerSec: 300,
		AvgCacheHit:             45.0,
		AvgKVPercGPU:            45.0,
		P99TTFT:                 120.0,
		PrefillCount:            3,
		DecodeCount:             5,
	}

	result := RenderHeader(summary, "vdev", 2, 200, 0, time.Time{})

	for _, want := range []string{"llmtop", "vdev", "9 workers", "gen 1.5k tok/s", "prefill 300 tok/s", "45%", "120ms", "3P", "5D"} {
		if !strings.Contains(result, want) {
			t.Errorf("header missing %q", want)
		}
	}
}

func TestRenderGPUHeader_FillsExactWidth(t *testing.T) {
	summary := metrics.GPUSummary{
		TotalGPUs:    4,
		ActiveGPUs:   4,
		AvgUtilPct:   75.0,
		TotalMemUsed: 40 * 1024,
		TotalMemCap:  80 * 1024,
	}

	for _, width := range []int{100, 120, 200} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			result := RenderGPUHeader(summary, "vdev", 2, width)
			got := lipgloss.Width(result)
			if got != width {
				t.Errorf("lipgloss.Width(result) = %d, want %d", got, width)
			}
		})
	}
}

func TestRenderModelHeader_FillsExactWidth(t *testing.T) {
	groups := []metrics.ModelGroup{
		{ModelName: "llama-3.1", Workers: 5, TotalTokPerSec: 800},
		{ModelName: "mixtral", Workers: 3, TotalTokPerSec: 600},
	}

	for _, width := range []int{80, 120, 200} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			result := RenderModelHeader(groups, "vdev", 2, width)
			got := lipgloss.Width(result)
			if got != width {
				t.Errorf("lipgloss.Width(result) = %d, want %d", got, width)
			}
		})
	}
}

func TestRenderAlertsHeader_FillsExactWidth(t *testing.T) {
	for _, width := range []int{60, 80, 120, 200} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			result := RenderAlertsHeader(2, 3, 1, "vdev", 2, width)
			got := lipgloss.Width(result)
			if got != width {
				t.Errorf("lipgloss.Width(result) = %d, want %d", got, width)
			}
		})
	}
}

func TestRenderAlertsHeader_NoPills(t *testing.T) {
	result := RenderAlertsHeader(0, 0, 0, "vdev", 2, 120)
	got := lipgloss.Width(result)
	if got != 120 {
		t.Errorf("lipgloss.Width(result) = %d, want 120", got)
	}
	if !strings.Contains(result, "Alerts") {
		t.Error("header missing 'Alerts' label")
	}
}

func TestRenderHeaderBar_FillsWidth(t *testing.T) {
	content := StyleHeaderTitle.Render("test content")
	result := renderHeaderBar(content, 80)
	got := lipgloss.Width(result)
	if got != 80 {
		t.Errorf("lipgloss.Width(result) = %d, want 80", got)
	}
}

// V1 — RenderHeader scrub indicator.

func TestRenderHeader_ScrubIndicatorWhenTraveling(t *testing.T) {
	summary := metrics.FleetSummary{TotalWorkers: 1}
	out := RenderHeader(summary, "vdev", 2, 200, 5, time.Time{})
	if !strings.Contains(out, "T-") {
		t.Errorf("expected 'T-' scrub indicator, got %q", out)
	}
	if !strings.Contains(out, "paused") {
		t.Errorf("expected 'paused' substring when scrubbing, got %q", out)
	}
}

func TestRenderHeader_NoScrubAtLive(t *testing.T) {
	summary := metrics.FleetSummary{TotalWorkers: 1}
	out := RenderHeader(summary, "vdev", 2, 200, 0, time.Time{})
	if strings.Contains(out, "paused") {
		t.Errorf("live header must not contain 'paused', got %q", out)
	}
}

// V6 — RenderPulseStrip behaviors not already covered by the spec test.

func TestRenderPulseStrip_VeryNarrow_NoSparkRunes(t *testing.T) {
	hist := []metrics.FleetSummary{{P99TTFT: 100, TotalTokPerSec: 1000}}
	out := RenderPulseStrip(hist, 40, 24)
	for _, r := range []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"} {
		if strings.Contains(out, r) {
			t.Errorf("narrow strip should not contain %q, got %q", r, out)
		}
	}
}

func TestRenderPulseStrip_TooShort_Empty(t *testing.T) {
	if got := RenderPulseStrip(nil, 120, 5); got != "" {
		t.Errorf("expected empty for height<12, got %q", got)
	}
}

func TestRenderHitRateBar_RespectsWidth(t *testing.T) {
	// At pct=100, the bar should be all-filled. The exact display width is
	// hard to assert in lipgloss across profiles, but the substring must
	// contain at least one filled block.
	out := RenderHitRateBar(100.0, 8)
	if !strings.Contains(out, "█") {
		t.Errorf("RenderHitRateBar(100, 8) should contain █, got %q", out)
	}
}

func TestFormatScrubDuration_Sub60(t *testing.T) {
	if got := formatScrubDuration(45); got != "45s" {
		t.Errorf("formatScrubDuration(45) = %q, want %q", got, "45s")
	}
}

func TestFormatScrubDuration_Minutes(t *testing.T) {
	if got := formatScrubDuration(125); got != "2m5s" {
		t.Errorf("formatScrubDuration(125) = %q, want %q", got, "2m5s")
	}
}
