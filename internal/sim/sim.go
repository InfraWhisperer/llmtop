package sim

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"
)

// Config holds simulator configuration.
type Config struct {
	Scenario  string
	Seed      int64
	Workers   int // decode workers
	Prefill   int // prefill workers
	Tenants   int
	TickRate  time.Duration
	PortBase  int
	DCGMPort  int
	K8sPort   int
	Duration  time.Duration
	ModelName string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Scenario:  "steady",
		Seed:      0,
		Workers:   5,
		Prefill:   3,
		Tenants:   5,
		TickRate:  500 * time.Millisecond,
		PortBase:  19100,
		DCGMPort:  19200,
		K8sPort:   19300,
		ModelName: "meta-llama/Llama-3.1-70B-Instruct",
	}
}

// Simulator orchestrates the fake cluster and HTTP servers.
type Simulator struct {
	cfg       Config
	scenario  Scenario
	rng       *rand.Rand
	mu        sync.RWMutex
	workers   map[string]*WorkerState
	gpus      map[string]*GPUState
	simTime   float64 // simulated seconds elapsed
	cancel    context.CancelFunc

	// HTTP servers
	workerServers []*http.Server
	dcgmServer    *http.Server
	k8sServer     *http.Server

	// Listeners for dynamic port assignment in tests
	workerListeners []net.Listener
	dcgmListener    net.Listener
	k8sListener     net.Listener
}

// New creates a Simulator from the given config.
func New(cfg Config) *Simulator {
	if cfg.Seed == 0 {
		cfg.Seed = time.Now().UnixNano()
	}
	if cfg.ModelName == "" {
		cfg.ModelName = "meta-llama/Llama-3.1-70B-Instruct"
	}

	scenario := GetScenario(cfg.Scenario)
	if cfg.TickRate > 0 {
		scenario.TickRate = cfg.TickRate
	}
	if cfg.Duration > 0 {
		scenario.Duration = cfg.Duration
	}

	s := &Simulator{
		cfg:      cfg,
		scenario: scenario,
		rng:      rand.New(rand.NewSource(cfg.Seed)),
		workers:  make(map[string]*WorkerState),
		gpus:     make(map[string]*GPUState),
	}

	s.initTopology()
	return s
}

func (s *Simulator) initTopology() {
	port := s.cfg.PortBase
	seedBase := s.cfg.Seed

	prefillNames := []struct{ name, node string; gpu int }{
		{"sim-prefill-alpha", "sim-node-01", 0},
		{"sim-prefill-beta", "sim-node-01", 1},
		{"sim-prefill-gamma", "sim-node-02", 0},
	}

	decodeNames := []struct{ name, node string; gpu int }{
		{"sim-decode-00", "sim-node-01", 2},
		{"sim-decode-01", "sim-node-01", 3},
		{"sim-decode-02", "sim-node-02", 1},
		{"sim-decode-03", "sim-node-02", 2},
		{"sim-decode-04", "sim-node-03", 0},
	}

	// Create prefill workers (up to cfg.Prefill)
	for i := 0; i < s.cfg.Prefill && i < len(prefillNames); i++ {
		pn := prefillNames[i]
		ws := &WorkerState{
			Name:      pn.name,
			Role:      "prefill",
			NodeName:  pn.node,
			GPUID:     pn.gpu,
			ModelName: s.cfg.ModelName,
			Port:      port,
		}
		InitWorker(ws, seedBase+int64(i))
		s.workers[ws.Name] = ws

		gs := &GPUState{VRAMTotalGB: 80.0}
		InitGPU(gs, ws)
		s.gpus[gs.UUID] = gs

		port++
	}

	// Create decode workers (up to cfg.Workers)
	for i := 0; i < s.cfg.Workers && i < len(decodeNames); i++ {
		dn := decodeNames[i]
		ws := &WorkerState{
			Name:      dn.name,
			Role:      "decode",
			NodeName:  dn.node,
			GPUID:     dn.gpu,
			ModelName: s.cfg.ModelName,
			Port:      port,
		}
		InitWorker(ws, seedBase+int64(100+i))
		s.workers[ws.Name] = ws

		gs := &GPUState{VRAMTotalGB: 80.0}
		InitGPU(gs, ws)
		s.gpus[gs.UUID] = gs

		port++
	}

	// Error worker (always present)
	errWorker := &WorkerState{
		Name:      "sim-benchmark-runner",
		Role:      "decode",
		NodeName:  "sim-node-03",
		GPUID:     3,
		ModelName: s.cfg.ModelName,
		Port:      port,
		Online:    false,
	}
	errWorker.TenantReqs = make(map[string]int64)
	s.workers[errWorker.Name] = errWorker
}

