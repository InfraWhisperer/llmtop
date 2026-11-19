package metrics

import "sort"

// ModelGroup aggregates worker stats for a single model.
type ModelGroup struct {
	ModelName      string  `json:"model_name"`
	Backend        Backend `json:"backend"`
	Workers        int     `json:"workers"`
	OnlineWorkers  int     `json:"online_workers"`
	TotalTokPerSec float64 `json:"total_tok_per_sec"`
	// PrefillTokPerSec / GenTokPerSec split TotalTokPerSec across the two sides
	// that actually compute, so the model view can tell a prefill-bound fleet
	// from a decode-bound one. Prefill counts the computed half only — reusing
	// prompt tokens from prefix cache is not throughput the fleet earned, and
	// folding it in is what produced the 25k tok/s spikes of issue #15. For a
	// backend with no split the whole prompt side was computed, so
	// PrefillTokPerSec + GenTokPerSec == TotalTokPerSec always holds.
	PrefillTokPerSec float64         `json:"prefill_tok_per_sec"`
	GenTokPerSec     float64         `json:"gen_tok_per_sec"`
	AvgKVCachePct    float64         `json:"avg_kv_cache_pct"`
	TotalQueue       int             `json:"total_queue"`
	TotalRunning     int             `json:"total_running"`
	AvgTTFTP99       float64         `json:"avg_ttft_p99"`
	AvgHitRate       float64         `json:"avg_hit_rate"`
	TopTenants       []TenantMetrics `json:"top_tenants,omitempty"`
}

// maxTopTenants is the cap on TenantMetrics entries kept per ModelGroup.
const maxTopTenants = 5

// GroupWorkersByModel aggregates worker metrics by model name.
func GroupWorkersByModel(workers []*WorkerMetrics) []ModelGroup {
	groups := make(map[string]*modelAccum)

	for _, w := range workers {
		name := w.ModelName
		if name == "" {
			name = "Unknown"
		}

		g, ok := groups[name]
		if !ok {
			g = &modelAccum{
				name:        name,
				backend:     w.Backend,
				tenantTotal: make(map[string]float64),
				tenantTok:   make(map[string]float64),
			}
			groups[name] = g
		}
		g.total++
		if w.Online {
			g.online++
			prefillTok := w.PrefillComputeDisplayTokPerSec()
			workerTok := prefillTok + w.GenTokPerSec
			g.tokPerSec += workerTok
			g.prefillTok += prefillTok
			g.genTok += w.GenTokPerSec
			g.kvSum += w.KVCacheUsagePct
			g.queue += w.RequestsWaiting
			g.running += w.RequestsRunning
			g.ttftSum += w.TTFT_P99
			g.hitSum += w.CacheHitRatePct
			g.onlineCount++

			// Per-tenant request shares for this model group: total raw counter
			// across all online workers; tok/s estimate is proportional to each
			// worker's share contribution.
			var workerTagSum float64
			for _, c := range w.TenantReqs {
				workerTagSum += c
			}
			for tag, c := range w.TenantReqs {
				g.tenantTotal[tag] += c
				if workerTagSum > 0 {
					g.tenantTok[tag] += workerTok * (c / workerTagSum)
				}
			}
		}
	}

	result := make([]ModelGroup, 0, len(groups))
	for _, g := range groups {
		mg := ModelGroup{
			ModelName:        g.name,
			Backend:          g.backend,
			Workers:          g.total,
			OnlineWorkers:    g.online,
			TotalTokPerSec:   g.tokPerSec,
			PrefillTokPerSec: g.prefillTok,
			GenTokPerSec:     g.genTok,
			TotalQueue:       g.queue,
			TotalRunning:     g.running,
		}
		if g.onlineCount > 0 {
			mg.AvgKVCachePct = g.kvSum / float64(g.onlineCount)
			mg.AvgTTFTP99 = g.ttftSum / float64(g.onlineCount)
			mg.AvgHitRate = g.hitSum / float64(g.onlineCount)
		}
		mg.TopTenants = topTenants(g.tenantTotal, g.tenantTok)
		result = append(result, mg)
	}

	// Sort by model name for stable output.
	sort.Slice(result, func(i, j int) bool {
		return result[i].ModelName < result[j].ModelName
	})

	return result
}

// topTenants ranks tag totals by share desc and returns up to maxTopTenants.
// totals is the raw counter per tag; tok is the proportional tok/s estimate per tag.
// RequestShare is normalized over the sum of all tag totals (not over total
// requests including untagged) — sum across TopTenants is <= 1.0.
func topTenants(totals, tok map[string]float64) []TenantMetrics {
	if len(totals) == 0 {
		return nil
	}
	var grand float64
	for _, v := range totals {
		grand += v
	}
	if grand <= 0 {
		return nil
	}
	all := make([]TenantMetrics, 0, len(totals))
	for tag, v := range totals {
		all = append(all, TenantMetrics{
			Tag:          tag,
			RequestShare: v / grand,
			TokPerSec:    tok[tag],
		})
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].RequestShare > all[j].RequestShare
	})
	if len(all) > maxTopTenants {
		all = all[:maxTopTenants]
	}
	return all
}

type modelAccum struct {
	name        string
	backend     Backend
	total       int
	online      int
	onlineCount int
	tokPerSec   float64
	prefillTok  float64
	genTok      float64
	kvSum       float64
	queue       int
	running     int
	ttftSum     float64
	hitSum      float64
	tenantTotal map[string]float64
	tenantTok   map[string]float64
}
