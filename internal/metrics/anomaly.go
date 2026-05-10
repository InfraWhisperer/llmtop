package metrics

import (
	"math"
	"sort"
	"sync"
)

// metricNames are the six WorkerMetrics fields tracked by WelfordBank.
// Stable ordering matters — MaxSigma walks this list and returns the first
// metric at the highest |sigma|, so a deterministic order yields stable
// tiebreaks for tests and for the gutter halo.
const (
	metricKVCacheUsagePct = "KV%"
	metricTTFTP99         = "TTFT P99"
	metricITLP99          = "ITL P99"
	metricCacheHitRatePct = "Hit%"
	metricRequestsWaiting = "Queue"
	metricGenTokPerSec    = "tok/s"
)

// minSamplesForSigma is the warm-up threshold. MaxSigma and Anomalies return
// empty results until every tracked metric on the bank has at least this many
// observations; this prevents false halos during the first few minutes after
// a worker appears.
const minSamplesForSigma = 30

// WelfordState tracks online mean and variance for a single scalar series
// using Welford's algorithm. Not safe for concurrent use; callers
// (WelfordBank, AnomalyStore) synchronize.
type WelfordState struct {
	Count int64
	Mean  float64
	M2    float64
}

// Update incorporates a new observation.
func (w *WelfordState) Update(v float64) {
	w.Count++
	delta := v - w.Mean
	w.Mean += delta / float64(w.Count)
	delta2 := v - w.Mean
	w.M2 += delta * delta2
}

// Variance returns the sample variance. Returns 0 when Count < 2.
func (w *WelfordState) Variance() float64 {
	if w.Count < 2 {
		return 0
	}
	return w.M2 / float64(w.Count-1)
}

// Stddev returns the sample standard deviation. Returns 0 when Count < 2.
func (w *WelfordState) Stddev() float64 {
	v := w.Variance()
	if v <= 0 {
		return 0
	}
	return math.Sqrt(v)
}

// Sigma returns the signed number of standard deviations v sits from Mean.
// Returns 0 when Stddev() is 0 — an undefined sigma must not surface as
// infinity in the UI.
func (w *WelfordState) Sigma(v float64) float64 {
	sd := w.Stddev()
	if sd == 0 {
		return 0
	}
	return (v - w.Mean) / sd
}

// AnomalyDetail describes one anomalous metric on a worker.
type AnomalyDetail struct {
	MetricName string
	Sigma      float64 // signed; positive = above mean
	Mean       float64
	Stddev     float64
	CurrentVal float64
}

// WelfordBank holds per-metric Welford state for one worker over a rolling
// window of WindowSize observations. The bank stores raw samples per metric
// in a circular buffer; when an observation falls off the back of the
// window the bank recomputes mean/M2 from the remaining samples.
//
// Storage cost per bank: 6 metrics × WindowSize × 8 bytes. At 150 ticks
// (5 min @ 2s poll) and 200 workers: 6 × 150 × 8 × 200 ≈ 1.44 MB.
type WelfordBank struct {
	KVCacheUsagePct WelfordState
	TTFTP99         WelfordState
	ITLP99          WelfordState
	CacheHitRatePct WelfordState
	RequestsWaiting WelfordState
	GenTokPerSec    WelfordState

	WindowSize int

	// Circular per-metric raw-sample buffers; len == WindowSize when full.
	bufKV          []float64
	bufTTFT        []float64
	bufITL         []float64
	bufHit         []float64
	bufQueue       []float64
	bufGenTok      []float64
	headKV         int
	headTTFT       int
	headITL        int
	headHit        int
	headQueue      int
	headGenTok     int
	stalenessTicks int // ticks since last Update; AnomalyStore uses this for eviction
}

// newWelfordBank constructs an empty bank sized for the given window.
func newWelfordBank(windowSize int) *WelfordBank {
	if windowSize < 1 {
		windowSize = 1
	}
	return &WelfordBank{
		WindowSize: windowSize,
		bufKV:      make([]float64, 0, windowSize),
		bufTTFT:    make([]float64, 0, windowSize),
		bufITL:     make([]float64, 0, windowSize),
		bufHit:     make([]float64, 0, windowSize),
		bufQueue:   make([]float64, 0, windowSize),
		bufGenTok:  make([]float64, 0, windowSize),
	}
}

