package collector

import (
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type nimParser struct{}

func init() {
	RegisterParser(metrics.BackendNIM, &nimParser{})
	detectors = append(detectors, &nimParser{}) // MUST be last — conjunction check, not prefix
}

func (p *nimParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	hasRunning := false
	hasCachePerc := false
	hasTTFT := false
	var model string
	for _, s := range pm.Samples {
		switch s.Name {
		case "num_requests_running":
			hasRunning = true
			if m, ok := s.Labels["model_name"]; ok && m != "" {
				model = m
			}
		case "gpu_cache_usage_perc":
			hasCachePerc = true
		}
	}
	for _, h := range pm.Histograms {
		if h.Name == "time_to_first_token_seconds" {
			hasTTFT = true
			if m, ok := h.Labels["model_name"]; ok && m != "" {
				model = m
			}
		}
	}
	if hasRunning && hasCachePerc && hasTTFT {
		return metrics.BackendNIM, model
	}
	return metrics.BackendUnknown, ""
}

func (p *nimParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseNIMMetrics(m, prev, prevCounters, pm)
}

// parseNIMMetrics extracts NIM-specific metrics from parsed Prometheus data.
// NIM exports the same vLLM metrics but without the "vllm:" prefix.
// Counter metrics (prompt_tokens_total, generation_tokens_total) are stored in
// the Prometheus parser's Samples slice despite being typed as counters — the
// parser does not distinguish gauge vs counter storage. This matches vllm.go behavior.
func parseNIMMetrics(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	if v, _, ok := pm.GetGaugeAny("num_requests_running"); ok {
		m.RequestsRunning = int(v)
	}

	if v, _, ok := pm.GetGaugeAny("num_requests_waiting"); ok {
		m.RequestsWaiting = int(v)
	}

	// GPU KV cache usage (0.0-1.0 -> 0-100%)
	if v, _, ok := pm.GetGaugeAny("gpu_cache_usage_perc"); ok {
		m.KVCacheUsagePct = v * 100
	}

	// Prefix cache hit rate (0.0-1.0 -> 0-100%)
	if v, _, ok := pm.GetGaugeAny("gpu_prefix_cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	}

	// TTFT/ITL: populate scalar fields and LatencySnapshots in one pass.
	populateLatency(m, pm,
		"time_to_first_token_seconds",
		[]string{"time_per_output_token_seconds"},
	)

	// Token throughput: counter-delta rate computation.
	if prev != nil && prev.Online {
		dt := time.Since(prev.LastSeen).Seconds()
		if dt > 0 {
			if v, _, ok := pm.GetGaugeAny("prompt_tokens_total"); ok {
				m.PromptTokPerSec = (v - prevCounters.promptTokensTotal) / dt
				if m.PromptTokPerSec < 0 {
					m.PromptTokPerSec = 0
				}
			}
			if v, _, ok := pm.GetGaugeAny("generation_tokens_total"); ok {
				m.GenTokPerSec = (v - prevCounters.genTokensTotal) / dt
				if m.GenTokPerSec < 0 {
					m.GenTokPerSec = 0
				}
			}
		}
	}

	// Store raw counters for next delta.
	var counters counterState
	if v, _, ok := pm.GetGaugeAny("prompt_tokens_total"); ok {
		counters.promptTokensTotal = v
	}
	if v, _, ok := pm.GetGaugeAny("generation_tokens_total"); ok {
		counters.genTokensTotal = v
	}

	return counters
}
