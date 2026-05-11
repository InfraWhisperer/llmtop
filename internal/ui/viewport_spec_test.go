// SPDX-License-Identifier: Apache-2.0
// Spec-only contract tests for F5 (Main Table Viewport Scrolling).
// Source of truth: docs/feature-spec.md F5.
//
// Tests run as a black-box against the public ui helpers — ClampViewport,
// BuildTableRows, DataRowCount, DisplayIndexOf, FirstDataRowAt,
// LastDataRowAtOrBefore, NextDataRow, RenderTable — plus assertions on
// RenderTable output. We do not reach into Model's unexported state.

package ui_test

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/internal/ui"
)

// --- helpers ---------------------------------------------------------------

// makeWorkers returns N synthetic prefill workers with predictable labels.
// Using a single role yields a single pool with one separator row, simpler to
// reason about for viewport tests.
func makeWorkers(n int) []*metrics.WorkerMetrics {
	out := make([]*metrics.WorkerMetrics, n)
	for i := range n {
		out[i] = &metrics.WorkerMetrics{
			Endpoint:  "http://w" + itoa(i),
			Label:     "w" + itoa(i),
			Backend:   metrics.BackendVLLM,
			ModelName: "m",
			Online:    true,
			Role:      "prefill",
		}
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// --- ClampViewport ---------------------------------------------------------

func TestClampViewport_OffsetWithinRange_Unchanged(t *testing.T) {
	start, end := ui.ClampViewport(20, 5, 3)
	if start != 3 || end != 8 {
		t.Fatalf("expected [3,8), got [%d,%d)", start, end)
	}
}

func TestClampViewport_OffsetNegative_ClampedToZero(t *testing.T) {
	start, end := ui.ClampViewport(20, 5, -10)
	if start != 0 || end != 5 {
		t.Fatalf("expected [0,5) when offset is negative, got [%d,%d)", start, end)
	}
}

func TestClampViewport_OffsetPastEnd_PulledBackToShowFullWindow(t *testing.T) {
	// Spec: "When the requested window extends past the end of the row list,
	// start is pulled back so that exactly visibleRows are shown."
	start, end := ui.ClampViewport(20, 5, 100)
	if end-start != 5 {
		t.Fatalf("expected window of size 5 even when offset past end, got [%d,%d)=size %d", start, end, end-start)
	}
	if end != 20 {
		t.Fatalf("expected end=total=20, got %d", end)
	}
}

func TestClampViewport_TotalLessThanVisible_ReturnsFullRange(t *testing.T) {
	start, end := ui.ClampViewport(3, 10, 0)
	if start != 0 || end != 3 {
		t.Fatalf("expected [0,3) when total<visible, got [%d,%d)", start, end)
	}
}

func TestClampViewport_TotalEqualsVisible_ReturnsFullRange(t *testing.T) {
	start, end := ui.ClampViewport(5, 5, 0)
	if start != 0 || end != 5 {
		t.Fatalf("expected [0,5), got [%d,%d)", start, end)
	}
}

func TestClampViewport_ZeroTotal_ReturnsEmpty(t *testing.T) {
	start, end := ui.ClampViewport(0, 5, 0)
	if start != 0 || end != 0 {
		t.Fatalf("expected [0,0) for total=0, got [%d,%d)", start, end)
	}
}

func TestClampViewport_StartLessOrEqualEndAlways(t *testing.T) {
	// Property: 0 <= start <= end <= total for any input.
	totals := []int{0, 1, 5, 32}
	visibles := []int{0, 1, 5, 100}
	offsets := []int{-5, 0, 1, 100}
	for _, total := range totals {
		for _, vis := range visibles {
			for _, off := range offsets {
				start, end := ui.ClampViewport(total, vis, off)
				if start < 0 || start > end || end > total {
					t.Errorf("invariant violated for total=%d vis=%d off=%d → [%d,%d)", total, vis, off, start, end)
				}
				if end-start > vis && vis > 0 {
					// Allow window=total when visibleRows >= total (size-vis bound only when
					// not capped by total).
					if total > vis || end-start != total {
						t.Errorf("window too large for total=%d vis=%d off=%d → size %d", total, vis, off, end-start)
					}
				}
			}
		}
	}
}

// --- BuildTableRows + DataRowCount + display index helpers ----------------

func TestBuildTableRows_DataRowCountMatchesOnlineWorkerCount(t *testing.T) {
	workers := makeWorkers(8)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	if got := ui.DataRowCount(rows); got != 8 {
		t.Fatalf("expected 8 data rows, got %d (rows total %d)", got, len(rows))
	}
}

func TestBuildTableRows_SeparatorRowsCountTowardsDisplayLength(t *testing.T) {
	workers := makeWorkers(4)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	dataCount := ui.DataRowCount(rows)
	// Spec invariant: "Separator rows count as one displayed row each."
	// Total rows must therefore be >= dataCount; equality only if no separators.
	if len(rows) < dataCount {
		t.Fatalf("display rows (%d) cannot be fewer than data rows (%d)", len(rows), dataCount)
	}
}

func TestDisplayIndexOf_ZerothDataRow_GreaterThanOrEqualZero(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	idx := ui.DisplayIndexOf(rows, 0)
	if idx < 0 {
		t.Fatalf("expected non-negative display index for data row 0, got %d", idx)
	}
}

func TestDisplayIndexOf_MissingTarget_ReturnsNegativeOne(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	if got := ui.DisplayIndexOf(rows, 999); got != -1 {
		t.Fatalf("expected -1 for missing data row, got %d", got)
	}
}

func TestFirstDataRowAt_ZeroDisplay_ReturnsValidDataIdx(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.FirstDataRowAt(rows, 0)
	if got < 0 || got >= 3 {
		t.Fatalf("expected dataIdx in [0,3), got %d", got)
	}
}

func TestFirstDataRowAt_PastEnd_ReturnsNegativeOne(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.FirstDataRowAt(rows, 9999)
	if got != -1 {
		t.Fatalf("expected -1 for display past end, got %d", got)
	}
}

func TestLastDataRowAtOrBefore_PastEnd_ReturnsLastDataIdx(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.LastDataRowAtOrBefore(rows, len(rows)+10)
	if got != 2 {
		t.Fatalf("expected last dataIdx 2, got %d", got)
	}
}

func TestLastDataRowAtOrBefore_BeforeStart_ReturnsNegativeOne(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.LastDataRowAtOrBefore(rows, -1)
	if got != -1 {
		t.Fatalf("expected -1 for negative display index, got %d", got)
	}
}

// --- NextDataRow ---------------------------------------------------------

func TestNextDataRow_DownAdvancesByOne(t *testing.T) {
	workers := makeWorkers(4)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.NextDataRow(rows, 0, +1)
	if got != 1 {
		t.Fatalf("expected dataIdx 1 after +1 from 0, got %d", got)
	}
}

func TestNextDataRow_UpRetreatsByOne(t *testing.T) {
	workers := makeWorkers(4)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.NextDataRow(rows, 2, -1)
	if got != 1 {
		t.Fatalf("expected dataIdx 1 after -1 from 2, got %d", got)
	}
}

func TestNextDataRow_DownAtLastRow_StaysOrSignalsEnd(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.NextDataRow(rows, 2, +1)
	// Spec is permissive — could be 2 (clamp) or -1 (signal end). Both are
	// acceptable; we only assert it's not >= len(workers) (no skip past end).
	if got >= 3 {
		t.Fatalf("NextDataRow advanced past last row: got %d", got)
	}
}

func TestNextDataRow_UpAtFirstRow_StaysOrSignalsStart(t *testing.T) {
	workers := makeWorkers(3)
	rows := ui.BuildTableRows(workers, metrics.BackendUnknown)
	got := ui.NextDataRow(rows, 0, -1)
	if got > 0 {
		t.Fatalf("NextDataRow retreated past first row: got %d", got)
	}
}

// --- RenderTable signature & basic output ---------------------------------

func TestRenderTable_NewSignature_AcceptsVisibleRowsAndOffset(t *testing.T) {
	// Spec F5: signature is (workers, selectedIdx, sortCol, filterBackend,
	// width, visibleRows, viewportOffset). Calling it must compile and
	// return a non-empty string for non-empty input.
	workers := makeWorkers(3)
	out := ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 10, 0, nil, nil, "")
	if out == "" {
		t.Fatalf("expected non-empty render output")
	}
}

func TestRenderTable_LimitsRowsToVisibleWindow(t *testing.T) {
	// Spec F5 acceptance: with 32 workers and a small visibleRows, the output
	// contains fewer worker labels than the full set.
	workers := makeWorkers(32)
	smallOut := ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 5, 0, nil, nil, "")
	bigOut := ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 100, 0, nil, nil, "")
	count := func(s string) int {
		n := 0
		for i := range 32 {
			needle := "w" + itoa(i)
			if strings.Contains(s, needle) {
				n++
			}
		}
		return n
	}
	smallN := count(smallOut)
	bigN := count(bigOut)
	if smallN >= bigN {
		t.Fatalf("expected smaller window to render fewer worker labels: smallN=%d bigN=%d", smallN, bigN)
	}
}

