package sim

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Seed = 42
	cfg.PortBase = 0  // let OS assign
	cfg.DCGMPort = 0
	cfg.K8sPort = 0
	return cfg
}

func TestSimulatorStartStop(t *testing.T) {
	cfg := testConfig()
	cfg.Scenario = "steady"
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Verify workers were created
	s.mu.RLock()
	workerCount := len(s.workers)
	s.mu.RUnlock()

	// 3 prefill + 5 decode + 1 error = 9
	if workerCount != 9 {
		t.Errorf("expected 9 workers, got %d", workerCount)
	}
}

func TestWorkerMetricsEndpoint(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Tick once to populate counters
	s.Tick(1.0)

	// Find an online worker
	s.mu.RLock()
	var port int
	for _, w := range s.workers {
		if w.Online {
			port = w.Port
			break
		}
	}
	s.mu.RUnlock()

	if port == 0 {
		t.Fatal("no online worker found")
	}

	resp, err := http.Get(urlFor(port, "/metrics"))
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	for _, metric := range []string{
		"vllm:gpu_cache_usage_perc",
		"vllm:num_requests_running",
		"vllm:prompt_tokens_total",
		"vllm:time_to_first_token_seconds_bucket",
	} {
		if !strings.Contains(text, metric) {
			t.Errorf("worker /metrics missing %q", metric)
		}
	}
}

func TestErrorWorkerReturns503(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	s.mu.RLock()
	var port int
	for _, w := range s.workers {
		if !w.Online {
			port = w.Port
			break
		}
	}
	s.mu.RUnlock()

	if port == 0 {
		t.Fatal("no offline worker found")
	}

	resp, err := http.Get(urlFor(port, "/metrics"))
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 503 {
		t.Errorf("expected 503 for error worker, got %d", resp.StatusCode)
	}
}

func TestDCGMMetricsEndpoint(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	s.Tick(1.0)

	resp, err := http.Get(urlFor(s.DCGMPort(), "/metrics"))
	if err != nil {
		t.Fatalf("GET DCGM /metrics failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	for _, metric := range []string{
		"DCGM_FI_DEV_GPU_UTIL",
		"DCGM_FI_DEV_GPU_TEMP",
		"DCGM_FI_DEV_FB_USED",
		"DCGM_FI_DEV_ECC_DBE_VOL_TOTAL",
	} {
		if !strings.Contains(text, metric) {
			t.Errorf("DCGM /metrics missing %q", metric)
		}
	}

	// Should have exported_pod label
	if !strings.Contains(text, "exported_pod=") {
		t.Error("DCGM metrics missing exported_pod label")
	}
}

func TestK8sPodListEndpoint(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get(urlFor(s.K8sPort(), "/api/v1/namespaces/inference/pods"))
	if err != nil {
		t.Fatalf("GET pods failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	if !strings.Contains(text, "PodList") {
		t.Error("response missing PodList kind")
	}
	if !strings.Contains(text, "sim-prefill-alpha") {
		t.Error("pod list missing sim-prefill-alpha")
	}
	if !strings.Contains(text, "nvidia.com/grove-role") {
		t.Error("pod list missing grove-role label")
	}
}

func TestScenarioAdvanceTo(t *testing.T) {
	cfg := testConfig()
	cfg.Scenario = "demo"
	s := New(cfg)

	s.AdvanceTo(30 * time.Second)

	s.mu.RLock()
	gamma := s.workers["sim-prefill-gamma"]
	s.mu.RUnlock()

	if gamma == nil {
		t.Fatal("sim-prefill-gamma not found")
	}

	// After the demo scenario at t=25, gamma should have elevated KV
	if gamma.KVPercGPU < 0.50 {
		t.Errorf("expected elevated KV on gamma after demo events, got %.2f", gamma.KVPercGPU)
	}
}

func TestStressScenarioFires(t *testing.T) {
	cfg := testConfig()
	cfg.Scenario = "stress"
	s := New(cfg)

	s.AdvanceTo(10 * time.Second)

	s.mu.RLock()
	var highKV bool
	for _, w := range s.workers {
		if w.Online && w.KVPercGPU > 0.85 {
			highKV = true
		}
	}
	s.mu.RUnlock()

	if !highKV {
		t.Error("expected at least one worker with KV > 85% during stress scenario")
	}
}

func TestChaosScenarioNoPanic(t *testing.T) {
	cfg := testConfig()
	cfg.Scenario = "chaos"
	s := New(cfg)

	// Run 60 simulated seconds — should not panic
	for i := 0; i < 60; i++ {
		s.Tick(1.0)
	}
}

func TestTickCountersMonotonicallyIncrease(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)

	s.Tick(1.0)

	s.mu.RLock()
	var w *WorkerState
	for _, ws := range s.workers {
		if ws.Online {
			w = ws
			break
		}
	}
	prevPrompt := w.PromptToks
	prevGen := w.GenToks
	s.mu.RUnlock()

	s.Tick(1.0)

	s.mu.RLock()
	if w.PromptToks < prevPrompt {
		t.Errorf("PromptToks decreased: %d → %d", prevPrompt, w.PromptToks)
	}
	if w.GenToks < prevGen {
		t.Errorf("GenToks decreased: %d → %d", prevGen, w.GenToks)
	}
	s.mu.RUnlock()
}

func TestTenantDistribution(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)

	// Tick several times to build up counts
	for i := 0; i < 10; i++ {
		s.Tick(1.0)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, w := range s.workers {
		if !w.Online {
			continue
		}
		if len(w.TenantReqs) == 0 {
			t.Errorf("worker %s has no tenant request distribution", w.Name)
			continue
		}
		// team-search should have the highest count (35% weight)
		searchCount := w.TenantReqs["team-search"]
		infraCount := w.TenantReqs["team-infra"]
		if searchCount <= infraCount {
			t.Errorf("worker %s: team-search (%d) should exceed team-infra (%d)", w.Name, searchCount, infraCount)
		}
	}
}

func TestReproducibleWithSeed(t *testing.T) {
	run := func() float64 {
		cfg := testConfig()
		cfg.Seed = 12345
		s := New(cfg)
		s.Tick(1.0)
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, w := range s.workers {
			if w.Name == "sim-prefill-alpha" {
				return w.KVPercGPU
			}
		}
		return 0
	}

	v1 := run()
	v2 := run()
	if v1 != v2 {
		t.Errorf("same seed produced different KVPercGPU: %f vs %f", v1, v2)
	}
}

func urlFor(port int, path string) string {
	return fmt.Sprintf("http://localhost:%d%s", port, path)
}

func TestSwitchScenario_ResetsSimTime(t *testing.T) {
	cfg := testConfig()
	cfg.Scenario = "demo"
	s := New(cfg)

	// Advance into the demo's KV-pressure window.
	s.AdvanceTo(20 * time.Second)
	s.mu.RLock()
	if s.simTime < 20 {
		s.mu.RUnlock()
		t.Fatalf("expected simTime >= 20, got %v", s.simTime)
	}
	s.mu.RUnlock()

	s.SwitchScenario("steady")

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.simTime != 0 {
		t.Errorf("after SwitchScenario simTime = %v, want 0", s.simTime)
	}
	if s.scenario.Name != "steady" {
		t.Errorf("scenario.Name = %q, want \"steady\"", s.scenario.Name)
	}
}

func TestSwitchScenario_ResetsWorkersToBaseline(t *testing.T) {
	cfg := testConfig()
	cfg.Scenario = "demo"
	s := New(cfg)
	s.AdvanceTo(25 * time.Second)

	s.mu.RLock()
	gamma := s.workers["sim-prefill-gamma"]
	preKV := gamma.KVPercGPU
	s.mu.RUnlock()
	if preKV < 0.50 {
		t.Fatalf("demo at t=25 should have elevated KV on gamma, got %v", preKV)
	}

	s.SwitchScenario("steady")

	s.mu.RLock()
	defer s.mu.RUnlock()
	postKV := s.workers["sim-prefill-gamma"].KVPercGPU
	if postKV >= preKV {
		t.Errorf("expected KV to drop after switch to steady, pre=%v post=%v", preKV, postKV)
	}
}

func TestSwitchScenario_PreservesErrorWorkerOffline(t *testing.T) {
	cfg := testConfig()
	cfg.Scenario = "demo"
	s := New(cfg)
	s.SwitchScenario("steady")
	s.mu.RLock()
	defer s.mu.RUnlock()
	errW := s.workers["sim-benchmark-runner"]
	if errW == nil {
		t.Fatal("benchmark runner missing")
	}
	if errW.Online {
		t.Error("benchmark runner should remain offline after SwitchScenario")
	}
}

func TestSwitchScenario_UnknownFallsBackToSteady(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)
	s.SwitchScenario("nonexistent")
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.scenario.Name != "steady" {
		t.Errorf("unknown scenario should fall back to steady, got %q", s.scenario.Name)
	}
}

