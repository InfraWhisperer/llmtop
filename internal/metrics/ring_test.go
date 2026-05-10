package metrics

import (
	"sync"
	"testing"
	"time"
)

func mkWorker(endpoint string, ttftP99 float64) *WorkerMetrics {
	return &WorkerMetrics{
		Endpoint:      endpoint,
		Label:         endpoint,
		Online:        true,
		TTFT:          LatencySnapshot{P99: ttftP99},
		TTFT_P99:      ttftP99,
		TTFTHistory:   []float64{1, 2, 3},
		GenTokHistory: []float64{4, 5, 6},
		TenantReqs:    map[string]float64{"team-a": 100},
	}
}

func TestSnapshotWorker_DropsHistorySlices(t *testing.T) {
	w := mkWorker("http://w1", 200)
	cp := SnapshotWorker(w)
	if cp.TTFTHistory != nil {
		t.Errorf("TTFTHistory must be nil in snapshot, got %v", cp.TTFTHistory)
	}
	if cp.GenTokHistory != nil {
		t.Errorf("GenTokHistory must be nil in snapshot, got %v", cp.GenTokHistory)
	}
	// Source slice must be untouched.
	if len(w.TTFTHistory) != 3 {
		t.Errorf("source TTFTHistory mutated: %v", w.TTFTHistory)
	}
}

func TestSnapshotWorker_TenantReqsShallowCopy(t *testing.T) {
	w := mkWorker("http://w1", 200)
	cp := SnapshotWorker(w)
	w.TenantReqs["team-a"] = 999 // mutate map after snapshot
	w.TenantReqs["team-b"] = 50  // add a key
	if cp.TenantReqs["team-a"] != 100 {
		t.Errorf("snapshot tenant value should not reflect post-snapshot mutation, got %v", cp.TenantReqs["team-a"])
	}
	if _, ok := cp.TenantReqs["team-b"]; ok {
		t.Errorf("snapshot should not contain post-snapshot key team-b")
	}
}

func TestSnapshotWorker_NilInput(t *testing.T) {
	got := SnapshotWorker(nil)
	if got.Endpoint != "" {
		t.Errorf("nil input should yield zero value, got %+v", got)
	}
}

func TestCaptureFrame_AlertsCopied(t *testing.T) {
	alerts := []Alert{
		{Severity: AlertWarning, Title: "a"},
		{Severity: AlertCritical, Title: "b"},
	}
	frame := CaptureFrame([]*WorkerMetrics{mkWorker("http://w1", 100)}, FleetSummary{TotalWorkers: 1}, alerts)
	if len(frame.Alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(frame.Alerts))
	}
	// Mutating the source must not affect the frame.
	alerts[0].Title = "MUTATED"
	if frame.Alerts[0].Title != "a" {
		t.Errorf("frame.Alerts[0] aliased source: got %q", frame.Alerts[0].Title)
	}
}

func TestCaptureFrame_WorkerHistoriesDropped(t *testing.T) {
	frame := CaptureFrame(
		[]*WorkerMetrics{mkWorker("http://w1", 200)},
		FleetSummary{},
		nil,
	)
	if len(frame.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(frame.Workers))
	}
	if frame.Workers[0].TTFTHistory != nil {
		t.Errorf("frame worker still has TTFTHistory: %v", frame.Workers[0].TTFTHistory)
	}
}

func TestRingBuffer_AtNewest(t *testing.T) {
	r := NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		f := FrameSnapshot{At: time.Unix(int64(i), 0)}
		r.Push(f)
	}
	if r.Len() != 5 {
		t.Fatalf("Len = %d, want 5", r.Len())
	}
	got, ok := r.At(0)
	if !ok {
		t.Fatal("At(0) returned !ok")
	}
	if !got.At.Equal(time.Unix(4, 0)) {
		t.Errorf("At(0) returned frame at %v, want unix(4)", got.At)
	}
}

func TestRingBuffer_AtOldest(t *testing.T) {
	r := NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		r.Push(FrameSnapshot{At: time.Unix(int64(i), 0)})
	}
	got, ok := r.At(4)
	if !ok {
		t.Fatal("At(4) returned !ok")
	}
	if !got.At.Equal(time.Unix(0, 0)) {
		t.Errorf("At(4) (oldest) returned frame at %v, want unix(0)", got.At)
	}
}

func TestRingBuffer_AtOutOfBounds(t *testing.T) {
	r := NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		r.Push(FrameSnapshot{At: time.Unix(int64(i), 0)})
	}
	if _, ok := r.At(5); ok {
		t.Error("At(5) on 5-frame ring should be !ok")
	}
	if _, ok := r.At(-1); ok {
		t.Error("At(-1) should be !ok")
	}
}

func TestRingBuffer_CircularOverwrite(t *testing.T) {
	r := NewRingBuffer(3)
	for i := 0; i < 5; i++ {
		r.Push(FrameSnapshot{At: time.Unix(int64(i), 0)})
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (cap)", r.Len())
	}
	got, _ := r.At(0)
	if !got.At.Equal(time.Unix(4, 0)) {
		t.Errorf("newest after overwrite: %v, want unix(4)", got.At)
	}
	got, _ = r.At(2)
	if !got.At.Equal(time.Unix(2, 0)) {
		t.Errorf("oldest after overwrite: %v, want unix(2)", got.At)
	}
}

