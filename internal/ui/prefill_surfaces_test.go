package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// splitWorker is a SGLang-style worker that reports the prefill compute/cache
// split, reusing 25_000 tok/s of prompt tokens while computing 900 and generating
// 150. Reused tokens must never appear as earned throughput (issue #15).
func splitWorker() *metrics.WorkerMetrics {
	return &metrics.WorkerMetrics{
		Endpoint:                "http://localhost:8100",
		Label:                   "sglang-0",
		Backend:                 metrics.BackendSGLang,
		ModelName:               "llama-70b",
		Online:                  true,
		PromptTokPerSec:         25900,
		GenTokPerSec:            150,
		PrefillSplit:            true, // capability flag, not a >0 guess
		PrefillComputeTokPerSec: 900,
		PrefillCacheTokPerSec:   25000,
		CacheHitRatePct:         96,
		CacheHitWindowPct:       42,
		RequestsRunning:         2,
	}
}

// unsplitWorker is a vLLM-style worker: its whole prompt side was computed, and
// it has no cache-hit metric at all.
func unsplitWorker() *metrics.WorkerMetrics {
	return &metrics.WorkerMetrics{
		Endpoint:        "http://localhost:8000",
		Label:           "vllm-0",
		Backend:         metrics.BackendVLLM,
		ModelName:       "llama-70b",
		Online:          true,
		PromptTokPerSec: 900,
		GenTokPerSec:    150,
		RequestsRunning: 2,
	}
}

// TestThroughputDetailRowsNeverShowInflatedPrompt guards the detail pane: the
// inflated prompt total (25_900) may not be rendered as a rate, and no row may
// duplicate another.
func TestThroughputDetailRowsNeverShowInflatedPrompt(t *testing.T) {
	out := renderThroughputDetailRows(splitWorker())
	plain := stripANSI(out)

	if strings.Contains(plain, "25900") || strings.Contains(plain, "25900.0") {
		t.Errorf("detail rows leaked the cache-inflated prompt total:\n%s", plain)
	}
	for _, want := range []string{"Prefill Compute t/s", "900.0", "Prefill Cache t/s", "25000.0", "Generation t/s", "150.0"} {
		if !strings.Contains(plain, want) {
			t.Errorf("detail rows missing %q:\n%s", want, plain)
		}
	}
	// Each value appears exactly once: a total row that repeats a part is how the
	// old pane invited a hand-sum.
	if got := strings.Count(plain, "900.0"); got != 1 {
		t.Errorf("prefill compute value rendered %d times, want 1:\n%s", got, plain)
	}
}

// TestThroughputDetailRowsNoDuplicateForUnsplitBackend covers the other half of
// the contract: for a backend with no split there is no cache row, and the
// compute row is not mirrored by an identical prompt row.
func TestThroughputDetailRowsNoDuplicateForUnsplitBackend(t *testing.T) {
	plain := stripANSI(renderThroughputDetailRows(unsplitWorker()))

	if strings.Contains(plain, "Prefill Cache") {
		t.Errorf("backend without a split rendered a cache row:\n%s", plain)
	}
	if strings.Contains(plain, "Prompt Tokens/s") {
		t.Errorf("backend without a split rendered a prompt row duplicating compute:\n%s", plain)
	}
	if got := strings.Count(plain, "900.0"); got != 1 {
		t.Errorf("compute value rendered %d times, want 1:\n%s", got, plain)
	}
}

// TestThroughputDetailRowLabelsFitColumn is a layout guard: renderDetailRow pads
// labels to 22 columns, so a longer label pushes the value out of alignment.
func TestThroughputDetailRowLabelsFitColumn(t *testing.T) {
	const labelWidth = 22
	for _, worker := range []*metrics.WorkerMetrics{splitWorker(), unsplitWorker()} {
		for line := range strings.SplitSeq(stripANSI(renderThroughputDetailRows(worker)), "\n") {
			label := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, "  "), "  ", 2)[0])
			if label == "" || !strings.Contains(line, "t/s") {
				continue
			}
			if len(label) > labelWidth {
				t.Errorf("%s: label %q is %d chars, exceeds the %d-char column",
					worker.Label, label, len(label), labelWidth)
			}
		}
	}
}

