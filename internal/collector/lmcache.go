package collector

import (
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

type lmcacheParser struct{}

func init() {
	RegisterParser(metrics.BackendLMCache, &lmcacheParser{})
	detectors = append(detectors, &lmcacheParser{})
}

func (p *lmcacheParser) Detect(pm *metrics.ParsedMetrics) (metrics.Backend, string) {
	for _, s := range pm.Samples {
		if len(s.Name) >= 8 && s.Name[:8] == "lmcache_" {
			return metrics.BackendLMCache, ""
		}
	}
	return metrics.BackendUnknown, ""
}

func (p *lmcacheParser) Parse(m *metrics.WorkerMetrics, _ *metrics.WorkerMetrics, _ counterState, pm *metrics.ParsedMetrics) counterState {
	parseLMCacheMetrics(m, pm)
	return counterState{}
}

// parseLMCacheMetrics extracts LMCache-specific metrics from parsed Prometheus data.
func parseLMCacheMetrics(m *metrics.WorkerMetrics, pm *metrics.ParsedMetrics) {
	// Cache hit rate (0.0-1.0 → 0-100%)
	if v, _, ok := pm.GetGaugeAny("lmcache_hit_rate"); ok {
		m.CacheHitRatePct = v * 100
		m.CacheHitReported = true
	}

	// Total cache store size in bytes
	if v, _, ok := pm.GetGaugeAny("lmcache_store_size_bytes"); ok {
		m.StoreSizeBytes = v
	}

	// Total evictions
	if v, _, ok := pm.GetGaugeAny("lmcache_eviction_total"); ok {
		m.EvictionTotal = v
	}
}
