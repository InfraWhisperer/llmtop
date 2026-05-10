package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestRenderEvictionTimeline_NilFrames_DataUnavailable(t *testing.T) {
	out := RenderEvictionTimeline(nil, &metrics.Alert{Title: "kv"}, "ep", 0, 120, 30)
	if !strings.Contains(out, "data unavailable") {
		t.Errorf("expected 'data unavailable' for nil frames, got %q", out)
	}
}

func TestRenderEvictionTimeline_EmptyFrames_DataUnavailable(t *testing.T) {
	out := RenderEvictionTimeline([]metrics.FrameSnapshot{}, &metrics.Alert{Title: "kv"}, "ep", 0, 120, 30)
	if !strings.Contains(out, "data unavailable") {
		t.Errorf("expected 'data unavailable' for empty frames, got %q", out)
	}
}

func TestRenderEvictionTimeline_RendersTracks(t *testing.T) {
	frames := make([]metrics.FrameSnapshot, 21)
	for i := range frames {
		w := metrics.WorkerMetrics{Endpoint: "ep", Label: "ep"}
		w.KVCacheUsagePct = float64(50 + i)
		w.RequestsWaiting = i
		w.EvictPerSec = float64(i)
		w.CacheHitRatePct = 80 - float64(i)
		frames[i] = metrics.FrameSnapshot{
			At:      time.Unix(int64(1000+i), 0),
			Workers: []metrics.WorkerMetrics{w},
		}
	}
	out := RenderEvictionTimeline(frames, &metrics.Alert{Title: "kv_critical"}, "ep", 10, 120, 30)
	for _, want := range []string{"KV%", "Queue", "Evict/s", "Hit %"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in timeline output", want)
		}
	}
}

func TestRenderEvictionTimeline_NilAlert_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked on nil alert: %v", r)
		}
	}()
	frames := []metrics.FrameSnapshot{
		{At: time.Now(), Workers: []metrics.WorkerMetrics{{Endpoint: "ep"}}},
	}
	_ = RenderEvictionTimeline(frames, nil, "ep", 0, 120, 30)
}

func TestRenderEvictionTimeline_LabelFallback(t *testing.T) {
	// When endpoint doesn't match but alert.Source matches Label.
	w := metrics.WorkerMetrics{Endpoint: "different-ep", Label: "match-label", KVCacheUsagePct: 88}
	frames := []metrics.FrameSnapshot{
		{At: time.Now(), Workers: []metrics.WorkerMetrics{w}},
	}
	alert := &metrics.Alert{Title: "kv_critical", Source: "match-label"}
	out := RenderEvictionTimeline(frames, alert, "", 0, 120, 30)
	if out == "" {
		t.Errorf("expected non-empty output via label fallback")
	}
}

func TestIsKVEvictionAlert(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"KV cache critical on w1", true},
		{"KV pressure on w2", true},
		{"TTFT spike on w3", false},
		{"scrape failure", false},
		{"", false},
	}
	for _, c := range cases {
		got := isKVEvictionAlert(&metrics.Alert{Title: c.title})
		if got != c.want {
			t.Errorf("isKVEvictionAlert(%q) = %v, want %v", c.title, got, c.want)
		}
	}
	if isKVEvictionAlert(nil) {
		t.Errorf("isKVEvictionAlert(nil) should be false")
	}
}

func TestFindT0Idx_PicksClosest(t *testing.T) {
	t0 := time.Unix(1500, 0)
	frames := []metrics.FrameSnapshot{
		{At: time.Unix(1000, 0)},
		{At: time.Unix(1499, 0)}, // closest
		{At: time.Unix(2000, 0)},
	}
	if got := findT0Idx(frames, t0); got != 1 {
		t.Errorf("findT0Idx: expected 1, got %d", got)
	}
}
