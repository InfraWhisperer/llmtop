package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Issue #15 follow-up (P1): the "no split → the whole prompt side was computed"
// fallback was applied only to the fleet header, so every per-worker surface
// rendered a vLLM worker busy at 800 prompt tok/s as prefill 0 / "-" — which
// reads as "prefill is stuck", the exact complaint the SGLang fix addressed. All
// three surfaces must fall back the same way.
func TestUnsplitBackendPrefillSurfaces(t *testing.T) {
	w := &metrics.WorkerMetrics{
		Backend: metrics.BackendVLLM, Online: true, Label: "vllm-0",
		PromptTokPerSec: 800, GenTokPerSec: 150, EvictPerSec: 1, CacheHitRatePct: 42,
	}

	overlay := RenderDetailOverlay(w, nil, 120, 40)
	assertPrefillCell(t, "overlay Pre t/s", cellAfter(t, overlay, "Pre t/s"), "800")
	assertPrefillCell(t, "overlay Pre cache t/s", cellAfter(t, overlay, "Pre cache t/s"), "—")

	card := renderPrefillCard(w, 60)
	assertPrefillCell(t, "prefill card Pre compute t/s", cellAfter(t, card, "Pre compute t/s"), "800")

	details := renderThroughputDetailRows(w)
	if row := cellAfter(t, details, "Prefill Compute Tokens/s"); !strings.Contains(row, "800.0") {
		t.Errorf("detail pane prefill compute row = %q, want 800.0 (prompt side, no split reported)", row)
	}
	if strings.Contains(details, "Prefill Cache Tokens/s") {
		t.Errorf("detail pane must not invent a cache row for a backend with no split:\n%s", details)
	}
}

// A backend that does report a split keeps compute and cache apart: the fallback
// must not resurrect the 25k+ tok/s cache bursts inside the compute number.
func TestSplitBackendPrefillSurfacesKeepCacheApart(t *testing.T) {
	w := &metrics.WorkerMetrics{
		Backend: metrics.BackendSGLang, Online: true, Label: "sglang-0",
		PromptTokPerSec: 60, GenTokPerSec: 150,
		PrefillComputeTokPerSec: 40, PrefillCacheTokPerSec: 20,
	}

	card := renderPrefillCard(w, 60)
	assertPrefillCell(t, "prefill card Pre compute t/s", cellAfter(t, card, "Pre compute t/s"), "40")
	assertPrefillCell(t, "prefill card Pre cache t/s", cellAfter(t, card, "Pre cache t/s"), "20")

	if got := cellAfter(t, RenderDetailOverlay(w, nil, 120, 40), "Pre t/s"); !strings.Contains(got, "40") {
		t.Errorf("overlay Pre t/s = %q, want the 40 tok/s compute half", got)
	}
	if details := renderThroughputDetailRows(w); !strings.Contains(details, "Prefill Cache Tokens/s") {
		t.Errorf("detail pane should render the cache row when a split exists:\n%s", details)
	}
}

// cellAfter returns the rendered value that follows a label on the same line.
func cellAfter(t *testing.T, rendered, label string) string {
	t.Helper()
	i := strings.Index(rendered, label)
	if i < 0 {
		t.Fatalf("rendered surface has no %q cell:\n%s", label, rendered)
	}
	line := rendered[i:]
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	return strings.TrimSpace(strings.TrimPrefix(line, label))
}

func assertPrefillCell(t *testing.T, what, cell, want string) {
	t.Helper()
	if !strings.Contains(cell, want) {
		t.Errorf("%s = %q, want it to contain %q", what, cell, want)
	}
}