func TestDetailOverlayPrefillSide(t *testing.T) {
	split := stripANSI(RenderDetailOverlay(splitWorker(), nil, 120, 40))
	if !strings.Contains(split, "PreCache t/s") || !strings.Contains(split, "25") {
		t.Errorf("split worker should show its reused rate:\n%s", split)
	}

	unsplit := stripANSI(RenderDetailOverlay(unsplitWorker(), nil, 120, 40))
	if row := lineWith(unsplit, "PreCache t/s"); !strings.Contains(row, "\u2014") || strings.Contains(row, " 0 ") {
		t.Errorf("backend without a split should render the cache half as an em dash, not 0: %q", row)
	}
	if row := lineWith(unsplit, "Hit rate"); !strings.Contains(row, "\u2014") || strings.Contains(row, "0%") {
		t.Errorf("worker with no cache-hit metric should render an em dash, not 0%%: %q", row)
	}
}

// lineWith returns the first rendered line containing needle, trimmed of the box
// padding lipgloss adds around the overlay content.
func lineWith(rendered, needle string) string {
	for line := range strings.SplitSeq(stripBorder(rendered), "\n") {
		if strings.Contains(line, needle) {
			return strings.Join(strings.Fields(line), " ")
		}
	}
	return ""
}

// stripBorder drops the rounded box characters so column assertions see content
// widths rather than a wall of box-drawing runes.
func stripBorder(s string) string {
	for _, r := range []string{"\u256d", "\u2500", "\u252c", "\u250c", "\u251c", "\u256f", "\u2524", "\u2570", "\u251d", "\u2518", "\u2502"} {
		s = strings.ReplaceAll(s, r, " ")
	}
	return s
}

// TestFormatCacheHitDetailPrefersWindow — the counter-derived window describes
// current activity; the lifetime ratio sits beside it rather than replacing it.
func TestFormatCacheHitDetailPrefersWindow(t *testing.T) {
	if got := formatCacheHitDetail(splitWorker()); got != "42.0% (window) · 96.0% (lifetime)" {
		t.Errorf("formatCacheHitDetail(split) = %q", got)
	}
	if got := formatCacheHitDetail(unsplitWorker()); got != "\u2014" {
		t.Errorf("formatCacheHitDetail(no metric) = %q, want em dash", got)
	}
	// Known limit, pinned here rather than papered over: a backend that reports a
	// genuine 0% reuse rate with no counter movement is indistinguishable from one
	// that never reported the metric, so it also renders an em dash. Telling those
	// apart needs a capability flag from the collector, which is the tri-state work
	// that follows in the next branch of this stack.
	unset := &metrics.WorkerMetrics{Online: true}
	if got := formatCacheHitDetail(unset); got != "\u2014" {
		t.Errorf("formatCacheHitDetail(worker that never reported) = %q, want em dash", got)
	}
}

// TestComputeIdealRatioIgnoresReusedTokens — the pool sizing advice must cost a
// prefill worker only for prompt tokens it actually computes. At the inflated
// prompt rate this fleet looked like it needed ~172 decode workers per 10.
func TestComputeIdealRatioIgnoresReusedTokens(t *testing.T) {
	prefill := []*metrics.WorkerMetrics{splitWorker()}
	decode := []*metrics.WorkerMetrics{unsplitWorker()}

	if got := computeIdealRatio(prefill, decode); got != "6:1" {
		t.Errorf("computeIdealRatio() = %q, want %q (900 compute / 150 gen)", got, "6:1")
	}
}

// TestRenderModelGroupRowShowsBothSides — the model table must let a prefill-bound
// fleet be told apart from a decode-bound one at a glance (issue #15 surface gap).
func TestRenderModelGroupRowShowsBothSides(t *testing.T) {
	groups := metrics.GroupWorkersByModel([]*metrics.WorkerMetrics{splitWorker()})
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(groups))
	}
	g := groups[0]
	if g.PrefillTokPerSec != 900 || g.GenTokPerSec != 150 {
		t.Fatalf("group split = prefill %v gen %v, want 900/150", g.PrefillTokPerSec, g.GenTokPerSec)
	}

	row := stripANSI(renderModelGroupRow(g, false, 20))
	if !strings.Contains(row, "900") || !strings.Contains(row, "150") {
		t.Errorf("model row should carry both split halves, got %q", row)
	}
	if strings.Contains(row, "25900") {
		t.Errorf("model row leaked the reused-token total: %q", row)
	}
}

// TestRenderModelTableHeaderHasSplitColumns keeps the header and the row layout in
// step: 10 fixed columns, PRE/S and GEN/S next to TOK/S.
func TestRenderModelTableHeaderHasSplitColumns(t *testing.T) {
	head := stripANSI(renderModelTableHeader(ModelSortName, 20))
	for _, want := range []string{"TOK/S", "PRE/S", "GEN/S"} {
		if !strings.Contains(head, want) {
			t.Errorf("model header missing %q: %q", want, head)
		}
	}
}
