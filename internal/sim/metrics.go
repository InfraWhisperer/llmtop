// Package sim provides a realistic simulation harness for llmtop.
// It generates fake vLLM, DCGM, and K8s API responses that exercise
// every llmtop tab with populated data.
package sim

import (
	"fmt"
	"math"
	"math/rand"
)

// TenantWeights maps tenant IDs to their share of total requests.
var TenantWeights = map[string]float64{
	"team-search":    0.35,
	"team-rag":       0.25,
	"team-copilot":   0.20,
	"team-analytics": 0.12,
	"team-infra":     0.08,
}

// WorkerState holds live simulation state for a single inference worker.
type WorkerState struct {
	Name        string
	Role        string // "prefill" | "decode"
	NodeName    string
	GPUID       int
	ModelName   string
	Port        int
	Online      bool // false = returns 503

	// Live values, updated every tick
	KVPercGPU    float64 // 0.0–1.0
	KVPercCPU    float64
	KVPercNVMe   float64
	QueueDepth   int
	RunningReqs  int
	TTFTp99Ms    float64
	ITLp99Ms     float64
	CacheHitRate float64 // 0.0–1.0
	TokPerSec    float64

	// Cumulative counters (monotonically increasing)
	PromptToks    int64
	GenToks       int64
	Preemptions   int64
	RequestsTotal int64

	// Per-tenant request counters
	TenantReqs map[string]int64

	// Internal RNG for reproducible noise
	rng *rand.Rand
}

// GPUState holds live simulation state for a single GPU.
type GPUState struct {
	GPUID       int
	NodeName    string
	WorkerName  string
	UUID        string
	SMUtil      float64
	MemBWUtil   float64
	TempC       float64
	PowerW      float64
	NVLinkGBs   float64
	ECCErrors   int64
	VRAMUsedGB  float64
	VRAMTotalGB float64 // always 80.0
}

// BaseValues returns the default starting metrics for a role.
func BaseValues(role string) (kvGPU, queue, ttft, itl, tok, smUtil float64) {
	switch role {
	case "prefill":
		return 0.55, 2, 850, 200, 4000, 65
	case "decode":
		return 0.45, 0, 400, 145, 1500, 40
	default:
		return 0.30, 0, 500, 150, 1000, 30
	}
}

// InitWorker sets a worker to its base values.
func InitWorker(w *WorkerState, seed int64) {
	w.rng = rand.New(rand.NewSource(seed))
	w.Online = true
	w.TenantReqs = make(map[string]int64)

	kvGPU, queue, ttft, itl, tok, _ := BaseValues(w.Role)
	w.KVPercGPU = kvGPU
	w.KVPercCPU = kvGPU * 0.55
	w.KVPercNVMe = 0
	w.QueueDepth = int(queue)
	w.RunningReqs = int(queue) + 1
	w.TTFTp99Ms = ttft
	w.ITLp99Ms = itl
	w.CacheHitRate = 0.35
	w.TokPerSec = tok
}

// InitGPU sets a GPU to values derived from its associated worker.
func InitGPU(g *GPUState, w *WorkerState) {
	_, _, _, _, _, smUtil := BaseValues(w.Role)
	g.GPUID = w.GPUID
	g.NodeName = w.NodeName
	g.WorkerName = w.Name
	g.UUID = fmt.Sprintf("GPU-%s-%d", w.NodeName, w.GPUID)
	g.SMUtil = smUtil
	g.MemBWUtil = smUtil * 0.8
	g.TempC = smUtil*0.6 + 40
	g.PowerW = smUtil*3.5 + 60
	g.NVLinkGBs = w.TokPerSec * 0.002
	g.ECCErrors = 0
	g.VRAMUsedGB = w.KVPercGPU * 80.0 * 0.8
	g.VRAMTotalGB = 80.0
}

