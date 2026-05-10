// Black-box contract tests for V10 (KV Eviction Storm Timeline).
// Spec: docs/feature-spec-v2.md §3 V10, §7 glossary.
//
// Pinned contracts:
//   - RenderEvictionTimeline with frames=nil returns string containing "data unavailable".
//   - RenderEvictionTimeline with non-empty frames returns non-empty output.
//   - RenderEvictionTimeline at all branches does not panic.

package ui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/internal/ui"
)

func TestRenderEvictionTimeline_NilFrames_ContainsDataUnavailable(t *testing.T) {
	alert := &metrics.Alert{Title: "kv_critical"}
	out := ui.RenderEvictionTimeline(nil, alert, "ep", 0, 120, 30)
	if !strings.Contains(out, "data unavailable") {
		t.Errorf("RenderEvictionTimeline(nil frames) should contain \"data unavailable\", got %q", out)
	}
}

func TestRenderEvictionTimeline_EmptyFrames_ContainsDataUnavailable(t *testing.T) {
	alert := &metrics.Alert{Title: "kv_critical"}
	out := ui.RenderEvictionTimeline([]metrics.FrameSnapshot{}, alert, "ep", 0, 120, 30)
	if !strings.Contains(out, "data unavailable") {
		t.Errorf("RenderEvictionTimeline(empty frames) should contain \"data unavailable\", got %q", out)
	}
}

func TestRenderEvictionTimeline_NonEmptyFrames_NonEmptyOutput(t *testing.T) {
	frames := make([]metrics.FrameSnapshot, 121)
	for i := range frames {
		w := metrics.WorkerMetrics{Endpoint: "ep", Label: "ep"}
		w.KVCacheUsagePct = float64(50 + i%50)
		w.RequestsWaiting = i
		frames[i] = metrics.FrameSnapshot{
			At:      time.Unix(int64(1000+i), 0),
			Workers: []metrics.WorkerMetrics{w},
		}
	}
	alert := &metrics.Alert{Title: "kv_critical"}
	out := ui.RenderEvictionTimeline(frames, alert, "ep", 60, 120, 30)
	if out == "" {
		t.Errorf("RenderEvictionTimeline with 121 frames should not be empty")
	}
}

func TestRenderEvictionTimeline_NilAlert_DoesNotPanic(t *testing.T) {
	frames := []metrics.FrameSnapshot{
		{At: time.Unix(1, 0), Workers: []metrics.WorkerMetrics{{Endpoint: "ep"}}},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderEvictionTimeline(nil alert) panicked: %v", r)
		}
	}()
	_ = ui.RenderEvictionTimeline(frames, nil, "ep", 0, 120, 30)
}

func TestRenderEvictionTimeline_UnknownWorker_DoesNotPanic(t *testing.T) {
	frames := make([]metrics.FrameSnapshot, 10)
	for i := range frames {
		frames[i] = metrics.FrameSnapshot{
			At: time.Unix(int64(i+1), 0),
			Workers: []metrics.WorkerMetrics{
				{Endpoint: "real-ep", Label: "real"},
			},
		}
	}
	alert := &metrics.Alert{Title: "kv_critical"}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderEvictionTimeline panicked on unknown worker endpoint: %v", r)
		}
	}()
	_ = ui.RenderEvictionTimeline(frames, alert, "missing-ep", 5, 120, 30)
}

func TestRenderEvictionTimeline_NarrowWidth_DoesNotPanic(t *testing.T) {
	frames := make([]metrics.FrameSnapshot, 10)
	for i := range frames {
		frames[i] = metrics.FrameSnapshot{
			At: time.Unix(int64(i+1), 0),
			Workers: []metrics.WorkerMetrics{
				{Endpoint: "ep", Label: "ep"},
			},
		}
	}
	alert := &metrics.Alert{Title: "kv_critical"}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderEvictionTimeline panicked at narrow width: %v", r)
		}
	}()
	_ = ui.RenderEvictionTimeline(frames, alert, "ep", 5, 50, 20)
}