// Start launches all HTTP servers and begins the tick loop.
func (s *Simulator) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	// Start worker metrics servers
	s.mu.RLock()
	workerList := make([]*WorkerState, 0, len(s.workers))
	for _, w := range s.workers {
		workerList = append(workerList, w)
	}
	s.mu.RUnlock()

	for _, ws := range workerList {
		w := ws // capture
		mux := http.NewServeMux()
		mux.HandleFunc("/metrics", func(rw http.ResponseWriter, _ *http.Request) {
			s.mu.RLock()
			defer s.mu.RUnlock()
			if !w.Online {
				http.Error(rw, "scrape error", http.StatusServiceUnavailable)
				return
			}
			rw.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(rw, RenderWorkerMetrics(w))
		})

		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", w.Port))
		if err != nil {
			// Try any available port
			ln, err = net.Listen("tcp", ":0")
			if err != nil {
				return fmt.Errorf("listen worker %s: %w", w.Name, err)
			}
			w.Port = ln.Addr().(*net.TCPAddr).Port
		}
		srv := &http.Server{Handler: mux}
		s.workerServers = append(s.workerServers, srv)
		s.workerListeners = append(s.workerListeners, ln)
		go func() { _ = srv.Serve(ln) }()
	}

	// Start DCGM server
	dcgmMux := http.NewServeMux()
	dcgmMux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		gpuList := make([]*GPUState, 0, len(s.gpus))
		for _, g := range s.gpus {
			gpuList = append(gpuList, g)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, RenderDCGMMetrics(gpuList))
	})

	dcgmLn, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.DCGMPort))
	if err != nil {
		dcgmLn, err = net.Listen("tcp", ":0")
		if err != nil {
			return fmt.Errorf("listen dcgm: %w", err)
		}
		s.cfg.DCGMPort = dcgmLn.Addr().(*net.TCPAddr).Port
	}
	s.dcgmServer = &http.Server{Handler: dcgmMux}
	s.dcgmListener = dcgmLn
	go func() { _ = s.dcgmServer.Serve(dcgmLn) }()

	// Start fake K8s API server
	k8sHandler := FakeK8sServer(workerList)
	k8sLn, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.K8sPort))
	if err != nil {
		k8sLn, err = net.Listen("tcp", ":0")
		if err != nil {
			return fmt.Errorf("listen k8s: %w", err)
		}
		s.cfg.K8sPort = k8sLn.Addr().(*net.TCPAddr).Port
	}
	s.k8sServer = &http.Server{Handler: k8sHandler}
	s.k8sListener = k8sLn
	go func() { _ = s.k8sServer.Serve(k8sLn) }()

	// Start tick loop
	go s.tickLoop(ctx)

	return nil
}

// Stop shuts down all servers.
func (s *Simulator) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, srv := range s.workerServers {
		_ = srv.Shutdown(ctx)
	}
	if s.dcgmServer != nil {
		_ = s.dcgmServer.Shutdown(ctx)
	}
	if s.k8sServer != nil {
		_ = s.k8sServer.Shutdown(ctx)
	}
}

func (s *Simulator) tickLoop(ctx context.Context) {
	tickRate := s.scenario.TickRate
	if tickRate == 0 {
		tickRate = 500 * time.Millisecond
	}

	ticker := time.NewTicker(tickRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(1.0) // each wall-clock tick = 1 simulated second

			if s.scenario.Duration > 0 && time.Duration(s.simTime)*time.Second >= s.scenario.Duration {
				return
			}
		}
	}
}