// Update incorporates a new WorkerMetrics observation into all six tracked
// series. When the rolling window is full, the oldest sample per metric
// ages out and the corresponding WelfordState is recomputed from the
// surviving samples.
func (b *WelfordBank) Update(w *WorkerMetrics) {
	if w == nil {
		return
	}
	b.stalenessTicks = 0
	b.observe(&b.KVCacheUsagePct, &b.bufKV, &b.headKV, w.KVCacheUsagePct)
	b.observe(&b.TTFTP99, &b.bufTTFT, &b.headTTFT, w.TTFT.P99)
	b.observe(&b.ITLP99, &b.bufITL, &b.headITL, w.ITL.P99)
	b.observe(&b.CacheHitRatePct, &b.bufHit, &b.headHit, w.CacheHitRatePct)
	b.observe(&b.RequestsWaiting, &b.bufQueue, &b.headQueue, float64(w.RequestsWaiting))
	b.observe(&b.GenTokPerSec, &b.bufGenTok, &b.headGenTok, w.GenTokPerSec)
}

// observe inserts v into the bank's metric-specific circular buffer and
// updates the corresponding WelfordState. When the buffer is full, the
// oldest sample is evicted and mean/M2 are recomputed from the surviving
// samples — necessary because Welford's online formulas don't support
// removal.
//
// A zero-value WelfordBank (WindowSize == 0) accumulates online forever
// with no eviction; this is the unbounded baseline. Construction via
// newWelfordBank uses the rolling window.
func (b *WelfordBank) observe(state *WelfordState, buf *[]float64, head *int, v float64) {
	if b.WindowSize <= 0 {
		state.Update(v)
		return
	}
	if len(*buf) < b.WindowSize {
		*buf = append(*buf, v)
		state.Update(v)
		return
	}
	// Window is full — evict slot at head, write v there, recompute from scratch.
	(*buf)[*head] = v
	*head = (*head + 1) % b.WindowSize
	*state = recomputeWelford(*buf)
}

// recomputeWelford recomputes mean and M2 from a slice of raw samples.
// This is O(n) and runs only on window-eviction events.
func recomputeWelford(samples []float64) WelfordState {
	var s WelfordState
	for _, v := range samples {
		s.Update(v)
	}
	return s
}

// hasWarmupComplete reports whether every tracked metric has reached the
// minimum sample count for sigma reporting.
func (b *WelfordBank) hasWarmupComplete() bool {
	if b.KVCacheUsagePct.Count < minSamplesForSigma {
		return false
	}
	if b.TTFTP99.Count < minSamplesForSigma {
		return false
	}
	if b.ITLP99.Count < minSamplesForSigma {
		return false
	}
	if b.CacheHitRatePct.Count < minSamplesForSigma {
		return false
	}
	if b.RequestsWaiting.Count < minSamplesForSigma {
		return false
	}
	if b.GenTokPerSec.Count < minSamplesForSigma {
		return false
	}
	return true
}

// MaxSigma returns the highest |sigma| across all six tracked metrics for
// the given live worker observation, plus the metric name that produced it.
// Returns ("", 0) when the bank has not completed warm-up.
//
// Sigma is computed against the bank's current rolling baseline; w supplies
// the live values being tested for deviation.
func (b *WelfordBank) MaxSigma(w *WorkerMetrics) (float64, string) {
	if w == nil || !b.hasWarmupComplete() {
		return 0, ""
	}
	type candidate struct {
		name  string
		sigma float64
	}
	cands := []candidate{
		{metricKVCacheUsagePct, b.KVCacheUsagePct.Sigma(w.KVCacheUsagePct)},
		{metricTTFTP99, b.TTFTP99.Sigma(w.TTFT.P99)},
		{metricITLP99, b.ITLP99.Sigma(w.ITL.P99)},
		{metricCacheHitRatePct, b.CacheHitRatePct.Sigma(w.CacheHitRatePct)},
		{metricRequestsWaiting, b.RequestsWaiting.Sigma(float64(w.RequestsWaiting))},
		{metricGenTokPerSec, b.GenTokPerSec.Sigma(w.GenTokPerSec)},
	}
	var best candidate
	for _, c := range cands {
		if math.Abs(c.sigma) > math.Abs(best.sigma) {
			best = c
		}
	}
	if best.name == "" {
		return 0, ""
	}
	return best.sigma, best.name
}

