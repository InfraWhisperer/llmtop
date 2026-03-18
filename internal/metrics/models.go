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
	BackendTriton  Backend = "Triton"
	BackendUnknown Backend = "Unknown"
)

// WorkerMetrics holds all collected metrics for a single inference worker endpoint.
type WorkerMetrics struct {
	Endpoint  string
	Label     string
	Backend   Backend
	ModelName string
	Online    bool
	LastSeen  time.Time

	// Load
	RequestsRunning int
	RequestsWaiting int

	// KV Cache
	KVCacheUsagePct float64 // 0-100
	CacheHitRatePct float64 // 0-100

	// Latency (milliseconds)
	TTFT_P50 float64
	TTFT_P99 float64
	ITL_P50  float64
	ITL_P99  float64

	// Throughput
	PromptTokPerSec float64
	GenTokPerSec    float64

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
	P99TTFT        float64
	TotalTokPerSec float64
}

// ComputeFleetSummary computes aggregate stats from a slice of worker metrics.
func ComputeFleetSummary(workers []*WorkerMetrics) FleetSummary {
	s := FleetSummary{
		TotalWorkers: len(workers),
	}
	var cacheHitSum float64
	var cacheCount int
	var maxTTFT float64
	for _, w := range workers {
		if w.Online {
			s.OnlineWorkers++
			s.TotalTokPerSec += w.PromptTokPerSec + w.GenTokPerSec
			if w.CacheHitRatePct > 0 {
				cacheHitSum += w.CacheHitRatePct
				cacheCount++
			}
			if w.TTFT_P99 > maxTTFT {
				maxTTFT = w.TTFT_P99
			}
		}
	}
	if cacheCount > 0 {
		s.AvgCacheHit = cacheHitSum / float64(cacheCount)
	}
	s.P99TTFT = maxTTFT
	return s
}
