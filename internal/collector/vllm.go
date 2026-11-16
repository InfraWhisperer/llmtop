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

	// Prefix cache hit rate is derived from the cumulative counters in the
	// throughput block below, where the same counter window feeds the windowed
	// detail-row value.

	// TTFT and ITL: legacy scalar fields plus the new LatencySnapshot view.
	populateLatency(m, pm,
		"vllm:time_to_first_token_seconds",
		[]string{"vllm:time_per_output_token_seconds", "vllm:inter_token_latency_seconds"},
	)

	// Token throughput and prefix-cache hit rate, both read as per-series
	// counter snapshots (issue #15).
	//
	// vllm:{prompt,generation}_tokens_total and the prefix-cache counters fan out
	// per model_name (and per engine in V1), so deltas are taken series by series
	// with each clamped at 0 — a restarted engine must not cancel its siblings'
	// growth and flatten the whole window (see seriesDelta). dt comes from the
	// previous counter snapshot's timestamp, not prev.LastSeen, which lags one
	// poll behind prevCounters and halved every rate.
	//
	// The hit rate prefers the cumulative hits/queries counters over the
	// instantaneous vllm:gpu_prefix_cache_hit_rate gauge: that gauge is only set on
	// iterations carrying a new prefill batch, so it reads 0 at idle and during
	// pure decode even while the prefix cache serves most prompt tokens — the same
	// instantaneous-source trap as issue #15 cause 5, previously hidden under the
	// header's KV% label. The gauge stays as the fallback for builds that export
	// neither counter.
	now := time.Now()
	hits, hitsTotal, okHits := pm.GaugeSeries("vllm:prefix_cache_hits_total", nil)
	queries, queriesTotal, okQueries := pm.GaugeSeries("vllm:prefix_cache_queries_total", nil)
	prompt, promptTotal, okPrompt := pm.GaugeSeries("vllm:prompt_tokens_total", nil)
	gen, genTotal, okGen := pm.GaugeSeries("vllm:generation_tokens_total", nil)
	preempt, preemptTotal, okPreempt := pm.GaugeSeries("vllm:num_preemptions_total", nil)

	if okHits && okQueries && queriesTotal > 0 {
		m.CacheHitRatePct = clampPct(hitsTotal / queriesTotal * 100)
	} else if v, _, ok := pm.GetGaugeAny("vllm:gpu_prefix_cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	}

	dt, windowOK := prevCounters.rateWindow(now)
	haveWindow := windowOK && prev != nil && prev.Online
	if haveWindow {
		if okPrompt {
			m.PromptTokPerSec = seriesRate(prevCounters.promptSeries, prompt, prevCounters.promptTokensTotal, promptTotal, dt)
		}
		if okGen {
			m.GenTokPerSec = seriesRate(prevCounters.genSeries, gen, prevCounters.genTokensTotal, genTotal, dt)
		}
		// Preemptions (evictions) rate from the same counter delta window.
		if okPreempt {
			m.EvictPerSec = seriesRate(prevCounters.preemptSeries, preempt, prevCounters.preemptionsTotal, preemptTotal, dt)
		}
		// Windowed hit rate for the detail row: Δhits/Δqueries over this poll,
		// left unset (rather than 0 %) when no query landed in the window.
		if okHits && okQueries {
			dQueries := seriesDelta(prevCounters.prefixQueriesSeries, queries, 0, queriesTotal)
			dHits := seriesDelta(prevCounters.prefixHitsSeries, hits, 0, hitsTotal)
			if dQueries > 0 && dHits <= dQueries {
				m.CacheHitWindowPct = clampPct(dHits / dQueries * 100)
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

	// Store per-series counters, their totals (for single-series readers), and
	// the sample timestamp for the next poll's rate window.
	var counters counterState
	if okPrompt {
		counters.promptSeries, counters.promptTokensTotal = prompt, promptTotal
	}
	if okGen {
		counters.genSeries, counters.genTokensTotal = gen, genTotal
	}
	if okPreempt {
		counters.preemptSeries, counters.preemptionsTotal = preempt, preemptTotal
	}
	if okHits {
		counters.prefixHitsSeries = hits
	}
	if okQueries {
		counters.prefixQueriesSeries = queries
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
