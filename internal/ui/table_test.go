package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func poolTestWorkers() []*metrics.WorkerMetrics {
	return []*metrics.WorkerMetrics{
		{Label: "prefill-a", Role: "prefill", Online: true, Backend: metrics.BackendVLLM},
		{Label: "prefill-b", Role: "prefill", Online: true, Backend: metrics.BackendVLLM},
		{Label: "decode-a", Role: "decode", Online: true, Backend: metrics.BackendVLLM},
		{Label: "decode-b", Role: "decode", Online: true, Backend: metrics.BackendVLLM},
		{Label: "decode-c", Role: "decode", Online: true, Backend: metrics.BackendVLLM},
		{Label: "err-worker", Role: "decode", Online: false, Backend: metrics.BackendVLLM},
	}
}

func TestBuildTableRowsPoolGrouping(t *testing.T) {
	workers := poolTestWorkers()
	rows := BuildTableRows(workers, "")

	// Expect: PREFILL separator, 2 prefill, DECODE separator, 3 decode, then 1 offline worker (no ERROR separator)
	var separators []string
	var dataCount int
	for _, r := range rows {
		if r.separator != "" {
			separators = append(separators, r.separator)
		} else {
			dataCount++
		}
	}

	if len(separators) != 2 {
		t.Fatalf("expected 2 separators (PREFILL, DECODE), got %d: %v", len(separators), separators)
	}
	if !strings.Contains(separators[0], "PREFILL") {
		t.Errorf("first separator should be PREFILL, got %q", separators[0])
	}
	if !strings.Contains(separators[1], "DECODE") {
		t.Errorf("second separator should be DECODE, got %q", separators[1])
	}
	if dataCount != 6 {
		t.Errorf("expected 6 data rows, got %d", dataCount)
	}
}

func TestBuildTableRowsNoPools(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Label: "w1", Role: "mono", Online: true, Backend: metrics.BackendVLLM},
		{Label: "w2", Role: "mono", Online: true, Backend: metrics.BackendVLLM},
	}
	rows := BuildTableRows(workers, "")

	for _, r := range rows {
		if r.separator != "" {
			t.Errorf("mono-only workers should not have pool separators, got %q", r.separator)
		}
	}
	if DataRowCount(rows) != 2 {
		t.Errorf("expected 2 data rows, got %d", DataRowCount(rows))
	}
}

func TestBuildTableRowsCounts(t *testing.T) {
	workers := poolTestWorkers()
	rows := BuildTableRows(workers, "")

	for _, r := range rows {
		if strings.Contains(r.separator, "PREFILL") && !strings.Contains(r.separator, "2 workers") {
			t.Errorf("PREFILL separator should say 2 workers, got %q", r.separator)
		}
		if strings.Contains(r.separator, "DECODE") && !strings.Contains(r.separator, "ERROR") && !strings.Contains(r.separator, "3 workers") {
			t.Errorf("DECODE separator should say 3 workers, got %q", r.separator)
		}
	}
}

func TestNextDataRowSkipsSeparators(t *testing.T) {
	workers := poolTestWorkers()
	rows := BuildTableRows(workers, "")

	// Starting at first prefill (dataIdx=0), moving down should reach dataIdx=1
	next := NextDataRow(rows, 0, +1)
	if next != 1 {
		t.Errorf("expected next=1 from 0 going down, got %d", next)
	}

	// From last prefill (dataIdx=1), moving down should skip DECODE separator → dataIdx=2
	next = NextDataRow(rows, 1, +1)
	if next != 2 {
		t.Errorf("expected next=2 from 1 going down (skip separator), got %d", next)
	}

	// From first decode (dataIdx=2), moving up should skip separator → dataIdx=1
	next = NextDataRow(rows, 2, -1)
	if next != 1 {
		t.Errorf("expected next=1 from 2 going up (skip separator), got %d", next)
	}

	// From first row, moving up should clamp
	next = NextDataRow(rows, 0, -1)
	if next != 0 {
		t.Errorf("expected next=0 at top going up, got %d", next)
	}

	// From last data row, moving down should clamp
	lastIdx := DataRowCount(rows) - 1
	next = NextDataRow(rows, lastIdx, +1)
	if next != lastIdx {
		t.Errorf("expected next=%d at bottom going down, got %d", lastIdx, next)
	}
}

func TestDataRowCount(t *testing.T) {
	workers := poolTestWorkers()
	rows := BuildTableRows(workers, "")

	if DataRowCount(rows) != 6 {
		t.Errorf("expected 6 data rows, got %d", DataRowCount(rows))
	}
}

func TestRenderTableWithPools(t *testing.T) {
	workers := poolTestWorkers()
	output := RenderTable(workers, 0, SortNone, "", 120, 0, 0, nil, nil, "")

	if !strings.Contains(output, "PREFILL POOL") {
		t.Error("expected PREFILL POOL separator in output")
	}
	if !strings.Contains(output, "DECODE POOL") {
		t.Error("expected DECODE POOL separator in output")
	}
	// No ERROR separator — offline workers render inline
	if strings.Contains(output, "── ERROR") {
		t.Error("ERROR separator should not appear")
	}
}

func TestRenderTableHeaderColumns(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Label: "w1", Role: "mono", Online: true, Backend: metrics.BackendVLLM, KVCacheUsagePct: 55},
	}
	output := RenderTable(workers, 0, SortNone, "", 160, 0, 0, nil, nil, "")

	for _, col := range []string{"ROLE", "WORKER", "BACKEND", "KV%", "KV BAR", "QUEUE"} {
		if !strings.Contains(output, col) {
			t.Errorf("expected column header %q in output", col)
		}
	}
}

func TestBuildTableRowsFilterBackend(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Label: "w1", Role: "prefill", Online: true, Backend: metrics.BackendVLLM},
		{Label: "w2", Role: "decode", Online: true, Backend: metrics.BackendSGLang},
	}
	rows := BuildTableRows(workers, metrics.BackendVLLM)

	if DataRowCount(rows) != 1 {
		t.Errorf("expected 1 data row after filtering, got %d", DataRowCount(rows))
	}
}

func TestRenderKVBar(t *testing.T) {
	tests := []struct {
		pct  float64
		want int // expected string length > 0
	}{
		{0, 6},
		{50, 6},
		{75, 6},
		{90, 6},
		{100, 6},
	}

	for _, tt := range tests {
		bar := RenderKVBar(tt.pct, 6)
		if len(bar) == 0 {
			t.Errorf("RenderKVBar(%.0f, 6) returned empty string", tt.pct)
		}
	}
}
