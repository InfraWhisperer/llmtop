// Package metrics defines the core data structures for LLM inference worker metrics.
package metrics

import "time"

// Backend represents the type of LLM inference backend.
type Backend string

const (
	BackendVLLM    Backend = "vLLM"
	BackendSGLang  Backend = "SGLang"
	BackendLMCache Backend = "LMCache"
	BackendNIM     Backend = "NIM"
	BackendTGI     Backend = "TGI"
	BackendTRTLLM  Backend = "TRT-LLM"
	BackendTriton   Backend = "Triton"
	BackendLlamaCpp Backend = "llama.cpp"
	BackendLiteLLM  Backend = "LiteLLM"
	BackendOllama   Backend = "Ollama"
	BackendUnknown  Backend = "Unknown"
)

// WorkerMetrics holds all collected metrics for a single inference worker endpoint.
type WorkerMetrics struct {
	Endpoint  string
	Label     string
	Backend   Backend
	ModelName string
	Online    bool
	LastSeen  time.Time
	Role     string // "prefill", "decode", or "mono"
	NodeName string // K8s node running this worker (empty for local discovery)

	// Load
	RequestsRunning int
	RequestsWaiting int

	// KV Cache
	KVCacheUsagePct    float64 // 0-100 (GPU)
	KVCacheUsageCPUPct float64 // 0-100 (CPU, may be absent)
	CacheHitRatePct    float64 // 0-100

	// Latency (milliseconds)
	TTFT_P50 float64
	TTFT_P99 float64
	ITL_P50  float64
	ITL_P99  float64

	// Throughput
	PromptTokPerSec float64
	GenTokPerSec    float64

	// KV eviction rate (preemptions per second, computed from counter)
	EvictPerSec float64

	// LMCache specific
	StoreSizeBytes  float64
	EvictionTotal   float64

	// History for sparklines (last N samples)
	TTFTHistory []float64
	GenTokHistory []float64
}

// FleetSummary aggregates metrics across all workers.
type FleetSummary struct {
	TotalWorkers   int
	OnlineWorkers  int
	TotalReqPerSec float64
	AvgCacheHit    float64
	AvgKVPercGPU   float64 // cluster-wide average GPU KV cache usage (0-100)
	P99TTFT        float64
	TotalTokPerSec float64
	PrefillCount   int
	DecodeCount    int
}

// ComputeFleetSummary computes aggregate stats from a slice of worker metrics.
func ComputeFleetSummary(workers []*WorkerMetrics) FleetSummary {
	s := FleetSummary{
		TotalWorkers: len(workers),
	}
	var cacheHitSum float64
	var cacheCount int
	var kvGPUSum float64
	var kvGPUCount int
	var maxTTFT float64
	for _, w := range workers {
		switch w.Role {
		case "prefill":
			s.PrefillCount++
		case "decode":
			s.DecodeCount++
		}
		if w.Online {
			s.OnlineWorkers++
			s.TotalTokPerSec += w.PromptTokPerSec + w.GenTokPerSec
			if w.CacheHitRatePct > 0 {
				cacheHitSum += w.CacheHitRatePct
				cacheCount++
			}
			if w.KVCacheUsagePct > 0 {
				kvGPUSum += w.KVCacheUsagePct
				kvGPUCount++
			}
			if w.TTFT_P99 > maxTTFT {
				maxTTFT = w.TTFT_P99
			}
		}
	}
	if cacheCount > 0 {
		s.AvgCacheHit = cacheHitSum / float64(cacheCount)
	}
	if kvGPUCount > 0 {
		s.AvgKVPercGPU = kvGPUSum / float64(kvGPUCount)
	}
	s.P99TTFT = maxTTFT
	return s
}
