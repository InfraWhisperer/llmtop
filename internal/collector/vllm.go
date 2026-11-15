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
	// Newer vLLM exports counters (prefix_cache_hits_total / prefix_cache_queries_total) instead of a gauge.
	// Sum all series: like the token counters these can fan out per model_name (issue #15).
	if v, _, ok := pm.GetGaugeAny("vllm:gpu_prefix_cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	} else {
		hits, hOk := pm.GetGaugeSum("vllm:prefix_cache_hits_total", nil)
		queries, qOk := pm.GetGaugeSum("vllm:prefix_cache_queries_total", nil)
		if hOk && qOk && queries > 0 {
			m.CacheHitRatePct = (hits / queries) * 100
		}
	}

	// TTFT and ITL: legacy scalar fields plus the new LatencySnapshot view.
	populateLatency(m, pm,
		"vllm:time_to_first_token_seconds",
		[]string{"vllm:time_per_output_token_seconds", "vllm:inter_token_latency_seconds"},
	)

	// Token throughput: counter-delta rates (issue #15).
	// vllm:{prompt,generation}_tokens_total can fan out per model_name/engine,
	// so deltas are taken over summed series, and dt comes from the previous
	// counter snapshot's timestamp rather than prev.LastSeen (which lags one
	// poll behind prevCounters, halving every rate).
	now := time.Now()
	prompt, okPrompt := pm.GetGaugeSum("vllm:prompt_tokens_total", nil)
	gen, okGen := pm.GetGaugeSum("vllm:generation_tokens_total", nil)
	preempt, okPreempt := pm.GetGaugeSum("vllm:num_preemptions_total", nil)
	if prev != nil && prev.Online {
		if dt, ok := prevCounters.rateWindow(now); ok {
			if okPrompt {
				m.PromptTokPerSec = max(0.0, (prompt-prevCounters.promptTokensTotal)/dt)
			}
			if okGen {
				m.GenTokPerSec = max(0.0, (gen-prevCounters.genTokensTotal)/dt)
			}
			// Preemptions (evictions) rate from the same counter delta window.
			if okPreempt {
				m.EvictPerSec = max(0.0, (preempt-prevCounters.preemptionsTotal)/dt)
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

	// Store summed raw counters (with sample timestamp) for next rate calculation
	var counters counterState
	if okPrompt {
		counters.promptTokensTotal = prompt
	}
	if okGen {
		counters.genTokensTotal = gen
	}
	if okPreempt {
		counters.preemptionsTotal = preempt
	}
	counters.ts = now
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
