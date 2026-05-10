// Black-box contract tests for V1 (Time-Travel Buffer) and V10 (timeline window).
// Spec: docs/feature-spec-v2.md §3 V1, §3 V10, §7 glossary.
//
// These tests do not read the implementation; they pin the documented contract:
//   - FrameSnapshot shape and field semantics
//   - RingBuffer push/At/Len/Cap circular semantics
//   - SnapshotWorker drops history slices, shallow-copies tenant map
//   - CaptureFrame copies alerts (no aliasing)
//   - SparklineFor returns oldest-first TTFT P99 values, len <= n
//   - ExtractTimelineWindow centers around targetTime, oldest-first

package metrics_test

import (
	"sync"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// ─── FrameSnapshot ────────────────────────────────────────────────────────────

func TestFrameSnapshot_FieldsAreExportedAndAddressable(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := metrics.FrameSnapshot{
		At:      now,
		Workers: []metrics.WorkerMetrics{{Endpoint: "a"}},
		Summary: metrics.FleetSummary{},
		Alerts:  []metrics.Alert{},
	}
	if !f.At.Equal(now) {
		t.Fatalf("At round-trip failed: got %v want %v", f.At, now)
	}
	if len(f.Workers) != 1 || f.Workers[0].Endpoint != "a" {
		t.Fatalf("Workers slice not preserved")
	}
}

// ─── SnapshotWorker ──────────────────────────────────────────────────────────

func TestSnapshotWorker_TTFTHistoryNil(t *testing.T) {
	src := &metrics.WorkerMetrics{
		Endpoint:      "ep",
		TTFTHistory:   []float64{1, 2, 3},
		GenTokHistory: []float64{4, 5, 6},
	}
	snap := metrics.SnapshotWorker(src)
	if snap.TTFTHistory != nil {
		t.Errorf("TTFTHistory should be nil after SnapshotWorker, got %v", snap.TTFTHistory)
	}
}

func TestSnapshotWorker_GenTokHistoryNil(t *testing.T) {
	src := &metrics.WorkerMetrics{
		GenTokHistory: []float64{4, 5, 6},
	}
	snap := metrics.SnapshotWorker(src)
	if snap.GenTokHistory != nil {
		t.Errorf("GenTokHistory should be nil after SnapshotWorker, got %v", snap.GenTokHistory)
	}
}

func TestSnapshotWorker_PreservesEndpoint(t *testing.T) {
	src := &metrics.WorkerMetrics{Endpoint: "http://1.2.3.4:8000"}
	snap := metrics.SnapshotWorker(src)
	if snap.Endpoint != "http://1.2.3.4:8000" {
		t.Errorf("Endpoint not preserved: got %q", snap.Endpoint)
	}
}

func TestSnapshotWorker_TenantReqsShallowCopy_NewMapHeader(t *testing.T) {
	src := &metrics.WorkerMetrics{
		TenantReqs: map[string]float64{"team-a": 10.0},
	}
	snap := metrics.SnapshotWorker(src)
	// Adding to source must not appear in snapshot (separate map header).
	src.TenantReqs["team-b"] = 20.0
	if _, ok := snap.TenantReqs["team-b"]; ok {
		t.Errorf("snapshot saw mutation to source map — TenantReqs not shallow-copied")
	}
}

// ─── RingBuffer basic ─────────────────────────────────────────────────────────

func TestNewRingBuffer_LenIsZero(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	if r.Len() != 0 {
		t.Errorf("Len() should be 0 on empty ring, got %d", r.Len())
	}
}

func TestNewRingBuffer_CapMatchesArg(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	if r.Cap() != 10 {
		t.Errorf("Cap() should be 10, got %d", r.Cap())
	}
}

func TestRingBuffer_AtZero_IsMostRecentPush(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	f1 := metrics.FrameSnapshot{At: time.Unix(1, 0)}
	f2 := metrics.FrameSnapshot{At: time.Unix(2, 0)}
	r.Push(f1)
	r.Push(f2)
	got, ok := r.At(0)
	if !ok {
		t.Fatalf("At(0) returned ok=false on len=2 ring")
	}
	if !got.At.Equal(f2.At) {
		t.Errorf("At(0) should be most recent push (f2 at %v), got %v", f2.At, got.At)
	}
}

func TestRingBuffer_AtPosFour_IsOldestOnFiveFrames(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	got, ok := r.At(4)
	if !ok {
		t.Fatalf("At(4) returned ok=false on len=5 ring")
	}
	if !got.At.Equal(time.Unix(1, 0)) {
		t.Errorf("At(4) should be oldest (t=1), got %v", got.At)
	}
}

func TestRingBuffer_AtOutOfRange_ReturnsFalse(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	_, ok := r.At(5)
	if ok {
		t.Errorf("At(5) on len=5 ring should return ok=false")
	}
}

func TestRingBuffer_AtOnEmpty_ReturnsFalse(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	_, ok := r.At(0)
	if ok {
		t.Errorf("At(0) on empty ring should return ok=false")
	}
}

func TestRingBuffer_LenIncrementsUntilCap(t *testing.T) {
	r := metrics.NewRingBuffer(3)
	for i := 0; i < 3; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	if r.Len() != 3 {
		t.Errorf("Len() should be 3 after 3 pushes on cap=3 ring, got %d", r.Len())
	}
}

func TestRingBuffer_LenSaturatesAtCap(t *testing.T) {
	r := metrics.NewRingBuffer(3)
	for i := 0; i < 10; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	if r.Len() != 3 {
		t.Errorf("Len() should saturate at Cap=3 after 10 pushes, got %d", r.Len())
	}
}

// Spec acceptance: after Cap+1 pushes, At(Cap-1) is the second-ever-pushed frame.
func TestRingBuffer_PushOverflow_OverwritesOldest(t *testing.T) {
	cap := 5
	r := metrics.NewRingBuffer(cap)
	for i := 0; i < cap+1; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	// After cap+1 pushes, the oldest retained frame is the 2nd push (t=2).
	got, ok := r.At(cap - 1)
	if !ok {
		t.Fatalf("At(Cap-1) returned ok=false after cap+1 pushes")
	}
	if !got.At.Equal(time.Unix(2, 0)) {
		t.Errorf("After cap+1 pushes, At(Cap-1) should be 2nd push (t=2), got %v", got.At)
	}
}

// Spec orchestrator note: push cap+5 frames, At(0) most recent, At(cap-1) is the 6th push.
func TestRingBuffer_PushCapPlus5_AtZero_IsMostRecent(t *testing.T) {
	cap := 10
	r := metrics.NewRingBuffer(cap)
	for i := 0; i < cap+5; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	got, _ := r.At(0)
	expected := time.Unix(int64(cap+5), 0)
	if !got.At.Equal(expected) {
		t.Errorf("At(0) after cap+5 pushes should be t=%v, got %v", expected, got.At)
	}
}

func TestRingBuffer_PushCapPlus5_AtCapMinus1_IsSixthPush(t *testing.T) {
	cap := 10
	r := metrics.NewRingBuffer(cap)
	for i := 0; i < cap+5; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	got, _ := r.At(cap - 1)
	// Oldest retained is the (cap+5 - cap + 1)th = 6th push, value t=6.
	if !got.At.Equal(time.Unix(6, 0)) {
		t.Errorf("At(Cap-1) after cap+5 pushes should be 6th push (t=6), got %v", got.At)
	}
}

// ─── RingBuffer thread-safety ─────────────────────────────────────────────────

func TestRingBuffer_ConcurrentPush_NoPanicLenAtMostCap(t *testing.T) {
	cap := 50
	r := metrics.NewRingBuffer(cap)
	var wg sync.WaitGroup
	N := 200
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
		}(i)
	}
	wg.Wait()
	if r.Len() > cap {
		t.Errorf("Len() = %d exceeded Cap=%d after concurrent pushes", r.Len(), cap)
	}
	if r.Len() != cap {
		// N > cap so saturation expected.
		t.Errorf("Len() = %d, expected saturation at %d after %d concurrent pushes", r.Len(), cap, N)
	}
}

