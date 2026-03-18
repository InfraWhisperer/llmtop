package collector

import (
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type llamacppParser struct{}

func init() {
	RegisterParser(metrics.BackendLlamaCpp, &llamacppParser{})
	detectors = append(detectors, &llamacppParser{})
}

func (p *llamacppParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 9 && s.Name[:9] == "llamacpp:" {
			return metrics.BackendLlamaCpp, ""
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *llamacppParser) Parse(m *metrics.WorkerMetrics, _ *metrics.WorkerMetrics, _ counterState, pm *metrics.ParsedMetrics) counterState {
	return parseLlamaCppMetrics(m, pm)
}

// parseLlamaCppMetrics extracts llama.cpp / llama-server metrics.
//
// llama-server uses the llamacpp: prefix (colons, not underscores — violates
// Prometheus naming conventions but that's what llama.cpp ships).
// Requires --metrics flag to enable the /metrics endpoint.
//
// Key advantage over other backends: llama-server exposes computed throughput
// gauges directly (tokens/sec), so no rate computation from counters is needed.
// No model_name label — metrics are global aggregates across all slots.
func parseLlamaCppMetrics(m *metrics.WorkerMetrics, pm *metrics.ParsedMetrics) counterState {
	// KV cache utilization (0.0-1.0 gauge → 0-100%)
	if v, _, ok := pm.GetGaugeAny("llamacpp:kv_cache_usage_ratio"); ok {
		m.KVCacheUsagePct = v * 100
	}

	// Running requests (active slots)
	if v, _, ok := pm.GetGaugeAny("llamacpp:requests_processing"); ok {
		m.RequestsRunning = int(v)
	}

	// Waiting/deferred requests
	if v, _, ok := pm.GetGaugeAny("llamacpp:requests_deferred"); ok {
		m.RequestsWaiting = int(v)
	}

	// Throughput: llama-server provides computed gauges directly — no rate
	// calculation needed. These are rolling averages over recent activity.
	if v, _, ok := pm.GetGaugeAny("llamacpp:prompt_tokens_seconds"); ok {
		m.PromptTokPerSec = v
	}
	if v, _, ok := pm.GetGaugeAny("llamacpp:predicted_tokens_seconds"); ok {
		m.GenTokPerSec = v
	}

	return counterState{}
}
