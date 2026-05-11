package collector

import (
	"maps"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type vllmParser struct{}

func init() {
	RegisterParser(metrics.BackendVLLM, &vllmParser{})
	detectors = append(detectors, &vllmParser{})
}

func (p *vllmParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 5 && s.Name[:5] == "vllm:" {
			return metrics.BackendVLLM, s.Labels["model_name"]
		}
	}
	for _, h := range pm.Histograms {
		if len(h.Name) >= 5 && h.Name[:5] == "vllm:" {
			return metrics.BackendVLLM, h.Labels["model_name"]
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *vllmParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseVLLMMetrics(m, prev, prevCounters, pm)
}

// parseVLLMMetrics extracts vLLM-specific metrics from the parsed Prometheus data.
func parseVLLMMetrics(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	// Running requests
	if v, _, ok := pm.GetGaugeAny("vllm:num_requests_running"); ok {
		m.RequestsRunning = int(v)
	}

	// Waiting requests (queue depth)
	if v, _, ok := pm.GetGaugeAny("vllm:num_requests_waiting"); ok {
		m.RequestsWaiting = int(v)
	}

	// GPU KV cache usage (0.0-1.0 → convert to 0-100%)
	// Newer vLLM versions (and Dynamo) use vllm:kv_cache_usage_perc instead of vllm:gpu_cache_usage_perc
	if v, _, ok := pm.GetGaugeAny("vllm:gpu_cache_usage_perc"); ok {
		m.KVCacheUsagePct = v * 100
	} else if v, _, ok := pm.GetGaugeAny("vllm:kv_cache_usage_perc"); ok {
		m.KVCacheUsagePct = v * 100
	}

	// CPU KV cache usage (0.0-1.0 → convert to 0-100%, may be absent)
	if v, _, ok := pm.GetGaugeAny("vllm:cpu_cache_usage_perc"); ok {
		m.KVCacheUsageCPUPct = v * 100
	}

	// NVMe KV cache usage (0.0-1.0 → 0-100%, absent in current vLLM but reserved for LMCache-aware deployments)
	if v, _, ok := pm.GetGaugeAny("vllm:nvme_cache_usage_perc"); ok {
		m.KVCacheUsageNVMePct = v * 100
	}

	// Prefix cache hit rate (0.0-1.0 → convert to 0-100%)
	// Newer vLLM exports counters (prefix_cache_hits_total / prefix_cache_queries_total) instead of a gauge
	if v, _, ok := pm.GetGaugeAny("vllm:gpu_prefix_cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	} else {
		hits, _, hOk := pm.GetGaugeAny("vllm:prefix_cache_hits_total")
		queries, _, qOk := pm.GetGaugeAny("vllm:prefix_cache_queries_total")
		if hOk && qOk && queries > 0 {
			m.CacheHitRatePct = (hits / queries) * 100
		}
	}

	// TTFT and ITL: legacy scalar fields plus the new LatencySnapshot view.
	populateLatency(m, pm,
		"vllm:time_to_first_token_seconds",
		[]string{"vllm:time_per_output_token_seconds", "vllm:inter_token_latency_seconds"},
	)

	// Token throughput: compute rate from counters
	// We use prev snapshot to compute delta/time
	if prev != nil && prev.Online {
		dt := time.Since(prev.LastSeen).Seconds()
		if dt > 0 {
			if v, _, ok := pm.GetGaugeAny("vllm:prompt_tokens_total"); ok {
				m.PromptTokPerSec = (v - prevCounters.promptTokensTotal) / dt
				if m.PromptTokPerSec < 0 {
					m.PromptTokPerSec = 0
				}
			}
			if v, _, ok := pm.GetGaugeAny("vllm:generation_tokens_total"); ok {
				m.GenTokPerSec = (v - prevCounters.genTokensTotal) / dt
				if m.GenTokPerSec < 0 {
					m.GenTokPerSec = 0
				}
			}
		}
	}

	// Preemptions (evictions) rate: compute from counter delta
	if v, _, ok := pm.GetGaugeAny("vllm:num_preemptions_total"); ok {
		if prev != nil && prev.Online {
			dt := time.Since(prev.LastSeen).Seconds()
			if dt > 0 {
				rate := (v - prevCounters.preemptionsTotal) / dt
				if rate < 0 {
					rate = 0
				}
				m.EvictPerSec = rate
			}
		}
	}

	// Per-tenant request counter snapshot. Always populate the raw counters
	// from this poll; the rate is implied by the next poll's delta against
	// counters.tenantReqTotals. The map stays at zero on first poll.
	tagCounters := pm.GetAllGaugesByLabel("vllm:request_success_total", "request_tag")
	if len(tagCounters) > 0 {
		m.TenantReqs = make(map[string]float64, len(tagCounters))
		maps.Copy(m.TenantReqs, tagCounters)
	}

	// KV transfer P99 (prefill workers only). vLLM disagg / Dynamo emits
	// vllm:kv_cache_offload_time_seconds; decode/mono workers never receive
	// offloaded blocks, so keep their value at 0 even if a synthetic histogram
	// happens to be present.
	if m.Role == "prefill" {
		if p99, ok := pm.GetHistogramQuantileAny("vllm:kv_cache_offload_time_seconds", 0.99); ok {
			m.KVTransferP99Ms = p99 * 1000
		}
	}

	// Store raw counters for next rate calculation
	var counters counterState
	if v, _, ok := pm.GetGaugeAny("vllm:prompt_tokens_total"); ok {
		counters.promptTokensTotal = v
	}
	if v, _, ok := pm.GetGaugeAny("vllm:generation_tokens_total"); ok {
		counters.genTokensTotal = v
	}
	if v, _, ok := pm.GetGaugeAny("vllm:num_preemptions_total"); ok {
		counters.preemptionsTotal = v
	}
	if len(tagCounters) > 0 {
		counters.tenantReqTotals = tagCounters
	}

	// Dynamo runtime augmentation: Dynamo pods emit dynamo_component_* metrics
	// alongside vllm:* metrics. Use them as fallbacks when the vllm: gauge is
	// missing (e.g., future Dynamo versions that drop the vllm: prefix).
	if m.KVCacheUsagePct == 0 {
		if v, _, ok := pm.GetGaugeAny("dynamo_component_gpu_cache_usage_percent"); ok {
			m.KVCacheUsagePct = v * 100
		}
	}
	if m.RequestsRunning == 0 {
		if v, labels, ok := pm.GetGaugeAny("dynamo_component_inflight_requests"); ok {
			// Only count the "generate" endpoint — other endpoints (kv_indexer,
			// clear_kv_blocks) are internal Dynamo RPCs, not user requests.
			if labels["dynamo_endpoint"] == "generate" {
				m.RequestsRunning = int(v)
			}
		}
	}

	return counters
}