// TickWorker advances a worker's state by dt seconds with noise and correlations.
func TickWorker(w *WorkerState, dt float64) {
	if !w.Online {
		return
	}

	// Noise helper: random delta ±3% of base per second
	noise := func(base, magnitude float64) float64 {
		return (w.rng.Float64()*2 - 1) * magnitude * base * dt
	}

	_, baseQueue, baseTTFT, baseITL, baseTok, baseSM := BaseValues(w.Role)

	// Apply noise to KV GPU, clamped 0–1
	w.KVPercGPU = clamp(w.KVPercGPU+noise(0.55, 0.03), 0, 1)

	// Correlations: higher KV → lower cache hit, higher queue → higher TTFT
	w.CacheHitRate = clamp(0.45-w.KVPercGPU*0.3+noise(0.35, 0.02), 0.05, 0.65)
	w.KVPercCPU = clamp(w.KVPercGPU*0.55+noise(0.3, 0.02), 0, 1)
	w.KVPercNVMe = clamp(w.KVPercGPU*0.15+noise(0.1, 0.01), 0, 1)

	// Queue drifts around base
	qDelta := noise(baseQueue+1, 0.05)
	w.QueueDepth = clampInt(w.QueueDepth+int(math.Round(qDelta)), 0, 100)
	w.RunningReqs = clampInt(w.QueueDepth+1+w.rng.Intn(3), 1, 50)

	// TTFT correlated with queue depth
	queueFactor := 1.0 + float64(w.QueueDepth)*0.05
	w.TTFTp99Ms = clamp(baseTTFT*queueFactor+noise(baseTTFT, 0.03), 50, 30000)

	// ITL with noise
	w.ITLp99Ms = clamp(baseITL+noise(baseITL, 0.03), 20, 15000)

	// TokPerSec with noise
	w.TokPerSec = clamp(baseTok+noise(baseTok, 0.03), 100, 50000)

	// Increment counters proportional to load
	reqsDelta := int64(float64(w.RunningReqs) * dt * 2)
	w.RequestsTotal += reqsDelta
	w.PromptToks += int64(w.TokPerSec * dt * 0.4)
	w.GenToks += int64(w.TokPerSec * dt * 0.6)

	// Preemptions correlated with KV pressure
	if w.KVPercGPU > 0.7 {
		w.Preemptions += int64((w.KVPercGPU - 0.7) * 50 * dt)
	}

	// Distribute requests across tenants
	for tenant, weight := range TenantWeights {
		w.TenantReqs[tenant] += int64(float64(reqsDelta) * weight)
	}

	// SM util correlated with KV and queue
	_ = baseSM
	w.updateGPUDerivedFields()
}

func (w *WorkerState) updateGPUDerivedFields() {
	// These are read by GPU tick
}

// TickGPU updates GPU state from its associated worker.
func TickGPU(g *GPUState, w *WorkerState, rng *rand.Rand, dt float64) {
	if !w.Online {
		return
	}

	_, _, _, _, _, baseSM := BaseValues(w.Role)

	// SM util correlated with KV pressure and queue
	smTarget := baseSM + w.KVPercGPU*20 + float64(w.QueueDepth)*2
	g.SMUtil = clamp(g.SMUtil+(smTarget-g.SMUtil)*0.3*dt+(rng.Float64()*2-1)*2, 5, 100)

	// Derived fields
	g.MemBWUtil = clamp(g.SMUtil*0.8+(rng.Float64()*2-1)*3, 5, 100)
	g.TempC = clamp(g.SMUtil*0.6+40+(rng.Float64()*2-1)*2, 30, 95)
	g.PowerW = clamp(g.SMUtil*3.5+60+(rng.Float64()*2-1)*10, 50, 700)
	g.NVLinkGBs = clamp(w.TokPerSec*0.002+(rng.Float64()*2-1)*5, 0, 300)
	g.VRAMUsedGB = clamp(w.KVPercGPU*80.0*0.8+(rng.Float64()*2-1)*2, 10, 80)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