// Anomalies returns up to 3 metrics with |sigma| >= threshold, sorted by
// |sigma| descending. Returns nil when the bank has not completed warm-up.
func (b *WelfordBank) Anomalies(w *WorkerMetrics, threshold float64) []AnomalyDetail {
	if w == nil || !b.hasWarmupComplete() {
		return nil
	}
	type entry struct {
		state   *WelfordState
		name    string
		current float64
	}
	entries := []entry{
		{&b.KVCacheUsagePct, metricKVCacheUsagePct, w.KVCacheUsagePct},
		{&b.TTFTP99, metricTTFTP99, w.TTFT.P99},
		{&b.ITLP99, metricITLP99, w.ITL.P99},
		{&b.CacheHitRatePct, metricCacheHitRatePct, w.CacheHitRatePct},
		{&b.RequestsWaiting, metricRequestsWaiting, float64(w.RequestsWaiting)},
		{&b.GenTokPerSec, metricGenTokPerSec, w.GenTokPerSec},
	}
	out := make([]AnomalyDetail, 0, len(entries))
	for _, e := range entries {
		sd := e.state.Stddev()
		if sd == 0 {
			continue
		}
		sigma := (e.current - e.state.Mean) / sd
		if math.Abs(sigma) < threshold {
			continue
		}
		out = append(out, AnomalyDetail{
			MetricName: e.name,
			Sigma:      sigma,
			Mean:       e.state.Mean,
			Stddev:     sd,
			CurrentVal: e.current,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return math.Abs(out[i].Sigma) > math.Abs(out[j].Sigma)
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// AnomalyGetter is the read-only interface the UI consumes — minimal so
// tests can inject a mock without depending on AnomalyStore.
type AnomalyGetter interface {
	MaxSigma(endpoint string, w *WorkerMetrics) (float64, string)
	Anomalies(endpoint string, w *WorkerMetrics) []AnomalyDetail
}

// AnomalyStore manages a set of WelfordBanks keyed by worker endpoint.
// Banks for workers that disappear from successive Update calls are evicted
// after 2 × windowTicks of staleness — long enough that a brief blip
// doesn't lose baseline state, short enough that pod churn doesn't leak
// memory.
type AnomalyStore struct {
	mu     sync.RWMutex
	banks  map[string]*WelfordBank
	window int
}

// NewAnomalyStore creates a store with the given rolling-window size in
// ticks. Caller computes ticks from poll interval (e.g. 5 minutes / 2s = 150).
func NewAnomalyStore(windowTicks int) *AnomalyStore {
	if windowTicks < 1 {
		windowTicks = 1
	}
	return &AnomalyStore{
		banks:  make(map[string]*WelfordBank),
		window: windowTicks,
	}
}

// Update incorporates the live workers slice. Workers absent from this
// slice age out their banks (stalenessTicks++); banks past 2 × window
// staleness are evicted.
func (s *AnomalyStore) Update(workers []*WorkerMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]struct{}, len(workers))
	for _, w := range workers {
		if w == nil || w.Endpoint == "" {
			continue
		}
		seen[w.Endpoint] = struct{}{}
		bank, ok := s.banks[w.Endpoint]
		if !ok {
			bank = newWelfordBank(s.window)
			s.banks[w.Endpoint] = bank
		}
		// An offline worker's metrics are stale; do not pollute the baseline.
		if !w.Online {
			bank.stalenessTicks++
			continue
		}
		bank.Update(w)
	}
	for ep, bank := range s.banks {
		if _, ok := seen[ep]; ok {
			continue
		}
		bank.stalenessTicks++
		if bank.stalenessTicks > 2*s.window {
			delete(s.banks, ep)
		}
	}
}

// MaxSigma returns the highest |sigma| across all tracked metrics for
// endpoint, given the live observation w. Returns (0, "") when the
// endpoint has no bank or warm-up is incomplete.
func (s *AnomalyStore) MaxSigma(endpoint string, w *WorkerMetrics) (float64, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bank, ok := s.banks[endpoint]
	if !ok {
		return 0, ""
	}
	return bank.MaxSigma(w)
}

// Anomalies returns the per-metric anomaly details for endpoint at threshold
// 2σ. Returns nil when no bank exists, warm-up is incomplete, or no metric
// crosses the threshold.
func (s *AnomalyStore) Anomalies(endpoint string, w *WorkerMetrics) []AnomalyDetail {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bank, ok := s.banks[endpoint]
	if !ok {
		return nil
	}
	return bank.Anomalies(w, 2.0)
}
