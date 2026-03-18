// Package collector provides concurrent polling of LLM inference worker metrics.
package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

const (
	// MaxHistory is the number of historical snapshots to retain per worker.
	MaxHistory = 60
	// DefaultTimeout is the HTTP request timeout for metrics polling.
	// K8s API proxy requests need more headroom than direct HTTP — the request
	// traverses apiserver → kubelet → pod, and concurrent scrapes compete for
	// apiserver bandwidth.
	DefaultTimeout = 10 * time.Second
)

// Collector manages concurrent polling of multiple worker endpoints.
type Collector struct {
	mu       sync.RWMutex
	workers  map[string]*workerState
	client   *http.Client
	interval time.Duration
	cancel   context.CancelFunc
	polling  int32 // atomic: 1 if a poll is in progress
}

// workerState holds the current and historical metrics for a single worker.
type workerState struct {
	mu          sync.Mutex
	current     *metrics.WorkerMetrics
	history     []*metrics.WorkerMetrics
	prev        *metrics.WorkerMetrics // previous snapshot for rate calculation
	counters    counterState           // raw counter values for rate calculation (never exported)
	metricsPath string
	fetchFunc   func(ctx context.Context) (string, error)
}

// WorkerConfig describes a single worker endpoint to poll.
type WorkerConfig struct {
	Endpoint    string
	Label       string
	Backend     metrics.Backend // hint; auto-detected if Unknown
	MetricsPath string
	FetchFunc   func(ctx context.Context) (string, error) // optional: custom fetcher (e.g., K8s API proxy)
}

// New creates a new Collector with the given worker configs and polling interval.
func New(configs []WorkerConfig, interval time.Duration) *Collector {
	c := &Collector{
		workers: make(map[string]*workerState),
		client: &http.Client{
			Timeout: DefaultTimeout,
		},
		interval: interval,
	}
	for _, cfg := range configs {
		m := &metrics.WorkerMetrics{
			Endpoint: cfg.Endpoint,
			Label:    cfg.Label,
			Backend:  cfg.Backend,
			Online:   false,
		}
		mp := cfg.MetricsPath
		if mp == "" {
			mp = "/metrics"
		}
		c.workers[cfg.Endpoint] = &workerState{
			current:     m,
			metricsPath: mp,
			fetchFunc:   cfg.FetchFunc,
		}
	}
	return c
}

// Start begins polling all workers in the background.
func (c *Collector) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	go c.pollLoop(ctx)
}

// Stop halts background polling.
func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// GetAll returns a snapshot of all current worker metrics.
func (c *Collector) GetAll() []*metrics.WorkerMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*metrics.WorkerMetrics, 0, len(c.workers))
	for _, ws := range c.workers {
		ws.mu.Lock()
		m := copyMetrics(ws.current)
		ws.mu.Unlock()
		result = append(result, m)
	}
	return result
}

// GetHistory returns the history for a specific endpoint.
func (c *Collector) GetHistory(endpoint string) []*metrics.WorkerMetrics {
	c.mu.RLock()
	ws, ok := c.workers[endpoint]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	hist := make([]*metrics.WorkerMetrics, len(ws.history))
	for i, m := range ws.history {
		hist[i] = copyMetrics(m)
	}
	return hist
}

// PollNow triggers an immediate poll of all workers (non-blocking).
// The provided ctx is threaded into each HTTP request so that in-flight
// fetches are cancelled when the parent context is done.
func (c *Collector) PollNow(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&c.polling, 0, 1) {
		return // poll already in progress
	}
	defer atomic.StoreInt32(&c.polling, 0)

	c.mu.RLock()
	endpoints := make([]string, 0, len(c.workers))
	for ep := range c.workers {
		endpoints = append(endpoints, ep)
	}
	c.mu.RUnlock()

	var wg sync.WaitGroup
	for _, ep := range endpoints {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()
			c.pollWorker(ctx, endpoint)
		}(ep)
	}
	wg.Wait()
}

// AddWorker adds a new worker endpoint to poll.
func (c *Collector) AddWorker(cfg WorkerConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.workers[cfg.Endpoint]; exists {
		return
	}
	m := &metrics.WorkerMetrics{
		Endpoint: cfg.Endpoint,
		Label:    cfg.Label,
		Backend:  cfg.Backend,
		Online:   false,
	}
	mp := cfg.MetricsPath
	if mp == "" {
		mp = "/metrics"
	}
	c.workers[cfg.Endpoint] = &workerState{
		current:     m,
		metricsPath: mp,
		fetchFunc:   cfg.FetchFunc,
	}
}

// RemoveWorker removes a worker endpoint from polling.
func (c *Collector) RemoveWorker(endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.workers, endpoint)
}

// Endpoints returns the set of currently tracked worker endpoints.
func (c *Collector) Endpoints() map[string]struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	eps := make(map[string]struct{}, len(c.workers))
	for ep := range c.workers {
		eps[ep] = struct{}{}
	}
	return eps
}