func TestRenderTable_ScrollOffsetShiftsWindow(t *testing.T) {
	// Two RenderTable calls on the same worker list with different
	// viewportOffset values should yield different output (different worker
	// labels visible).
	workers := makeWorkers(20)
	a := ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 5, 0, nil, nil, "")
	b := ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 5, 10, nil, nil, "")
	if a == b {
		t.Fatalf("expected different output for offset=0 vs offset=10")
	}
}

func TestRenderTable_NegativeOffsetDoesNotPanic(t *testing.T) {
	// Spec: RenderTable defends against out-of-bound values.
	workers := makeWorkers(5)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RenderTable panicked on negative offset: %v", r)
		}
	}()
	_ = ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 5, -100, nil, nil, "")
}

func TestRenderTable_OffsetPastEndDoesNotPanic(t *testing.T) {
	workers := makeWorkers(5)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RenderTable panicked on offset past end: %v", r)
		}
	}()
	_ = ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 5, 9999, nil, nil, "")
}

func TestRenderTable_EmptyWorkers_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RenderTable panicked on empty workers: %v", r)
		}
	}()
	_ = ui.RenderTable(nil, 0, ui.SortNone, metrics.BackendUnknown, 200, 10, 0, nil, nil, "")
}

func TestRenderTable_VisibleRowsZero_DoesNotPanic(t *testing.T) {
	workers := makeWorkers(5)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RenderTable panicked on visibleRows=0: %v", r)
		}
	}()
	_ = ui.RenderTable(workers, 0, ui.SortNone, metrics.BackendUnknown, 200, 0, 0, nil, nil, "")
}
