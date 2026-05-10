package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// TestClampViewport_TotalFitsInWindow exercises the case where the row list
// fits within the viewport: no clamping, no scroll.
func TestClampViewport_TotalFitsInWindow(t *testing.T) {
	start, end := ClampViewport(5, 10, 0)
	if start != 0 || end != 5 {
		t.Errorf("expected (0,5), got (%d,%d)", start, end)
	}
}

// TestClampViewport_OffsetTooLarge confirms we pull the start back to keep
// exactly visibleRows visible, matching End-style snap behavior.
func TestClampViewport_OffsetTooLarge(t *testing.T) {
	start, end := ClampViewport(20, 5, 99)
	if start != 15 || end != 20 {
		t.Errorf("expected (15,20), got (%d,%d)", start, end)
	}
}

// TestClampViewport_NegativeOffsetClampsToZero ensures we never produce a
// negative slice index.
func TestClampViewport_NegativeOffsetClampsToZero(t *testing.T) {
	start, end := ClampViewport(20, 5, -3)
	if start != 0 || end != 5 {
		t.Errorf("expected (0,5), got (%d,%d)", start, end)
	}
}

// TestClampViewport_ZeroTotalOrVisible covers the empty / pre-init cases.
func TestClampViewport_ZeroTotalOrVisible(t *testing.T) {
	if s, e := ClampViewport(0, 5, 0); s != 0 || e != 0 {
		t.Errorf("zero total: expected (0,0), got (%d,%d)", s, e)
	}
	if s, e := ClampViewport(5, 0, 0); s != 0 || e != 0 {
		t.Errorf("zero visible: expected (0,0), got (%d,%d)", s, e)
	}
}

// TestDisplayIndexOf_FindsRow walks past separators to the matching dataIdx.
func TestDisplayIndexOf_FindsRow(t *testing.T) {
	workers := poolTestWorkers()
	rows := BuildTableRows(workers, "")

	// dataIdx 0 is the first prefill worker; one separator precedes it.
	got := DisplayIndexOf(rows, 0)
	if got != 1 {
		t.Errorf("expected display index 1 for dataIdx 0 (after 1 separator), got %d", got)
	}
}

// TestDisplayIndexOf_NotFound returns -1 for an unknown dataIdx.
func TestDisplayIndexOf_NotFound(t *testing.T) {
	rows := BuildTableRows(poolTestWorkers(), "")
	if got := DisplayIndexOf(rows, 999); got != -1 {
		t.Errorf("expected -1 for missing dataIdx, got %d", got)
	}
}

// TestFirstDataRowAt_SkipsSeparators lands on the first selectable row at
// or after the requested display index.
func TestFirstDataRowAt_SkipsSeparators(t *testing.T) {
	rows := BuildTableRows(poolTestWorkers(), "")
	// Display index 0 is a separator; FirstDataRowAt should skip it.
	got := FirstDataRowAt(rows, 0)
	if got != 0 { // dataIdx 0 = first prefill
		t.Errorf("expected dataIdx 0 at display 0, got %d", got)
	}
}

// TestLastDataRowAtOrBefore_FindsLast walks backward past separators.
func TestLastDataRowAtOrBefore_FindsLast(t *testing.T) {
	rows := BuildTableRows(poolTestWorkers(), "")
	got := LastDataRowAtOrBefore(rows, len(rows)-1)
	// 6 data rows (5 online + 1 offline) → last dataIdx is 5.
	if got != 5 {
		t.Errorf("expected last dataIdx 5, got %d", got)
	}
}

// TestRenderTable_ViewportSlicesRows ensures we render at most visibleRows
// rows from the body when the row count exceeds the viewport.
func TestRenderTable_ViewportSlicesRows(t *testing.T) {
	// 10 mono workers — no separators, so the full slice is data rows.
	workers := make([]*metrics.WorkerMetrics, 10)
	for i := range workers {
		workers[i] = &metrics.WorkerMetrics{
			Label:   "worker-" + string(rune('a'+i)),
			Role:    "mono",
			Online:  true,
			Backend: metrics.BackendVLLM,
		}
	}
	out := RenderTable(workers, 0, SortNone, "", 160, 4, 0, nil, nil, "")
	// Header (1) + separator (1) + 4 body rows = 6 newlines max.
	bodyLines := strings.Count(out, "\n")
	if bodyLines > 6 {
		t.Errorf("expected at most 6 newlines (header+sep+4 body), got %d\n%s", bodyLines, out)
	}
}

// TestRenderTable_ViewportOffsetSkipsLeading slices into the row list at the
// given offset.
func TestRenderTable_ViewportOffsetSkipsLeading(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Label: "alpha", Role: "mono", Online: true, Backend: metrics.BackendVLLM},
		{Label: "bravo", Role: "mono", Online: true, Backend: metrics.BackendVLLM},
		{Label: "charlie", Role: "mono", Online: true, Backend: metrics.BackendVLLM},
	}
	out := RenderTable(workers, 0, SortNone, "", 160, 1, 2, nil, nil, "")
	if strings.Contains(out, "alpha") || strings.Contains(out, "bravo") {
		t.Errorf("expected leading rows excluded by offset, got:\n%s", out)
	}
	if !strings.Contains(out, "charlie") {
		t.Errorf("expected charlie row visible at offset 2, got:\n%s", out)
	}
}