func (c *Collector) pollLoop(ctx context.Context) {
	// Do an immediate poll on start
	c.PollNow(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.PollNow(ctx)
		}
	}
}

func (c *Collector) pollWorker(ctx context.Context, endpoint string) {
	c.mu.RLock()
	ws, ok := c.workers[endpoint]
	c.mu.RUnlock()
	if !ok {
		return
	}

	ws.mu.Lock()
	metricsPath := ws.metricsPath
	current := ws.current
	prev := ws.prev
	fetchFunc := ws.fetchFunc
	prevCounters := ws.counters
	ws.mu.Unlock()

	var body string
	var err error
	if fetchFunc != nil {
		fetchCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
		body, err = fetchFunc(fetchCtx)
	} else {
		url := endpoint + metricsPath
		body, err = c.fetchMetrics(url)
		if err != nil && metricsPath == "/metrics" {
			// Try /v1/metrics as fallback (NIM serves metrics there)
			body, err = c.fetchMetrics(endpoint + "/v1/metrics")
			if err == nil {
				ws.mu.Lock()
				ws.metricsPath = "/v1/metrics"
				ws.mu.Unlock()
			}
		}
	}
	if err != nil {
		// Mark offline, preserve last known data
		ws.mu.Lock()
		ws.current = &metrics.WorkerMetrics{
			Endpoint:  current.Endpoint,
			Label:     current.Label,
			Backend:   current.Backend,
			ModelName: current.ModelName,
			Online:    false,
			LastSeen:  current.LastSeen,
		}
		ws.mu.Unlock()
		return
	}

	// Ollama returns JSON from /api/ps, not Prometheus text.
	// Synthesize pseudo-Prometheus format before parsing.
	if current.Backend == metrics.BackendOllama {
		body = SynthesizeOllamaMetrics(body)
	}

	pm := metrics.ParseText(body)
	updated, newCounters := parseWorkerMetrics(current, prev, prevCounters, pm)

	ws.mu.Lock()
	ws.prev = copyMetrics(ws.current)
	ws.current = updated
	ws.counters = newCounters
	// Append to history
	ws.history = append(ws.history, copyMetrics(updated))
	if len(ws.history) > MaxHistory {
		ws.history = ws.history[len(ws.history)-MaxHistory:]
	}
	ws.mu.Unlock()
}

func (c *Collector) fetchMetrics(url string) (string, error) {
	resp, err := c.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body %s: %w", url, err)
	}
	return string(body), nil
}

// parseWorkerMetrics extracts WorkerMetrics from parsed prometheus data.
func parseWorkerMetrics(current, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) (*metrics.WorkerMetrics, counterState) {
	m := &metrics.WorkerMetrics{
		Endpoint: current.Endpoint,
		Label:    current.Label,
		Backend:  current.Backend,
		Online:   true,
		LastSeen: time.Now(),
	}

	// Short-circuit detection if backend AND model are both known.
	// Re-run detection when model is missing — some backends (Dynamo/vLLM)
	// don't emit the model_name label until the first request lands.
	if current.Backend != metrics.BackendUnknown && current.ModelName != "" {
		m.Backend = current.Backend
		m.ModelName = current.ModelName
	} else {
		// Detect backend and model from metrics content using registered parsers.
		for _, d := range detectors {
			if backend, model := d.Detect(pm); backend != metrics.BackendUnknown {
				m.Backend = backend
				m.ModelName = model
				break
			}
		}
	}

	var counters counterState
	if p, ok := parsers[m.Backend]; ok {
		counters = p.Parse(m, prev, prevCounters, pm)
	}

	// Carry over TTFT history
	if prev != nil {
		m.TTFTHistory = append(prev.TTFTHistory, m.TTFT_P99)
		if len(m.TTFTHistory) > MaxHistory {
			m.TTFTHistory = m.TTFTHistory[len(m.TTFTHistory)-MaxHistory:]
		}
		m.GenTokHistory = append(prev.GenTokHistory, m.GenTokPerSec)
		if len(m.GenTokHistory) > MaxHistory {
			m.GenTokHistory = m.GenTokHistory[len(m.GenTokHistory)-MaxHistory:]
		}
	} else {
		m.TTFTHistory = []float64{m.TTFT_P99}
		m.GenTokHistory = []float64{m.GenTokPerSec}
	}

	return m, counters
}

func copyMetrics(m *metrics.WorkerMetrics) *metrics.WorkerMetrics {
	if m == nil {
		return nil
	}
	cp := *m
	if m.TTFTHistory != nil {
		cp.TTFTHistory = make([]float64, len(m.TTFTHistory))
		copy(cp.TTFTHistory, m.TTFTHistory)
	}
	if m.GenTokHistory != nil {
		cp.GenTokHistory = make([]float64, len(m.GenTokHistory))
		copy(cp.GenTokHistory, m.GenTokHistory)
	}
	return &cp
}