func TestRingBuffer_LenAndCap(t *testing.T) {
	r := NewRingBuffer(8)
	if r.Cap() != 8 {
		t.Errorf("Cap = %d, want 8", r.Cap())
	}
	if r.Len() != 0 {
		t.Errorf("empty Len = %d, want 0", r.Len())
	}
	r.Push(FrameSnapshot{})
	if r.Len() != 1 {
		t.Errorf("Len after 1 push = %d, want 1", r.Len())
	}
}

func TestRingBuffer_NewRingBufferCapZero(t *testing.T) {
	r := NewRingBuffer(0)
	if r.Cap() != 1 {
		t.Errorf("zero cap should clamp to 1, got %d", r.Cap())
	}
	r.Push(FrameSnapshot{})
	if r.Len() != 1 {
		t.Errorf("Len = %d", r.Len())
	}
}

func TestRingBuffer_SparklineFor(t *testing.T) {
	r := NewRingBuffer(100)
	for i := 0; i < 30; i++ {
		w := mkWorker("http://w1", float64(100+i))
		r.Push(CaptureFrame([]*WorkerMetrics{w}, FleetSummary{}, nil))
	}
	got := r.SparklineFor("http://w1", 60)
	if len(got) != 30 {
		t.Fatalf("len = %d, want 30 (clamped to ring len)", len(got))
	}
	// Oldest-first ordering: first sample is the first push (TTFT=100), last is TTFT=129.
	if got[0] != 100 || got[29] != 129 {
		t.Errorf("ordering wrong: got[0]=%v got[29]=%v, want 100 and 129", got[0], got[29])
	}
}

func TestRingBuffer_SparklineFor_MissingEndpoint(t *testing.T) {
	r := NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		r.Push(CaptureFrame([]*WorkerMetrics{mkWorker("http://w1", 100)}, FleetSummary{}, nil))
	}
	if got := r.SparklineFor("http://nope", 60); got != nil {
		t.Errorf("missing endpoint should return nil, got %v", got)
	}
}

func TestRingBuffer_SparklineFor_Empty(t *testing.T) {
	r := NewRingBuffer(10)
	if got := r.SparklineFor("anything", 10); got != nil {
		t.Errorf("empty ring should return nil, got %v", got)
	}
}

func TestRingBuffer_SparklineFor_NonPositiveN(t *testing.T) {
	r := NewRingBuffer(10)
	r.Push(CaptureFrame([]*WorkerMetrics{mkWorker("http://w1", 50)}, FleetSummary{}, nil))
	if got := r.SparklineFor("http://w1", 0); got != nil {
		t.Errorf("n=0 should return nil, got %v", got)
	}
}

func TestRingBuffer_ExtractTimelineWindow_Centered(t *testing.T) {
	r := NewRingBuffer(200)
	base := time.Unix(0, 0)
	for i := 0; i < 121; i++ {
		f := FrameSnapshot{At: base.Add(time.Duration(i) * time.Second)}
		r.Push(f)
	}
	// Target sits at i=60 (the 61st pushed frame).
	target := base.Add(60 * time.Second)
	out := r.ExtractTimelineWindow(target, 60)
	if len(out) != 121 {
		t.Fatalf("len = %d, want 121", len(out))
	}
	// Oldest-first ordering: out[0] is i=0, out[60] is target, out[120] is i=120.
	if !out[0].At.Equal(base) {
		t.Errorf("out[0].At = %v, want base", out[0].At)
	}
	if !out[60].At.Equal(target) {
		t.Errorf("out[60].At = %v, want target %v", out[60].At, target)
	}
	if !out[120].At.Equal(base.Add(120 * time.Second)) {
		t.Errorf("out[120].At = %v, want +120s", out[120].At)
	}
}

func TestRingBuffer_ExtractTimelineWindow_OutsideRange(t *testing.T) {
	r := NewRingBuffer(50)
	base := time.Unix(1000, 0)
	for i := 0; i < 10; i++ {
		r.Push(FrameSnapshot{At: base.Add(time.Duration(i) * time.Second)})
	}
	if got := r.ExtractTimelineWindow(base.Add(-time.Hour), 5); got != nil {
		t.Errorf("target before range should return nil, got %d frames", len(got))
	}
	if got := r.ExtractTimelineWindow(base.Add(time.Hour), 5); got != nil {
		t.Errorf("target after range should return nil, got %d frames", len(got))
	}
}

func TestRingBuffer_ExtractTimelineWindow_TruncatedLeft(t *testing.T) {
	r := NewRingBuffer(200)
	base := time.Unix(0, 0)
	// Push 71 frames; target on frame 10 — only 10 frames before, 60 after = 71 total.
	for i := 0; i < 71; i++ {
		r.Push(FrameSnapshot{At: base.Add(time.Duration(i) * time.Second)})
	}
	target := base.Add(10 * time.Second)
	out := r.ExtractTimelineWindow(target, 60)
	if len(out) != 71 {
		t.Errorf("len = %d, want 71 (10 pre + target + 60 post)", len(out))
	}
}

func TestRingBuffer_ExtractTimelineWindow_Empty(t *testing.T) {
	r := NewRingBuffer(10)
	if got := r.ExtractTimelineWindow(time.Now(), 5); got != nil {
		t.Errorf("empty ring should return nil, got %d frames", len(got))
	}
}

func TestRingBuffer_ConcurrentPushAt(t *testing.T) {
	r := NewRingBuffer(100)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Push(FrameSnapshot{At: time.Unix(int64(i*100+j), 0)})
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = r.At(0)
				_ = r.Len()
			}
		}()
	}
	wg.Wait()
	if r.Len() != 100 {
		t.Errorf("Len = %d, want 100 (cap)", r.Len())
	}
}
