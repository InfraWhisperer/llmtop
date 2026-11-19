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
	// Windowed prefix-cache hit rate over the last sample window, derived
	// from counter deltas (Δcached / Δprompt). 0 = no data; fall back to
	// CacheHitRatePct. CacheHitRatePct is the lifetime-cumulative ratio when
	// the backend exposes counters (matching vLLM hits_total/queries_total),
	// so the header stays stable while this field feeds the detail row.
	CacheHitWindowPct float64 `json:"cache_hit_window_pct"` // 0-100

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
	// PromptTokPerSec is the total prompt-side rate. For backends that report a
	// split (PrefillSplit, currently only SGLang's realtime counters) it equals
	// PrefillComputeTokPerSec + PrefillCacheTokPerSec; for the rest the split
	// fields are 0 and the whole prompt rate was actually computed.
	PromptTokPerSec float64 `json:"prompt_tok_per_sec"`
	GenTokPerSec    float64 `json:"gen_tok_per_sec"`
	// Tokens/s taken from the prefix/radix cache rather than recomputed. Not a
	// compute throughput: reporting it mixed into the prompt/gen totals produced
	// 25k+ tok/s spikes against a real ~150 tok/s generation rate (issue #15).
	PrefillCacheTokPerSec float64 `json:"prefill_cache_tok_per_sec"`
	// Tokens/s of prompt prefill that was actually computed. Meaningful only when
	// PrefillSplit is true; read PrefillComputeDisplayTokPerSec instead of this
	// field when rendering a rate for any backend.
	PrefillComputeTokPerSec float64 `json:"prefill_compute_tok_per_sec"`
	// PrefillSplit reports whether this poll's backend exposed its prompt side
	// split into computed and cache-reused halves. This is a capability, not a
	// value: it stays true while both halves read 0 (idle worker, decode-only
	// window), so a zero rate renders as a real 0 rather than as no data. Inferred
	// from the split values instead, it flipped with traffic and printed a hard
	// "cache 0.0" for backends that never report a split — the exact symptom
	// issue #15 was filed against.
	PrefillSplit bool `json:"prefill_split"`

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

// HasPrefillSplit reports whether this worker's backend reported its prompt side
// split into computed and cache-reused halves. It mirrors the PrefillSplit
// capability set by the collector, so it is stable across idle windows and false
// for every backend that has no split (vLLM, NIM, TGI, Triton, TRT-LLM, LiteLLM,
// llama.cpp, Ollama), where every prompt token in the window was computed.
func (w *WorkerMetrics) HasPrefillSplit() bool {
	return w.PrefillSplit
}

// PrefillComputeDisplayTokPerSec is the prefill rate every surface must render:
// the compute half when the backend reports a split, otherwise the whole prompt
// side. Reading PrefillComputeTokPerSec directly reports vLLM (and every other
// unsplit backend) as "prefill 0" while it computes at 800 tok/s — the same
// complaint issue #15 fixed for SGLang, on the P/D sizing card and the worker
// detail pane.
func (w *WorkerMetrics) PrefillComputeDisplayTokPerSec() float64 {
	if w.HasPrefillSplit() {
		return w.PrefillComputeTokPerSec
	}
	return w.PromptTokPerSec
}

// DisplayTokPerSec is the token rate every surface must show as a worker's
// throughput: prefill that was actually computed, plus generation. Summing
// PromptTokPerSec + GenTokPerSec instead lets prefix-cache reuse inflate a
// worker to 25k+ tok/s while it generates ~150 tok/s (issue #15). Use this in
// place of that expression in tables, overlays, aggregations and sorts alike.
func (w *WorkerMetrics) DisplayTokPerSec() float64 {
	return w.PrefillComputeDisplayTokPerSec() + w.GenTokPerSec
}

// CacheHitDisplayPct is the hit rate to show for a single worker: the windowed
// rate the collector derived from counter deltas when the backend exposes them,
// otherwise the backend-reported cumulative ratio. Windowed first because a
// lifetime 89% reuse hides the collapse of the last few polls (issue #15,
// hit-rate axis); fleet-level averages keep using the cumulative field.
func (w *WorkerMetrics) CacheHitDisplayPct() float64 {
	if w.CacheHitWindowPct > 0 {
		return w.CacheHitWindowPct
	}
	return w.CacheHitRatePct
}

// ReportsCacheHit says whether this worker's backend exposes a cache-hit signal
// at all, over either window. A zero from a backend that reports the metric is a
// real 0% reuse rate; a zero from one that never reported it is missing data,
// and the two must not render the same way — a 0% pill tells the operator the
// cache is useless when in fact nothing was measured.
func (w *WorkerMetrics) ReportsCacheHit() bool {
	return w.CacheHitRatePct > 0 || w.CacheHitWindowPct > 0
}

// FleetSummary aggregates metrics across all workers.
type FleetSummary struct {
	TotalWorkers   int     `json:"total_workers"`
	OnlineWorkers  int     `json:"online_workers"`
	TotalReqPerSec float64 `json:"total_req_per_sec"`
	AvgCacheHit    float64 `json:"avg_cache_hit"`
	// CacheHitWorkers is how many online workers reported a cache-hit signal at
	// all (windowed or lifetime). A fleet where nothing reports it must render
	// "—", not a 0% pill: "0%" claims the cache never hits, which is a different
	// statement from "this backend has no cache-hit metric".
	CacheHitWorkers int     `json:"cache_hit_workers"`
	AvgKVPercGPU    float64 `json:"avg_kv_perc_gpu"` // cluster-wide average GPU KV cache usage (0-100)
	P99TTFT         float64 `json:"p99_ttft"`
	TotalTokPerSec  float64 `json:"total_tok_per_sec"`
	// Segment rates over online workers, so a surface can show the two sides of
	// the pipeline separately instead of one blended number.
	//
	// Invariant: TotalTokPerSec == GenTokPerSec + PrefillComputeTokPerSec.
	//
	// PrefillCacheTokPerSec sits outside that sum on purpose: those tokens were
	// reused from the prefix/radix cache rather than computed, which is what
	// inflated displayed throughput in issue #15. It is accounting, not earned
	// throughput, so surfaces render it as its own labeled segment and never
	// add it back onto the total.
	GenTokPerSec            float64 `json:"gen_tok_per_sec"`
	PrefillComputeTokPerSec float64 `json:"prefill_compute_tok_per_sec"`
	PrefillCacheTokPerSec   float64 `json:"prefill_cache_tok_per_sec"`
	// SplitWorkers counts online workers whose backend reports the prefill
	// compute/cache split (currently only SGLang's realtime counters). A cache
	// segment may be rendered only when this is > 0 — for every other backend
	// "cache 0 tok/s" describes a metric that was never sent.
	SplitWorkers int `json:"split_workers"`
	PrefillCount int `json:"prefill_count"`
	DecodeCount  int `json:"decode_count"`
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
			s.TotalTokPerSec += w.DisplayTokPerSec()
			s.GenTokPerSec += w.GenTokPerSec
			s.PrefillComputeTokPerSec += w.PrefillComputeDisplayTokPerSec()
			if w.HasPrefillSplit() {
				s.SplitWorkers++
				s.PrefillCacheTokPerSec += w.PrefillCacheTokPerSec
			}
			if hit := w.CacheHitDisplayPct(); hit > 0 {
				cacheHitSum += hit
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
	s.CacheHitWorkers = cacheCount
	if kvGPUCount > 0 {
		s.AvgKVPercGPU = kvGPUSum / float64(kvGPUCount)
	}
	s.P99TTFT = maxTTFT
	return s
}
