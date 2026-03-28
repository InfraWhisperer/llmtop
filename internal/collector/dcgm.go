package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// gpuKey uniquely identifies a GPU across hosts.
type gpuKey struct {
	Hostname string
	Index    int
}

// DCGMCollector polls DCGM exporter endpoints and maintains per-GPU state.
// Supports multiple fetch functions for DaemonSet deployments where each node
// runs its own exporter.
type DCGMCollector struct {
	mu         sync.RWMutex
	gpus       map[gpuKey]*metrics.GPUInfo
	endpoint   string
	client     *http.Client
	interval   time.Duration
	cancel     context.CancelFunc
	fetchFuncs []func(ctx context.Context) (string, error) // custom fetchers (e.g., K8s API proxy per node)
	polling    int32                                        // atomic: 1 if a poll is in progress
}

// NewDCGMCollector creates a collector targeting the given DCGM exporter base URL.
func NewDCGMCollector(endpoint string, interval time.Duration) *DCGMCollector {
	return &DCGMCollector{
		gpus:     make(map[gpuKey]*metrics.GPUInfo),
		endpoint: endpoint,
		client:   &http.Client{Timeout: DefaultTimeout},
		interval: interval,
	}
}

// NewDCGMCollectorWithFetchFuncs creates a collector that scrapes multiple DCGM
// exporter pods via custom fetch functions (e.g., K8s API server proxy).
// DCGM exporters are DaemonSets — one per GPU node — so we need one fetcher per pod.
func NewDCGMCollectorWithFetchFuncs(label string, interval time.Duration, fetchFuncs []func(ctx context.Context) (string, error)) *DCGMCollector {
	return &DCGMCollector{
		gpus:       make(map[gpuKey]*metrics.GPUInfo),
		endpoint:   label,
		interval:   interval,
		fetchFuncs: fetchFuncs,
	}
}

// Start begins polling the DCGM exporter in the background.
func (d *DCGMCollector) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	go d.pollLoop(ctx)
}

// Stop halts background polling.
func (d *DCGMCollector) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// PollNow triggers an immediate synchronous poll of all DCGM exporter pods.
// Each pod is scraped concurrently; results are merged into the shared GPU map.
// The provided ctx is threaded into each HTTP request so that in-flight
// fetches are cancelled when the parent context is done.
func (d *DCGMCollector) PollNow(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&d.polling, 0, 1) {
		return // poll already in progress
	}
	defer atomic.StoreInt32(&d.polling, 0)

	if len(d.fetchFuncs) > 0 {
		d.pollAllFetchFuncs(ctx)
		return
	}
	// Direct HTTP fallback (single endpoint, no K8s proxy)
	body, err := d.fetchMetrics(d.endpoint + "/metrics")
	if err != nil {
		return
	}
	pm := metrics.ParseText(body)
	d.mu.Lock()
	defer d.mu.Unlock()
	parseDCGMMetrics(d.gpus, pm)
}

// pollAllFetchFuncs scrapes all DCGM exporter pods concurrently and merges.
func (d *DCGMCollector) pollAllFetchFuncs(ctx context.Context) {
	type result struct {
		body string
	}
	results := make([]result, len(d.fetchFuncs))
	var wg sync.WaitGroup

	for i, fn := range d.fetchFuncs {
		wg.Add(1)
		go func(idx int, fetch func(ctx context.Context) (string, error)) {
			defer wg.Done()
			fetchCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()
			body, err := fetch(fetchCtx)
			if err != nil {
				return
			}
			results[idx] = result{body: body}
		}(i, fn)
	}
	wg.Wait()

	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range results {
		if r.body == "" {
			continue
		}
		pm := metrics.ParseText(r.body)
		parseDCGMMetrics(d.gpus, pm)
	}
}

// GetAll returns a snapshot of all GPU metrics sorted by (Hostname, Index).
func (d *DCGMCollector) GetAll() []*metrics.GPUInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*metrics.GPUInfo, 0, len(d.gpus))
	for _, g := range d.gpus {
		cp := copyGPUInfo(g)
		result = append(result, cp)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Hostname != result[j].Hostname {
			return result[i].Hostname < result[j].Hostname
		}
		return result[i].Index < result[j].Index
	})
	return result
}

// GetSummary returns aggregated GPU metrics.
func (d *DCGMCollector) GetSummary() metrics.GPUSummary {
	return metrics.ComputeGPUSummary(d.GetAll())
}

func (d *DCGMCollector) pollLoop(ctx context.Context) {
	d.PollNow(ctx)

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.PollNow(ctx)
		}
	}
}