func TestSwitchScenario_ConcurrentWithTick(t *testing.T) {
	cfg := testConfig()
	s := New(cfg)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			s.Tick(0.5)
		}
		close(done)
	}()
	for i := 0; i < 20; i++ {
		s.SwitchScenario("steady")
	}
	<-done
}

func TestRenderWorkerMetrics_NVMeGaugeEmittedWhenNonzero(t *testing.T) {
	w := &WorkerState{Name: "w", Role: "prefill", ModelName: "m", Online: true, KVPercNVMe: 0.12}
	InitWorker(w, 1)
	w.KVPercNVMe = 0.12 // restore after Init zeroed it
	out := RenderWorkerMetrics(w)
	if !strings.Contains(out, "vllm:nvme_cache_usage_perc") {
		t.Errorf("expected NVMe gauge present when KVPercNVMe>0:\n%s", out)
	}
}

func TestRenderWorkerMetrics_NVMeGaugeAbsentWhenZero(t *testing.T) {
	w := &WorkerState{Name: "w", Role: "prefill", ModelName: "m", Online: true}
	InitWorker(w, 1)
	w.KVPercNVMe = 0
	out := RenderWorkerMetrics(w)
	if strings.Contains(out, "vllm:nvme_cache_usage_perc") {
		t.Errorf("expected no NVMe gauge when KVPercNVMe==0:\n%s", out)
	}
}

func TestRenderWorkerMetrics_KVTransferHistogram_PrefillOnly(t *testing.T) {
	prefill := &WorkerState{Name: "p", Role: "prefill", ModelName: "m", Online: true}
	InitWorker(prefill, 1)
	prefill.RequestsTotal = 100
	prefill.TTFTp99Ms = 1000
	out := RenderWorkerMetrics(prefill)
	if !strings.Contains(out, "vllm:kv_cache_offload_time_seconds_bucket") {
		t.Errorf("expected histogram buckets for prefill:\n%s", out)
	}

	decode := &WorkerState{Name: "d", Role: "decode", ModelName: "m", Online: true}
	InitWorker(decode, 1)
	decode.RequestsTotal = 100
	decode.TTFTp99Ms = 1000
	out2 := RenderWorkerMetrics(decode)
	if strings.Contains(out2, "vllm:kv_cache_offload_time_seconds") {
		t.Errorf("expected no KV transfer histogram for decode:\n%s", out2)
	}
}
