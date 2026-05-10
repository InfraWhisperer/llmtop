package collector

import (
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type sglangParser struct{}

func init() {
	RegisterParser(metrics.BackendSGLang, &sglangParser{})
	detectors = append(detectors, &sglangParser{})
}

func (p *sglangParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 7 && s.Name[:7] == "sglang:" {
			return metrics.BackendSGLang, s.Labels["model_name"]
		}
	}
	for _, h := range pm.Histograms {
		if len(h.Name) >= 7 && h.Name[:7] == "sglang:" {
			return metrics.BackendSGLang, h.Labels["model_name"]
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *sglangParser) Parse(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	return parseSGLangMetrics(m, prev, prevCounters, pm)
}

// parseSGLangMetrics extracts SGLang-specific metrics from the parsed Prometheus data.
func parseSGLangMetrics(m *metrics.WorkerMetrics, prev *metrics.WorkerMetrics, prevCounters counterState, pm *metrics.ParsedMetrics) counterState {
	// Running requests
	if v, _, ok := pm.GetGaugeAny("sglang:num_running_reqs"); ok {
		m.RequestsRunning = int(v)
	}

	// Waiting requests (queue depth)
	if v, _, ok := pm.GetGaugeAny("sglang:num_waiting_reqs"); ok {
		m.RequestsWaiting = int(v)
	}

	// Token usage (KV cache utilization, 0.0-1.0 → 0-100%)
	if v, _, ok := pm.GetGaugeAny("sglang:token_usage"); ok {
		m.KVCacheUsagePct = v * 100
	}

	// Cache hit rate (0.0-1.0 → 0-100%)
	if v, _, ok := pm.GetGaugeAny("sglang:cache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
	}

	// TTFT/ITL: populate scalar fields and LatencySnapshots in one pass.
	populateLatency(m, pm,
		"sglang:time_to_first_token_seconds",
		[]string{"sglang:time_per_output_token_seconds"},
	)

	// Token throughput rates
	if prev != nil && prev.Online {
		dt := time.Since(prev.LastSeen).Seconds()
		if dt > 0 {
			if v, _, ok := pm.GetGaugeAny("sglang:prompt_tokens_total"); ok {
				m.PromptTokPerSec = (v - prevCounters.promptTokensTotal) / dt
				if m.PromptTokPerSec < 0 {
					m.PromptTokPerSec = 0
				}
			}
			if v, _, ok := pm.GetGaugeAny("sglang:generation_tokens_total"); ok {
				m.GenTokPerSec = (v - prevCounters.genTokensTotal) / dt
				if m.GenTokPerSec < 0 {
					m.GenTokPerSec = 0
				}
			}
		}
	}

	// Store raw counters for next rate calculation
	var counters counterState
	if v, _, ok := pm.GetGaugeAny("sglang:prompt_tokens_total"); ok {
		counters.promptTokensTotal = v
	}
	if v, _, ok := pm.GetGaugeAny("sglang:generation_tokens_total"); ok {
		counters.genTokensTotal = v
	}

	return counters
}