func (d *DCGMCollector) fetchMetrics(url string) (string, error) {
	resp, err := d.client.Get(url)
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

// parseDCGMMetrics updates the gpu map from parsed Prometheus samples.
// It groups samples by (Hostname, gpu-index) and populates all known DCGM fields.
// Existing UtilHistory is preserved and extended.
func parseDCGMMetrics(gpus map[gpuKey]*metrics.GPUInfo, pm *metrics.ParsedMetrics) {
	// Collect raw per-GPU values keyed by (Hostname, Index).
	type rawGPU struct {
		uuid      string
		name      string
		hostname  string
		index     int
		pod       string
		namespace string
		container string
		util      float64
		fbUsed    float64
		fbFree    float64
		memBWUtil float64
		temp      float64
		power     float64
		nvlinkBW  float64
		eccErrors float64
		smClock   float64
		memClock  float64
		hasFBUsed bool
		hasFBFree bool
	}

	raw := make(map[gpuKey]*rawGPU)

	getOrCreate := func(hostname string, index int) *rawGPU {
		k := gpuKey{Hostname: hostname, Index: index}
		if r, ok := raw[k]; ok {
			return r
		}
		r := &rawGPU{hostname: hostname, index: index}
		raw[k] = r
		return r
	}

	for _, s := range pm.Samples {
		hostname := s.Labels["Hostname"]
		indexStr := s.Labels["gpu"]
		if hostname == "" || indexStr == "" {
			continue
		}
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			continue
		}

		r := getOrCreate(hostname, index)
		// Populate metadata from every sample that carries it.
		if v := s.Labels["UUID"]; v != "" {
			r.uuid = v
		}
		if v := s.Labels["modelName"]; v != "" {
			r.name = v
		}
		if v := s.Labels["pod"]; v != "" {
			r.pod = v
		}
		if v := s.Labels["namespace"]; v != "" {
			r.namespace = v
		}
		if v := s.Labels["container"]; v != "" {
			r.container = v
		}

		switch s.Name {
		case "DCGM_FI_DEV_GPU_UTIL":
			r.util = s.Value
		case "DCGM_FI_DEV_FB_USED":
			r.fbUsed = s.Value
			r.hasFBUsed = true
		case "DCGM_FI_DEV_FB_FREE":
			r.fbFree = s.Value
			r.hasFBFree = true
		case "DCGM_FI_DEV_GPU_TEMP":
			r.temp = s.Value
		case "DCGM_FI_DEV_POWER_USAGE":
			r.power = s.Value
		case "DCGM_FI_DEV_MEM_COPY_UTIL":
			r.memBWUtil = s.Value
		case "DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL":
			r.nvlinkBW = s.Value
		case "DCGM_FI_DEV_ECC_DBE_VOL_TOTAL":
			r.eccErrors = s.Value
		case "DCGM_FI_DEV_SM_CLOCK":
			r.smClock = s.Value
		case "DCGM_FI_DEV_MEM_CLOCK":
			r.memClock = s.Value
		}
	}

	// Merge raw data into the gpu map, carrying forward UtilHistory.
	for k, r := range raw {
		existing, ok := gpus[k]
		var history []float64
		if ok {
			history = existing.UtilHistory
		}

		history = append(history, r.util)
		if len(history) > MaxHistory {
			history = history[len(history)-MaxHistory:]
		}

		var memTotal float64
		if r.hasFBUsed && r.hasFBFree {
			memTotal = r.fbUsed + r.fbFree
		} else if ok {
			memTotal = existing.MemTotalMB
		}

		gpus[k] = &metrics.GPUInfo{
			Index:       r.index,
			UUID:        r.uuid,
			Name:        r.name,
			Hostname:    r.hostname,
			UtilPct:     r.util,
			MemUsedMB:   r.fbUsed,
			MemTotalMB:  memTotal,
			MemBWUtil:   r.memBWUtil,
			TempC:       r.temp,
			PowerW:      r.power,
			NVLinkGBs:   r.nvlinkBW,
			ECCErrors:   int64(r.eccErrors),
			SMClockMHz:  r.smClock,
			MemClockMHz: r.memClock,
			Pod:         r.pod,
			Namespace:   r.namespace,
			Container:   r.container,
			UtilHistory: history,
			LastScrape:  time.Now(),
		}
	}
}

func copyGPUInfo(g *metrics.GPUInfo) *metrics.GPUInfo {
	if g == nil {
		return nil
	}
	cp := *g
	if g.UtilHistory != nil {
		cp.UtilHistory = make([]float64, len(g.UtilHistory))
		copy(cp.UtilHistory, g.UtilHistory)
	}
	return &cp
}
