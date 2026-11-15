// Package metrics defines the core data structures for LLM inference worker metrics.
package metrics

import "time"

// Backend represents the type of LLM inference backend.
type Backend string

const (
	BackendVLLM     Backend = "vLLM"
	BackendSGLang   Backend = "SGLang"
	BackendLMCache  Backend = "LMCache"
	BackendNIM      Backend = "NIM"
	BackendTGI      Backend = "TGI"
	BackendTRTLLM   Backend = "TRT-LLM"
	BackendTriton   Backend = "Triton"
	BackendLlamaCpp Backend = "llama.cpp"
	BackendLiteLLM  Backend = "LiteLLM"
	BackendOllama   Backend = "Ollama"
	BackendUnknown  Backend = "Unknown"
)

// LatencySnapshot holds multi-quantile latency data for one metric.
// All values are in milliseconds. Zero means absent/not-reported.
//
// Invariants:
//
//	P50 <= P95 <= P99 (all non-negative; 0 means absent)
//	CDFPoints[i] <= CDFPoints[i+1] for all i (monotonically non-decreasing)
//	CDFPoints[i] in [0.0, 1.0]
//	All elements are 0 when P99 == 0
type LatencySnapshot struct {
	P50       float64     `json:"p50"`
	P95       float64     `json:"p95"`
	P99       float64     `json:"p99"`
	CDFPoints [10]float64 `json:"cdf_points"`
}

// TenantMetrics holds aggregate metrics for one tenant (request_tag value)
// across all workers serving a given model group.
//
// TTFT_P99Ms is always 0: vLLM does not emit per-request_tag histogram
// buckets, so a real per-tenant P99 is not derivable from current backends.
// The field is kept in the contract for forward compatibility; do not
// synthesize values into it.
type TenantMetrics struct {
	Tag          string  `json:"tag"`
	RequestShare float64 `json:"request_share"`
	TokPerSec    float64 `json:"tok_per_sec"`
	TTFT_P99Ms   float64 `json:"ttft_p99_ms"`
}

// WorkerMetrics holds all collected metrics for a single inference worker endpoint.
type WorkerMetrics struct {
	Endpoint  string    `json:"endpoint"`
	Label     string    `json:"label"`
	Backend   Backend   `json:"backend"`
	ModelName string    `json:"model_name"`
	Online    bool      `json:"online"`
	LastSeen  time.Time `json:"last_seen"`
	Role      string    `json:"role"`      // "prefill", "decode", or "mono"
	NodeName  string    `json:"node_name"` // K8s node running this worker (empty for local discovery)

	// Load
	RequestsRunning int `json:"requests_running"`
	RequestsWaiting int `json:"requests_waiting"`

	// KV Cache
	KVCacheUsagePct     float64 `json:"kv_cache_usage_pct"`      // 0-100 (GPU)
	KVCacheUsageCPUPct  float64 `json:"kv_cache_usage_cpu_pct"`  // 0-100 (CPU, may be absent)
	KVCacheUsageNVMePct float64 `json:"kv_cache_usage_nvme_pct"` // 0-100 (NVMe, may be absent)
	CacheHitRatePct     float64 `json:"cache_hit_rate_pct"`      // 0-100

	// Latency (milliseconds) — legacy scalar fields kept for backward compatibility.
	TTFT_P50 float64 `json:"ttft_p50"`
	TTFT_P99 float64 `json:"ttft_p99"`
	ITL_P50  float64 `json:"itl_p50"`
	ITL_P99  float64 `json:"itl_p99"`

	// Multi-percentile latency snapshots with CDF data for distribution shape.
	TTFT LatencySnapshot `json:"ttft"`
	ITL  LatencySnapshot `json:"itl"`

	// KV block transfer P99 latency in ms; 0 means absent.
	// Only populated for prefill workers reporting vllm:kv_cache_offload_time_seconds.
	KVTransferP99Ms float64 `json:"kv_transfer_p99_ms"`

	// Throughput
	// PromptTokPerSec is the total prompt-side rate. For backends that split
	// it (SGLang realtime_tokens_total), it equals PrefillComputeTokPerSec +
	// PrefillCacheTokPerSec; for the rest the split fields are 0 and the whole
	// prompt rate was actually computed.
	PromptTokPerSec float64 `json:"prompt_tok_per_sec"`
	GenTokPerSec    float64 `json:"gen_tok_per_sec"`
	// Tokens/s taken from the prefix/radix cache rather than recomputed. Not a
	// compute throughput: reporting it mixed into gen/prompt totals produced
	// 25k+ tok/s spikes against a real ~150 tok/s generation rate (issue #15).
	PrefillCacheTokPerSec float64 `json:"prefill_cache_tok_per_sec"`
	// Tokens/s of prompt prefill that was actually computed.
	PrefillComputeTokPerSec float64 `json:"prefill_compute_tok_per_sec"`

	// KV eviction rate (preemptions per second, computed from counter)
	EvictPerSec float64 `json:"evict_per_sec"`

	// LMCache specific
	StoreSizeBytes float64 `json:"store_size_bytes"`
	EvictionTotal  float64 `json:"eviction_total"`

	// Per-tenant raw request_success_total counter values, keyed by request_tag.
	// Zero-length / nil map means no tenant labels were observed.
	TenantReqs map[string]float64 `json:"tenant_reqs,omitempty"`

	// History for sparklines (last N samples)
	TTFTHistory   []float64 `json:"-"`
	GenTokHistory []float64 `json:"-"`
}

// FleetSummary aggregates metrics across all workers.
type FleetSummary struct {
	TotalWorkers   int     `json:"total_workers"`
	OnlineWorkers  int     `json:"online_workers"`
	TotalReqPerSec float64 `json:"total_req_per_sec"`
	AvgCacheHit    float64 `json:"avg_cache_hit"`
	AvgKVPercGPU   float64 `json:"avg_kv_perc_gpu"` // cluster-wide average GPU KV cache usage (0-100)
	P99TTFT        float64 `json:"p99_ttft"`
	TotalTokPerSec float64 `json:"total_tok_per_sec"`
	// Per-segment fleet rates, summed over online workers. TotalTokPerSec stays
	// the prompt+gen sum (sparkline / sort / --once); the header renders the
	// three segments separately so a prefill-cache burst is never read as
	// generation throughput.
	GenTokPerSec            float64 `json:"gen_tok_per_sec"`
	PrefillComputeTokPerSec float64 `json:"prefill_compute_tok_per_sec"`
	PrefillCacheTokPerSec   float64 `json:"prefill_cache_tok_per_sec"`
	PrefillCount            int     `json:"prefill_count"`
	DecodeCount             int     `json:"decode_count"`
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
			s.GenTokPerSec += w.GenTokPerSec
			// Backends without a cache split report the whole prompt side as
			// compute, so the header's prefill segment stays meaningful.
			if w.PrefillCacheTokPerSec > 0 || w.PrefillComputeTokPerSec > 0 {
				s.PrefillComputeTokPerSec += w.PrefillComputeTokPerSec
				s.PrefillCacheTokPerSec += w.PrefillCacheTokPerSec
			} else {
				s.PrefillComputeTokPerSec += w.PromptTokPerSec
			}
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