func TestRingBuffer_ConcurrentPushAndRead_NoPanic(t *testing.T) {
	r := metrics.NewRingBuffer(20)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
		}
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		default:
			_ = r.Len()
			_, _ = r.At(0)
		}
	}
}

// ─── SparklineFor ─────────────────────────────────────────────────────────────

func TestSparklineFor_EmptyRing_ReturnsNil(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	got := r.SparklineFor("ep", 60)
	if got != nil {
		t.Errorf("SparklineFor on empty ring should return nil, got %v", got)
	}
}

func TestSparklineFor_FewerSamplesThanN_ReturnsAvailable(t *testing.T) {
	r := metrics.NewRingBuffer(900)
	for i := 0; i < 30; i++ {
		w := metrics.WorkerMetrics{Endpoint: "ep"}
		w.TTFT.P99 = float64(i + 1)
		f := metrics.FrameSnapshot{
			At:      time.Unix(int64(i+1), 0),
			Workers: []metrics.WorkerMetrics{w},
		}
		r.Push(f)
	}
	got := r.SparklineFor("ep", 60)
	if len(got) != 30 {
		t.Errorf("SparklineFor with 30 frames and n=60 should return 30 values, got %d", len(got))
	}
}

func TestSparklineFor_OldestFirstOrdering(t *testing.T) {
	r := metrics.NewRingBuffer(900)
	// TTFT.P99 = i+1 for i = 0..99. After 100 pushes, last 50 P99 values (oldest-first) are 51..100.
	for i := 0; i < 100; i++ {
		w := metrics.WorkerMetrics{Endpoint: "ep"}
		w.TTFT.P99 = float64(i + 1)
		r.Push(metrics.FrameSnapshot{
			At:      time.Unix(int64(i+1), 0),
			Workers: []metrics.WorkerMetrics{w},
		})
	}
	got := r.SparklineFor("ep", 50)
	if len(got) != 50 {
		t.Fatalf("expected 50 samples, got %d", len(got))
	}
	if got[0] != 51.0 {
		t.Errorf("oldest sample should be 51.0 (oldest-first), got %v", got[0])
	}
	if got[49] != 100.0 {
		t.Errorf("newest sample should be 100.0, got %v", got[49])
	}
}

