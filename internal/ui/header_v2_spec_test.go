// Black-box contract tests for V6 (Workload Pulse — Sparkbar Strip).
// Spec: docs/feature-spec-v2.md §3 V6, §7 glossary.
//
// Pinned contracts:
//   - RenderPulseStrip with termHeight < 12 returns "".
//   - RenderPulseStrip with full size produces exactly 2 newlines (2-line strip).
//   - RenderPulseStrip with width < 60 omits sparklines but keeps "TTFT P99" text.
//   - RenderPulseStrip with width >= 90 includes "DEC", "KV%", "HIT" gauge labels.
//   - RenderHitRateBar uses inverted scale: high pct = green, low pct = red.

package ui_test

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/internal/ui"
)

// ─── RenderPulseStrip ────────────────────────────────────────────────────────

func TestRenderPulseStrip_HeightUnder12_ReturnsEmptyString(t *testing.T) {
	hist := []metrics.FleetSummary{{}, {}, {}}
	got := ui.RenderPulseStrip(hist, 120, 11)
	if got != "" {
		t.Errorf("RenderPulseStrip with termHeight<12 should return \"\", got %q", got)
	}
}

func TestRenderPulseStrip_HeightExactly11_StillEmpty(t *testing.T) {
	hist := []metrics.FleetSummary{{}}
	got := ui.RenderPulseStrip(hist, 120, 11)
	if got != "" {
		t.Errorf("RenderPulseStrip with termHeight=11 should return \"\", got %q", got)
	}
}

func TestRenderPulseStrip_HeightAtLeast12_IsTwoLines(t *testing.T) {
	hist := make([]metrics.FleetSummary, 60)
	for i := range hist {
		hist[i] = metrics.FleetSummary{}
	}
	got := ui.RenderPulseStrip(hist, 120, 24)
	if got == "" {
		t.Fatalf("RenderPulseStrip should not be empty when height>=12 and width>=60")
	}
	nl := strings.Count(got, "\n")
	if nl != 2 {
		t.Errorf("RenderPulseStrip should have exactly 2 newlines (2-line strip), got %d in %q", nl, got)
	}
}

func TestRenderPulseStrip_WideTerminal_ContainsGaugeLabels(t *testing.T) {
	hist := make([]metrics.FleetSummary, 60)
	got := ui.RenderPulseStrip(hist, 120, 24)
	for _, label := range []string{"DEC", "KV%", "HIT"} {
		if !strings.Contains(got, label) {
			t.Errorf("RenderPulseStrip width>=90 should contain %q, got %q", label, got)
		}
	}
}

func TestRenderPulseStrip_WideTerminal_ContainsTTFTLabel(t *testing.T) {
	hist := make([]metrics.FleetSummary, 60)
	got := ui.RenderPulseStrip(hist, 120, 24)
	if !strings.Contains(got, "TTFT") {
		t.Errorf("RenderPulseStrip should contain \"TTFT\" label, got %q", got)
	}
}

func TestRenderPulseStrip_WideTerminal_ContainsTokPerSecLabel(t *testing.T) {
	hist := make([]metrics.FleetSummary, 60)
	got := ui.RenderPulseStrip(hist, 120, 24)
	if !strings.Contains(got, "tok/s") {
		t.Errorf("RenderPulseStrip should contain \"tok/s\" label, got %q", got)
	}
}

// Spec: width<60 omits sparklines but keeps text labels.
func TestRenderPulseStrip_NarrowTerminal_NoSparklineGlyphs(t *testing.T) {
	hist := make([]metrics.FleetSummary, 60)
	got := ui.RenderPulseStrip(hist, 50, 24)
	// Spec edge case: at width<60 only metric names + values; no sparkline runes.
	for _, r := range []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"} {
		if strings.Contains(got, r) {
			t.Errorf("RenderPulseStrip width<60 should not contain sparkline glyph %q, got %q", r, got)
		}
	}
}

// The narrow-width strip is still a 2-line strip (TTFT row + tok/s row).
// Round-1 punted on this; the spec is unambiguous now that the impl is in
// place (renderPulseStripCompact writes two "...\n" lines).
func TestRenderPulseStrip_NarrowTerminal_IsTwoLines(t *testing.T) {
	hist := []metrics.FleetSummary{{P99TTFT: 250, TotalTokPerSec: 1200}}
	got := ui.RenderPulseStrip(hist, 50, 24)
	if got == "" {
		t.Fatalf("RenderPulseStrip at width=50 should not be empty (height>=12)")
	}
	if nl := strings.Count(got, "\n"); nl != 2 {
		t.Errorf("narrow-width pulse strip should be 2 lines (2 newlines), got %d in %q", nl, got)
	}
}

func TestRenderPulseStrip_NarrowTerminal_RendersTTFTAndTokValues(t *testing.T) {
	hist := []metrics.FleetSummary{{P99TTFT: 250, TotalTokPerSec: 1200}}
	got := ui.RenderPulseStrip(hist, 50, 24)
	for _, needle := range []string{"TTFT P99", "tok/s", "250", "1200"} {
		if !strings.Contains(got, needle) {
			t.Errorf("narrow-width pulse strip should contain %q, got %q", needle, got)
		}
	}
}

func TestRenderPulseStrip_EmptyHistory_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderPulseStrip panicked on empty history: %v", r)
		}
	}()
	_ = ui.RenderPulseStrip(nil, 120, 24)
}

func TestRenderPulseStrip_SingleSampleHistory_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderPulseStrip panicked on single-sample history: %v", r)
		}
	}()
	_ = ui.RenderPulseStrip([]metrics.FleetSummary{{}}, 120, 24)
}

// ─── RenderHitRateBar ────────────────────────────────────────────────────────

// Spec: inverted scale — high pct = green, low pct = red.
// We can't assert specific ANSI codes universally because lipgloss may strip
// styling depending on profile. But we can assert that outputs differ between
// extreme values, that they're non-empty, and that width is honored.

func TestRenderHitRateBar_NonEmpty(t *testing.T) {
	got := ui.RenderHitRateBar(95.0, 8)
	if got == "" {
		t.Errorf("RenderHitRateBar should produce non-empty output for valid pct/width")
	}
}

func TestRenderHitRateBar_HighAndLowDiffer(t *testing.T) {
	high := ui.RenderHitRateBar(95.0, 8)
	low := ui.RenderHitRateBar(10.0, 8)
	if high == low {
		t.Errorf("RenderHitRateBar should produce different output for high vs low pct (got identical: %q)", high)
	}
}

func TestRenderHitRateBar_ZeroWidth_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderHitRateBar panicked on width=0: %v", r)
		}
	}()
	_ = ui.RenderHitRateBar(50.0, 0)
}

func TestRenderHitRateBar_ZeroPct_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderHitRateBar panicked on pct=0: %v", r)
		}
	}()
	_ = ui.RenderHitRateBar(0.0, 8)
}
