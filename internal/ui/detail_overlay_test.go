package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// TestRenderCDFSparkline_AllZerosRendersBaseline ensures the data-unavailable
// case (zero CDF) renders as 10 baseline blocks rather than panicking or
// producing fewer characters.
func TestRenderCDFSparkline_AllZerosRendersBaseline(t *testing.T) {
	var zero [10]float64
	out := stripANSI(renderCDFSparkline(zero))
	if got := runeCount(out); got != 10 {
		t.Errorf("expected 10 chars, got %d (%q)", got, out)
	}
	for _, r := range out {
		if r != '▁' {
			t.Errorf("expected all baseline blocks, got %q", out)
			break
		}
	}
}

// TestRenderCDFSparkline_RisingShape verifies a monotonically increasing CDF
// produces a non-decreasing block sequence.
func TestRenderCDFSparkline_RisingShape(t *testing.T) {
	cdf := [10]float64{0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 1.0}
	out := stripANSI(renderCDFSparkline(cdf))
	prev := -1
	for _, r := range out {
		idx := blockIndex(r)
		if idx < prev {
			t.Errorf("expected non-decreasing blocks, got %q", out)
			break
		}
		prev = idx
	}
}

// TestFormatLatencyMs_ZeroIsDash exercises the all-zero invariant.
func TestFormatLatencyMs_ZeroIsDash(t *testing.T) {
	if got := formatLatencyMs(0); got != "—" {
		t.Errorf("expected em-dash for zero, got %q", got)
	}
	if got := formatLatencyMs(-1); got != "—" {
		t.Errorf("expected em-dash for negative, got %q", got)
	}
}

// TestRenderLatencyBlock_ZeroP99SuppressesSparkline confirms we hide the
// sparkline row when there's no real distribution to plot.
func TestRenderLatencyBlock_ZeroP99SuppressesSparkline(t *testing.T) {
	ls := metrics.LatencySnapshot{} // all zero
	out := renderLatencyBlock("TTFT", ls, 200)
	// One trailing newline only — the sparkline row is omitted.
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected single line for zero P99, got %q", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash placeholder in line, got %q", out)
	}
}

// TestRenderLatencyBlock_NarrowOmitsSparkline ensures terminals < 80 chars
// don't get a sparkline row that would wrap.
func TestRenderLatencyBlock_NarrowOmitsSparkline(t *testing.T) {
	ls := metrics.LatencySnapshot{
		P50: 100, P95: 200, P99: 400,
		CDFPoints: [10]float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	}
	out := renderLatencyBlock("TTFT", ls, 60)
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected only the header line at narrow width, got:\n%s", out)
	}
}

// TestRenderLatencyBlock_WideRendersSparkline ensures wide terminals get
// the 10-char CDF.
func TestRenderLatencyBlock_WideRendersSparkline(t *testing.T) {
	ls := metrics.LatencySnapshot{
		P50: 100, P95: 200, P99: 400,
		CDFPoints: [10]float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	}
	out := renderLatencyBlock("TTFT", ls, 200)
	if strings.Count(out, "\n") != 2 {
		t.Errorf("expected 2 lines (header + sparkline), got:\n%s", out)
	}
}

// blockIndex returns the index of r in cdfBlocks, or -1.
func blockIndex(r rune) int {
	for i, b := range cdfBlocks {
		if b == r {
			return i
		}
	}
	return -1
}

// stripANSI removes ANSI color codes (ESC [ ... m sequences). Sufficient for
// these tests since lipgloss only emits SGR codes.
func stripANSI(s string) string {
	var sb strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// runeCount returns the count of runes in s.
func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