func TestSparklineFor_UnknownEndpoint_DoesNotPanic(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		w := metrics.WorkerMetrics{Endpoint: "ep-real"}
		w.TTFT.P99 = float64(i)
		r.Push(metrics.FrameSnapshot{Workers: []metrics.WorkerMetrics{w}})
	}
	// Unknown endpoint must not panic; spec is silent on exact return shape.
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("SparklineFor for unknown endpoint panicked: %v", rec)
		}
	}()
	_ = r.SparklineFor("ep-missing", 60)
}

// ─── CaptureFrame ─────────────────────────────────────────────────────────────

func TestCaptureFrame_AlertsAreNotAliased(t *testing.T) {
	w1 := &metrics.WorkerMetrics{Endpoint: "ep1"}
	src := []metrics.Alert{{Title: "kv_critical"}}
	snap := metrics.CaptureFrame([]*metrics.WorkerMetrics{w1}, metrics.FleetSummary{}, src)
	if len(snap.Alerts) != 1 {
		t.Fatalf("expected 1 alert in snap, got %d", len(snap.Alerts))
	}
	// Mutate source slice; the snapshot should NOT change.
	src[0].Title = "MUTATED"
	if snap.Alerts[0].Title == "MUTATED" {
		t.Errorf("snap.Alerts aliases the source — element mutation visible")
	}
}

func TestCaptureFrame_SnapshotsAllWorkers(t *testing.T) {
	ws := []*metrics.WorkerMetrics{
		{Endpoint: "a"}, {Endpoint: "b"}, {Endpoint: "c"},
	}
	snap := metrics.CaptureFrame(ws, metrics.FleetSummary{}, nil)
	if len(snap.Workers) != 3 {
		t.Errorf("CaptureFrame should snapshot all workers; got %d, want 3", len(snap.Workers))
	}
}

func TestCaptureFrame_DropsHistoryFields(t *testing.T) {
	w := &metrics.WorkerMetrics{
		Endpoint:    "ep",
		TTFTHistory: []float64{1, 2, 3},
	}
	snap := metrics.CaptureFrame([]*metrics.WorkerMetrics{w}, metrics.FleetSummary{}, nil)
	if snap.Workers[0].TTFTHistory != nil {
		t.Errorf("CaptureFrame must drop TTFTHistory in workers slice")
	}
}

// ─── ExtractTimelineWindow (V10) ─────────────────────────────────────────────

func TestExtractTimelineWindow_NilOnEmptyRing(t *testing.T) {
	r := metrics.NewRingBuffer(10)
	got := r.ExtractTimelineWindow(time.Unix(100, 0), 60)
	if got != nil {
		t.Errorf("ExtractTimelineWindow on empty ring should return nil, got len=%d", len(got))
	}
}

func TestExtractTimelineWindow_TargetMidRing_Returns2HalfPlus1(t *testing.T) {
	r := metrics.NewRingBuffer(200)
	for i := 0; i < 121; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	target := time.Unix(61, 0) // middle frame
	got := r.ExtractTimelineWindow(target, 60)
	if len(got) != 121 {
		t.Errorf("ExtractTimelineWindow with mid-ring target and halfWidth=60 should return 121 frames, got %d", len(got))
	}
}

func TestExtractTimelineWindow_OutsideRange_ReturnsNil(t *testing.T) {
	r := metrics.NewRingBuffer(50)
	for i := 0; i < 30; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(1000+i), 0)})
	}
	// Target far in the future, outside stored range.
	got := r.ExtractTimelineWindow(time.Unix(99999, 0), 60)
	if got != nil {
		t.Errorf("ExtractTimelineWindow with target outside range should return nil, got len=%d", len(got))
	}
}

func TestExtractTimelineWindow_OldestFirstOrdering(t *testing.T) {
	r := metrics.NewRingBuffer(200)
	for i := 0; i < 121; i++ {
		r.Push(metrics.FrameSnapshot{At: time.Unix(int64(i+1), 0)})
	}
	got := r.ExtractTimelineWindow(time.Unix(61, 0), 60)
	if len(got) < 2 {
		t.Fatalf("expected len>=2, got %d", len(got))
	}
	if !got[0].At.Before(got[len(got)-1].At) {
		t.Errorf("ExtractTimelineWindow should return frames oldest-first; first=%v last=%v",
			got[0].At, got[len(got)-1].At)
	}
}
