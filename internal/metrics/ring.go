package metrics

import (
	"sync"
	"time"
)

// FrameSnapshot is an immutable point-in-time capture of the full fleet state.
// It contains the workers slice, fleet summary, and active+resolved alerts at
// one poll tick.
//
// Slice fields TTFTHistory and GenTokHistory on each WorkerMetrics are dropped
// (set to nil) by SnapshotWorker — sparkline history is reconstructed from the
// ring itself via SparklineFor. TenantReqs maps are shallow-copied: the map
// header is fresh, but value entries reference the same backing keys/values
// captured at snapshot time. Frames are read-only after Push; callers must
// not mutate fields on a returned FrameSnapshot.
//
// All fields are exported to keep the type gob-serializable for the future
// V5 replay format (see spec §4): no interface fields, no unexported
// embedding, no pointers to internal state.
type FrameSnapshot struct {
	At      time.Time
	Workers []WorkerMetrics
	Summary FleetSummary
	Alerts  []Alert
}

// SnapshotWorker produces a value copy of w suitable for inclusion in a
// FrameSnapshot. It drops TTFTHistory and GenTokHistory (re-derivable from
// the ring) and shallow-copies TenantReqs so post-snapshot mutations to the
// caller's map header do not surface in the frame.
//
// Memory: dropping the two history slices is what keeps each WorkerMetrics
// at ~250B in the snapshot rather than ~5KB with full per-worker histories.
func SnapshotWorker(w *WorkerMetrics) WorkerMetrics {
	if w == nil {
		return WorkerMetrics{}
	}
	cp := *w
	cp.TTFTHistory = nil
	cp.GenTokHistory = nil
	if w.TenantReqs != nil {
		m := make(map[string]float64, len(w.TenantReqs))
		for k, v := range w.TenantReqs {
			m[k] = v
		}
		cp.TenantReqs = m
	}
	return cp
}

// CaptureFrame builds a FrameSnapshot from live worker pointers, a fleet
// summary, and the alert manager's current All() output. Each worker is
// passed through SnapshotWorker (drops history slices, shallow-copies
// tenant maps). The alerts slice is copied — the snapshot does not alias
// the AlertManager's internal storage.
func CaptureFrame(workers []*WorkerMetrics, summary FleetSummary, alerts []Alert) FrameSnapshot {
	w := make([]WorkerMetrics, len(workers))
	for i, src := range workers {
		w[i] = SnapshotWorker(src)
	}
	a := make([]Alert, len(alerts))
	copy(a, alerts)
	return FrameSnapshot{
		At:      time.Now(),
		Workers: w,
		Summary: summary,
		Alerts:  a,
	}
}

// RingBuffer holds up to Cap FrameSnapshots in a circular array.
// All exported methods are safe for concurrent use.
//
// Sizing rationale (see spec §7 invariant 5): capacity = ceil(retention /
// pollInterval). The default app-layer retention is 30 minutes; at the
// default 2-second poll interval that yields 900 ticks. Memory budget on a
// 50-worker cluster: 900 frames × 50 workers × ~250 B per WorkerMetrics
// (history slices excluded) ≈ 11 MB plus ~1 MB of alert slices. A
// 200-worker cluster lands around 45 MB, still inside the spec's 50 MB
// process budget. Callers compute Cap from the live poll interval; the
// ring itself does not police the budget.
type RingBuffer struct {
	mu   sync.RWMutex
	buf  []FrameSnapshot
	head int
	len  int
	cap  int
}

// NewRingBuffer allocates a ring with the given capacity. A non-positive cap
// is clamped to 1 — callers should compute capacity from poll interval and
// retention; this guard prevents a zero-cap panic in Push.
func NewRingBuffer(cap int) *RingBuffer {
	if cap < 1 {
		cap = 1
	}
	return &RingBuffer{
		buf: make([]FrameSnapshot, cap),
		cap: cap,
	}
}

// Push appends a new frame, overwriting the oldest when full.
func (r *RingBuffer) Push(f FrameSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = f
	r.head = (r.head + 1) % r.cap
	if r.len < r.cap {
		r.len++
	}
}

// At returns the frame at logical offset from newest (0 = newest, 1 = one
// tick back). Returns (FrameSnapshot{}, false) when offset >= Len().
func (r *RingBuffer) At(offset int) (FrameSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if offset < 0 || offset >= r.len {
		return FrameSnapshot{}, false
	}
	// Newest frame sits at (head-1) mod cap.
	idx := (r.head - 1 - offset) % r.cap
	if idx < 0 {
		idx += r.cap
	}
	return r.buf[idx], true
}

// Len returns the number of valid frames stored.
func (r *RingBuffer) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.len
}

// Cap returns the ring capacity.
func (r *RingBuffer) Cap() int {
	return r.cap
}

// SparklineFor returns up to n TTFT P99 values for the worker matching
// endpoint, oldest-first, drawn from the most recent n frames where the
// endpoint appears. Returns nil when the endpoint never appears in the
// last n frames.
func (r *RingBuffer) SparklineFor(endpoint string, n int) []float64 {
	if n <= 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.len == 0 {
		return nil
	}
	limit := n
	if limit > r.len {
		limit = r.len
	}
	// Walk from oldest of the last `limit` frames forward.
	out := make([]float64, 0, limit)
	for i := limit - 1; i >= 0; i-- {
		idx := (r.head - 1 - i) % r.cap
		if idx < 0 {
			idx += r.cap
		}
		f := r.buf[idx]
		for j := range f.Workers {
			if f.Workers[j].Endpoint == endpoint {
				out = append(out, f.Workers[j].TTFT.P99)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ExtractTimelineWindow returns up to 2*halfWidth+1 frames centered on the
// frame whose At timestamp is closest to targetTime, oldest-first. Returns
// nil when the ring is empty or targetTime falls outside the ring's stored
// time range.
func (r *RingBuffer) ExtractTimelineWindow(targetTime time.Time, halfWidth int) []FrameSnapshot {
	if halfWidth < 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.len == 0 {
		return nil
	}

	// Walk frames newest-first to find timestamp bounds and the closest match.
	oldestAt := r.frameAt(r.len - 1).At
	newestAt := r.frameAt(0).At
	if targetTime.Before(oldestAt) || targetTime.After(newestAt) {
		return nil
	}

	bestOffset := -1
	var bestDelta time.Duration
	for off := 0; off < r.len; off++ {
		f := r.frameAt(off)
		d := f.At.Sub(targetTime)
		if d < 0 {
			d = -d
		}
		if bestOffset < 0 || d < bestDelta {
			bestOffset = off
			bestDelta = d
		}
	}
	if bestOffset < 0 {
		return nil
	}

	// Window: [center-halfWidth ... center+halfWidth] in offset-from-newest space.
	startOff := bestOffset + halfWidth
	endOff := bestOffset - halfWidth
	if startOff > r.len-1 {
		startOff = r.len - 1
	}
	if endOff < 0 {
		endOff = 0
	}
	out := make([]FrameSnapshot, 0, startOff-endOff+1)
	for off := startOff; off >= endOff; off-- {
		out = append(out, r.frameAt(off))
	}
	return out
}

// frameAt returns the frame at the given logical offset. Caller must hold
// r.mu (read or write). Bounds are caller-checked: 0 <= offset < r.len.
func (r *RingBuffer) frameAt(offset int) FrameSnapshot {
	idx := (r.head - 1 - offset) % r.cap
	if idx < 0 {
		idx += r.cap
	}
	return r.buf[idx]
}