// Tick advances the simulation by dt simulated seconds.
func (s *Simulator) Tick(dt float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.simTime += dt

	// Apply scenario events
	for _, ev := range s.scenario.Events {
		if s.simTime >= ev.AtSecond && s.simTime < ev.AtSecond+dt+0.01 {
			ev.Apply(s.workers, s.gpus)
		}
	}

	// Tick all workers
	for _, w := range s.workers {
		TickWorker(w, dt)
	}

	// Tick all GPUs
	for _, g := range s.gpus {
		if w, ok := s.workers[g.WorkerName]; ok {
			TickGPU(g, w, s.rng, dt)
		}
	}
}

// SwitchScenario resets the simulation to t=0 of the named scenario. It
// re-runs InitWorker on every existing worker, reseeding from each worker's
// own RNG so the noise sequence diverges from the previous run while the
// topology (worker names, ports, GPU bindings) is preserved. Unknown names
// fall through to "steady" via GetScenario.
//
// Safe for concurrent use with Tick: the write lock blocks Tick until reset
// completes.
func (s *Simulator) SwitchScenario(name string) {
	scenario := GetScenario(name)
	if s.cfg.TickRate > 0 {
		scenario.TickRate = s.cfg.TickRate
	}
	if s.cfg.Duration > 0 {
		scenario.Duration = s.cfg.Duration
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.scenario = scenario
	s.simTime = 0

	for _, ws := range s.workers {
		// The benchmark-runner is deliberately permanently offline (topology
		// init sets it that way). Preserve that across scenario switches.
		preserveOffline := ws.Name == "sim-benchmark-runner"
		seed := s.rng.Int63()
		InitWorker(ws, seed)
		if preserveOffline {
			ws.Online = false
		}
	}
}

// AdvanceTo advances the simulation to the given simulated time.
func (s *Simulator) AdvanceTo(t time.Duration) {
	target := t.Seconds()
	for s.simTime < target {
		s.Tick(1.0)
	}
}

// K8sURL returns the fake K8s API server URL.
func (s *Simulator) K8sURL() string {
	if s.k8sListener != nil {
		return fmt.Sprintf("http://localhost:%d", s.k8sListener.Addr().(*net.TCPAddr).Port)
	}
	return fmt.Sprintf("http://localhost:%d", s.cfg.K8sPort)
}

// DCGMURL returns the fake DCGM exporter URL.
func (s *Simulator) DCGMURL() string {
	if s.dcgmListener != nil {
		return fmt.Sprintf("http://localhost:%d", s.dcgmListener.Addr().(*net.TCPAddr).Port)
	}
	return fmt.Sprintf("http://localhost:%d", s.cfg.DCGMPort)
}

// DCGMPort returns the actual DCGM listener port (useful after OS-assigned port).
func (s *Simulator) DCGMPort() int {
	if s.dcgmListener != nil {
		return s.dcgmListener.Addr().(*net.TCPAddr).Port
	}
	return s.cfg.DCGMPort
}

// K8sPort returns the actual K8s API listener port.
func (s *Simulator) K8sPort() int {
	if s.k8sListener != nil {
		return s.k8sListener.Addr().(*net.TCPAddr).Port
	}
	return s.cfg.K8sPort
}

// MetricsBaseURL returns the base URL for worker metrics.
func (s *Simulator) MetricsBaseURL() string {
	return fmt.Sprintf("http://localhost:%d", s.cfg.PortBase)
}

// WorkerURLs returns all worker endpoint URLs.
func (s *Simulator) WorkerURLs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var urls []string
	for _, w := range s.workers {
		urls = append(urls, fmt.Sprintf("http://localhost:%d", w.Port))
	}
	return urls
}

// WorkerInfo holds metadata for a simulated worker, exported for building
// collector.WorkerConfig entries with correct Role and NodeName.
type WorkerInfo struct {
	Name     string
	Role     string
	NodeName string
	Port     int
	Online   bool
}

// Workers returns metadata for all simulated workers.
func (s *Simulator) Workers() []WorkerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	infos := make([]WorkerInfo, 0, len(s.workers))
	for _, w := range s.workers {
		infos = append(infos, WorkerInfo{
			Name:     w.Name,
			Role:     w.Role,
			NodeName: w.NodeName,
			Port:     w.Port,
			Online:   w.Online,
		})
	}
	return infos
}
