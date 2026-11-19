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

func TestRenderHeader_ContainsKeyMetrics(t *testing.T) {
	summary := metrics.FleetSummary{
		TotalWorkers:            9,
		OnlineWorkers:           8,
		TotalTokPerSec:          1500,
		GenTokPerSec:            1000,
		PrefillComputeTokPerSec: 500,
		AvgCacheHit:             45.0,
		CacheHitWorkers:         6,
		AvgKVPercGPU:            71.0,
		P99TTFT:                 120.0,
		PrefillCount:            3,
		DecodeCount:             5,
	}

	result := RenderHeader(summary, "vdev", 2, 200, 0, time.Time{})

	// The blended fleet rate is gone by design: one number that mixes decode
	// throughput with prompt throughput is what made #15 unreadable. The header
	// shows each side under its own label instead, so the assertions are per
	// segment.
	for _, want := range []string{"llmtop", "vdev", "9 workers", "gen 1.0k tok/s", "prefill 500 tok/s", "45%", "120ms", "3P", "5D"} {
		if !strings.Contains(result, want) {
			t.Errorf("header missing %q", want)
		}
	}

	// The cache-hit pill must read the hit rate, not KV occupancy: with the two
	// set apart here, only the hit rate may appear.
	if strings.Contains(result, "71%") {
		t.Errorf("header rendered KV occupancy (%.0f%%) as the cache hit rate", summary.AvgKVPercGPU)
	}
}

func TestRenderHeader_CachedSegmentOnlyWhenSplitReported(t *testing.T) {
	split := metrics.FleetSummary{
		TotalWorkers:            2,
		OnlineWorkers:           2,
		GenTokPerSec:            150,
		PrefillComputeTokPerSec: 900,
		PrefillCacheTokPerSec:   25000,
		SplitWorkers:            1,
	}

	wide := RenderHeader(split, "vdev", 2, 200, 0, time.Time{})
	if !strings.Contains(wide, "cached 25.0k tok/s") {
		t.Errorf("split fleet should render the cached segment, got %q", wide)
	}

	// Same numbers, but no worker reports the split (vLLM / NIM / TGI …): a
	// "cached 0 tok/s" segment would describe a metric that was never sent.
	unsplit := split
	unsplit.SplitWorkers = 0
	unsplit.PrefillCacheTokPerSec = 0
	unsplit.PrefillComputeTokPerSec = 25900
	out := RenderHeader(unsplit, "vdev", 2, 200, 0, time.Time{})
	if strings.Contains(out, "cached") {
		t.Errorf("backend without a split must not render a cached segment, got %q", out)
	}
}

func TestRenderHeader_NarrowDropsCachedWithoutFoldingItBack(t *testing.T) {
	// The richest tier costs ~30 cells; at 110 the bar cannot afford it. Dropping
	// to gen+prefill must not push the reused tokens back into "prefill" — that
	// fold is the number #15 exists to stop showing.
	summary := metrics.FleetSummary{
		TotalWorkers:            1,
		OnlineWorkers:           1,
		TotalTokPerSec:          1050,
		GenTokPerSec:            150,
		PrefillComputeTokPerSec: 900,
		PrefillCacheTokPerSec:   25000,
		SplitWorkers:            1,
	}

	wide := RenderHeader(summary, "vdev", 2, 200, 0, time.Time{})
	narrow := RenderHeader(summary, "vdev", 2, 110, 0, time.Time{})

	if !strings.Contains(wide, "cached") {
		t.Fatalf("expected the wide header to carry the cached segment: %q", wide)
	}
	if strings.Contains(narrow, "cached") {
		t.Fatalf("expected the narrow header to drop the cached segment: %q", narrow)
	}
	for _, forbidden := range []string{"25.9k", "26.0k", "25900"} {
		if strings.Contains(narrow, forbidden) {
			t.Errorf("narrow header folded reused tokens back into prefill (found %q): %q", forbidden, narrow)
		}
	}
	if !strings.Contains(narrow, "prefill 900 tok/s") {
		t.Errorf("narrow header should keep prefill at its computed 900 tok/s: %q", narrow)
	}
}

func TestRenderHeader_CacheHitPillDistinguishesNoDataFromZero(t *testing.T) {
	noData := metrics.FleetSummary{TotalWorkers: 1, OnlineWorkers: 1}
	if out := RenderHeader(noData, "vdev", 2, 200, 0, time.Time{}); strings.Contains(out, "cache hit 0%") {
		t.Errorf("a fleet with no cache-hit metric rendered a 0%% pill (claims the cache never hits): %q", out)
	}

	realZero := metrics.FleetSummary{TotalWorkers: 1, OnlineWorkers: 1, CacheHitWorkers: 1}
	if out := RenderHeader(realZero, "vdev", 2, 200, 0, time.Time{}); !strings.Contains(out, "cache hit 0%") {
		t.Errorf("a worker that reports 0%% reuse must still render 0%%: %q", out)
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
