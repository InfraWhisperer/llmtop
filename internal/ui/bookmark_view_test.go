package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestRenderBookmarkCompare_NoBookmarks(t *testing.T) {
	out := RenderBookmarkCompare(nil, nil, nil, 120, 30)
	if !strings.Contains(out, "No bookmarks") {
		t.Errorf("expected empty-state placeholder, got %q", out)
	}
}

func TestRenderBookmarkCompare_TwoBookmarksRendersLabels(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://w1", Label: "alpha", TTFT_P99: 100, KVCacheUsagePct: 40},
		{Endpoint: "http://w2", Label: "beta", TTFT_P99: 200, KVCacheUsagePct: 70},
	}
	bms := []WorkerBookmark{
		{Endpoint: "http://w1", Label: "alpha"},
		{Endpoint: "http://w2", Label: "beta"},
	}
	out := RenderBookmarkCompare(workers, nil, bms, 120, 30)
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected 'alpha' label in output, got %q", out)
	}
	if !strings.Contains(out, "beta") {
		t.Errorf("expected 'beta' label in output, got %q", out)
	}
}

func TestRenderBookmarkCompare_NilRing_NoSparklineButRenders(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://w1", Label: "alpha", TTFT_P99: 100},
	}
	bms := []WorkerBookmark{{Endpoint: "http://w1", Label: "alpha"}}
	out := RenderBookmarkCompare(workers, nil, bms, 120, 30)
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected label even with nil ring, got %q", out)
	}
}

func TestRenderBookmarkCompare_OfflineMarker(t *testing.T) {
	// bookmark exists but worker not in live set → "(offline)" column.
	bms := []WorkerBookmark{{Endpoint: "http://gone", Label: "ghost"}}
	out := RenderBookmarkCompare(nil, nil, bms, 120, 30)
	if !strings.Contains(out, "offline") {
		t.Errorf("expected offline marker for missing worker, got %q", out)
	}
}

func TestRenderBookmarkCompare_DoesNotPanic_EmptyWorkers(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderBookmarkCompare panicked: %v", r)
		}
	}()
	bms := []WorkerBookmark{{Endpoint: "http://w1", Label: "w1"}}
	_ = RenderBookmarkCompare(nil, nil, bms, 120, 30)
}
