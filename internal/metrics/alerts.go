package metrics

import (
	"fmt"
	"sync"
	"time"
)

// AlertThresholds holds user-configurable thresholds and sustained-condition
// durations for alert rules and the UI event ring. The zero value is not
// safe to use; call DefaultAlertThresholds() for production defaults.
//
// Invariants enforced by callers (config parser):
//
//	KVCriticalPct > KVPressurePct
//	All *Delay fields >= 0
type AlertThresholds struct {
	KVCriticalPct      float64       // default 90; must be > KVPressurePct
	KVPressurePct      float64       // default 75
	ITLSpikeMs         float64       // default 2000
	PrefillQueueDepth  int           // default 5
	CacheHitDropPP     float64       // default 10; percentage points
	TTFTEventMs        float64       // default 5000; used by detectEvents
	KVEventPct         float64       // default 75; used by detectEvents
	KVCriticalDelay    time.Duration // default 5s
	KVPressureDelay    time.Duration // default 30s
	ITLSpikeDelay      time.Duration // default 10s
	PrefillQueueDelay  time.Duration // default 10s
	ScrapeFailureDelay time.Duration // default 15s
}

// DefaultAlertThresholds returns the production defaults for alert thresholds.
// These values match the previous hardcoded constants in evalRule and
// detectEvents.
func DefaultAlertThresholds() AlertThresholds {
	return AlertThresholds{
		KVCriticalPct:      90,
		KVPressurePct:      75,
		ITLSpikeMs:         2000,
		PrefillQueueDepth:  5,
		CacheHitDropPP:     10,
		TTFTEventMs:        5000,
		KVEventPct:         75,
		KVCriticalDelay:    5 * time.Second,
		KVPressureDelay:    30 * time.Second,
		ITLSpikeDelay:      10 * time.Second,
		PrefillQueueDelay:  10 * time.Second,
		ScrapeFailureDelay: 15 * time.Second,
	}
}

// AlertSeverity represents the severity level of an alert.
type AlertSeverity int

const (
	AlertInfo AlertSeverity = iota
	AlertWarning
	AlertCritical
)

// String returns the display label for the severity.
func (s AlertSeverity) String() string {
	switch s {
	case AlertCritical:
		return "CRIT"
	case AlertWarning:
		return "WARN"
	default:
		return "INFO"
	}
}

// Alert represents a fired alert with optional resolution.
//
// Source carries the human-readable origin label (worker label, cluster
// scope, or "<host>/gpu<n>"). SourceEndpoint carries the worker endpoint
// URL when the alert is tied to a single worker — this is what V10's
// timeline view matches against WorkerMetrics.Endpoint to find the affected
// worker in historical frames. Cluster-scoped alerts and GPU-scoped alerts
// leave SourceEndpoint empty.
type Alert struct {
	Severity       AlertSeverity
	Title          string
	Detail         string
	Source         string // worker label, "cluster", or "<host>/gpu<n>"
	SourceEndpoint string // worker endpoint URL; empty for cluster/GPU-scoped alerts
	FiredAt        time.Time
	ResolvedAt     *time.Time // nil if still active
}

// IsActive returns true if the alert has not been resolved.
func (a *Alert) IsActive() bool {
	return a.ResolvedAt == nil
}

// AlertRule defines a named evaluation function that fires/resolves alerts.
type AlertRule struct {
	Name string
	Eval func(state *AlertState) *Alert // returns non-nil to fire, nil to resolve
}

// AlertState holds the snapshot data the rules evaluate against.
type AlertState struct {
	Workers      []*WorkerMetrics
	GPUs         []*GPUInfo
	Summary      FleetSummary
	PrevCacheHit float64 // cluster cache hit from previous eval, on the basis CacheHitAlertPct chooses
	// PrevCacheHitMeasured distinguishes a previous eval that measured 0% from one
	// that measured nothing. Without it a drop from a real 0% cannot be told apart
	// from “no baseline yet”, and the rule would either never fire or fire on
	// missing data.
	PrevCacheHitMeasured bool
	PrevCacheHitAt       time.Time // timestamp of previous cache hit reading
	// CacheHitWindowAgeSec is how long the poll window behind the windowed hit
	// rate is, so the alert can say what interval its comparison covers. It comes
	// from the caller's refresh interval, not from the summary, because the
	// summary aggregates windows measured over that interval.
	CacheHitWindowAgeSec int
}

// AlertManager evaluates rules against model state and manages alert lifecycle.
type AlertManager struct {
	mu          sync.Mutex
	rules       []AlertRule
	active      map[string]*Alert // keyed by rule name + source
	resolved    []*Alert          // last N resolved alerts
	maxResolved int

	// Duration thresholds: how long a condition must persist before firing
	pending map[string]time.Time // keyed by rule+source, tracks when condition first seen

	thresholds AlertThresholds
}

// NewAlertManager creates an AlertManager with the default production rules
// and DefaultAlertThresholds.
func NewAlertManager() *AlertManager {
	return NewAlertManagerWithThresholds(DefaultAlertThresholds())
}

// NewAlertManagerWithThresholds creates an AlertManager using caller-supplied
// thresholds. All rule evaluations and pending delays read from t.
func NewAlertManagerWithThresholds(t AlertThresholds) *AlertManager {
	am := &AlertManager{
		active:      make(map[string]*Alert),
		maxResolved: 20,
		pending:     make(map[string]time.Time),
		thresholds:  t,
	}
	am.rules = defaultRules()
	return am
}

// Thresholds returns a copy of the active thresholds.
func (am *AlertManager) Thresholds() AlertThresholds {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.thresholds
}

// Evaluate runs all rules against the current state and updates alerts.
func (am *AlertManager) Evaluate(state *AlertState) {
	am.mu.Lock()
	defer am.mu.Unlock()

	seen := make(map[string]bool)

	for _, rule := range am.rules {
		alerts := evalRule(rule, state, am.thresholds)
		for _, a := range alerts {
			key := rule.Name + ":" + a.Source
			seen[key] = true

			if _, exists := am.active[key]; !exists {
				// Check pending duration for rules that need sustained conditions
				dur := ruleDelay(rule.Name, am.thresholds)
				if dur > 0 {
					first, ok := am.pending[key]
					if !ok {
						am.pending[key] = time.Now()
						continue
					}
					if time.Since(first) < dur {
						continue
					}
				}
				am.active[key] = a
				delete(am.pending, key)
			}
		}
	}

	// Resolve alerts whose conditions cleared
	for key, a := range am.active {
		if !seen[key] {
			now := time.Now()
			a.ResolvedAt = &now
			am.resolved = append(am.resolved, a)
			if len(am.resolved) > am.maxResolved {
				am.resolved = am.resolved[len(am.resolved)-am.maxResolved:]
			}
			delete(am.active, key)
			delete(am.pending, key)
		}
	}

	// Clear pending entries that are no longer firing
	for key := range am.pending {
		if !seen[key] {
			delete(am.pending, key)
		}
	}
}

// All returns all alerts: active first (sorted by severity desc), then resolved.
func (am *AlertManager) All() []*Alert {
	am.mu.Lock()
	defer am.mu.Unlock()

	var result []*Alert
	// Active alerts by severity: critical, warning, info
	for sev := AlertCritical; sev >= AlertInfo; sev-- {
		for _, a := range am.active {
			if a.Severity == sev {
				cp := *a
				result = append(result, &cp)
			}
		}
	}
	// Resolved alerts (newest first)
	for i := len(am.resolved) - 1; i >= 0; i-- {
		cp := *am.resolved[i]
		result = append(result, &cp)
	}
	return result
}

// Counts returns (info, warning, critical) counts of active alerts, ordered
// by AlertSeverity iota.
func (am *AlertManager) Counts() (int, int, int) {
	am.mu.Lock()
	defer am.mu.Unlock()
	var info, warn, crit int
	for _, a := range am.active {
		switch a.Severity {
		case AlertCritical:
			crit++
		case AlertWarning:
			warn++
		case AlertInfo:
			info++
		}
	}
	return info, warn, crit
}

// ruleDelay returns how long a condition must persist before the rule fires.
func ruleDelay(name string, t AlertThresholds) time.Duration {
	switch name {
	case "itl_spike":
		return t.ITLSpikeDelay
	case "scrape_failure":
		return t.ScrapeFailureDelay
	case "kv_critical":
		return t.KVCriticalDelay
	case "prefill_queue":
		return t.PrefillQueueDelay
	case "kv_pressure":
		return t.KVPressureDelay
	default:
		return 0
	}
}

// evalRule evaluates a single rule and returns all alerts it would fire.
func evalRule(rule AlertRule, state *AlertState, t AlertThresholds) []*Alert {
	// Rules that produce per-worker alerts
	var alerts []*Alert
	switch rule.Name {
	case "itl_spike":
		for _, w := range state.Workers {
			if w.Online && w.ITL_P99 > t.ITLSpikeMs {
				alerts = append(alerts, &Alert{
					Severity:       AlertCritical,
					Title:          fmt.Sprintf("ITL p99 spike on %s: %.0fms (threshold %.0fms)", w.Label, w.ITL_P99, t.ITLSpikeMs),
					Detail:         fmt.Sprintf("Worker KV%% %.0f%% · consider KV eviction cascade", w.KVCacheUsagePct),
					Source:         w.Label,
					SourceEndpoint: w.Endpoint,
					FiredAt:        time.Now(),
				})
			}
		}
	case "scrape_failure":
		for _, w := range state.Workers {
			if !w.Online {
				alerts = append(alerts, &Alert{
					Severity:       AlertCritical,
					Title:          fmt.Sprintf("Scrape failure: %s — all metrics Err", w.Label),
					Detail:         "vLLM /metrics unreachable · pod Running but port closed",
					Source:         w.Label,
					SourceEndpoint: w.Endpoint,
					FiredAt:        time.Now(),
				})
			}
		}
	case "kv_critical":
		for _, w := range state.Workers {
			if w.Online && w.KVCacheUsagePct > t.KVCriticalPct {
				alerts = append(alerts, &Alert{
					Severity:       AlertCritical,
					Title:          fmt.Sprintf("KV cache critical on %s: %.0f%%", w.Label, w.KVCacheUsagePct),
					Detail:         fmt.Sprintf("GPU KV cache >%.0f%% · likely eviction cascade", t.KVCriticalPct),
					Source:         w.Label,
					SourceEndpoint: w.Endpoint,
					FiredAt:        time.Now(),
				})
			}
		}
	case "ecc_error":
		for _, g := range state.GPUs {
			if g.ECCErrors > 0 {
				alerts = append(alerts, &Alert{
					Severity: AlertCritical,
					Title:    fmt.Sprintf("ECC errors on %s GPU:%d — %d double-bit errors", g.Hostname, g.Index, g.ECCErrors),
					Detail:   fmt.Sprintf("Pod: %s · consider draining node", g.Pod),
					Source:   fmt.Sprintf("%s/gpu%d", g.Hostname, g.Index),
					FiredAt:  time.Now(),
				})
			}
		}
	case "prefill_queue":
		for _, w := range state.Workers {
			if w.Online && w.Role == "prefill" && w.RequestsWaiting > t.PrefillQueueDepth {
				alerts = append(alerts, &Alert{
					Severity:       AlertWarning,
					Title:          fmt.Sprintf("Prefill queue depth elevated on %s: queue=%d (threshold %d)", w.Label, w.RequestsWaiting, t.PrefillQueueDepth),
					Detail:         fmt.Sprintf("KV%% %.0f%% · TTFT p99 %.0fms · consider scale-out or rebalance", w.KVCacheUsagePct, w.TTFT_P99),
					Source:         w.Label,
					SourceEndpoint: w.Endpoint,
					FiredAt:        time.Now(),
				})
			}
		}
	case "cache_hit_drop":
		// Change detection needs the windowed rate. AvgCacheHit is a
		// lifetime-cumulative ratio on counter-backed backends — hits_total /
		// queries_total over the engine's whole life — so it moves by hundredths of a
		// point per poll and a 10-point drop threshold could not be met outside a
		// restart. The window is what actually carries a routing change, so the rule
		// reads the mean over workers whose denominator moved this poll; workers
		// reporting a measured 0% count toward it, workers that reported nothing do
		// not.
		cur, curOK := state.Summary.CacheHitAlertPct()
		prev, prevOK := state.PrevCacheHit, state.PrevCacheHitMeasured
		if prevOK && curOK && time.Since(state.PrevCacheHitAt) <= 60*time.Second {
			drop := prev - cur
			if drop > t.CacheHitDropPP {
				basis := "lifetime"
				if state.Summary.CacheHitWindowWorkers > 0 {
					basis = fmt.Sprintf("%ds window", state.CacheHitWindowAgeSec)
				}
				alerts = append(alerts, &Alert{
					Severity: AlertWarning,
					Title:    fmt.Sprintf("Cluster cache hit rate fell %.0fpp: %.0f%% → %.0f%%", drop, prev, cur),
					Detail: fmt.Sprintf("Compared on %s over %d reporting worker(s) · prefix affinity routing may be degraded",
						basis, state.Summary.CacheHitWorkers),
					Source:  "cluster",
					FiredAt: time.Now(),
				})
			}
		}
	case "dcgm_stale":
		for _, g := range state.GPUs {
			if !g.LastScrape.IsZero() && time.Since(g.LastScrape) > 15*time.Second {
				alerts = append(alerts, &Alert{
					Severity: AlertWarning,
					Title:    fmt.Sprintf("DCGM scrape stale on %s GPU:%d: last update %.0fs ago", g.Hostname, g.Index, time.Since(g.LastScrape).Seconds()),
					Detail:   "dcgm-exporter pod healthy · possible metric pipeline lag",
					Source:   fmt.Sprintf("%s/gpu%d", g.Hostname, g.Index),
					FiredAt:  time.Now(),
				})
			}
		}
	case "kv_pressure":
		for _, w := range state.Workers {
			if w.Online && w.KVCacheUsagePct >= t.KVPressurePct && w.KVCacheUsagePct < t.KVCriticalPct {
				alerts = append(alerts, &Alert{
					Severity:       AlertWarning,
					Title:          fmt.Sprintf("KV cache pressure on %s: %.0f%%", w.Label, w.KVCacheUsagePct),
					Detail:         fmt.Sprintf("GPU KV cache %.0f-%.0f%% · approaching eviction threshold", t.KVPressurePct, t.KVCriticalPct-1),
					Source:         w.Label,
					SourceEndpoint: w.Endpoint,
					FiredAt:        time.Now(),
				})
			}
		}
	case "pd_ratio":
		if state.Summary.DecodeCount > 0 && state.Summary.PrefillCount > 0 {
			var prefillTok, decodeTok float64
			for _, w := range state.Workers {
				if !w.Online {
					continue
				}
				// Each side is charged for the work that side owes: a prefill
				// worker for the prompt tokens it must compute, a decode worker
				// for the tokens it generates. PromptTokPerSec counts prompt
				// tokens served from the prefix cache as if they were computed,
				// which inflated the prefill side of this ratio and made the rule
				// recommend decode workers that are not needed (issue #15).
				switch w.Role {
				case "prefill":
					prefillTok += w.PrefillComputeDisplayTokPerSec()
				case "decode":
					decodeTok += w.GenTokPerSec
				}
			}
			if decodeTok > 0 {
				idealDecode := int(prefillTok/decodeTok) + 1
				if idealDecode-state.Summary.DecodeCount >= 2 {
					alerts = append(alerts, &Alert{
						Severity: AlertWarning,
						Title:    fmt.Sprintf("P/D ratio suboptimal: %d prefill : %d decode, estimated ideal %d:%d", state.Summary.PrefillCount, state.Summary.DecodeCount, state.Summary.PrefillCount, idealDecode),
						Detail:   fmt.Sprintf("Decode slack low · %d additional decode workers recommended", idealDecode-state.Summary.DecodeCount),
						Source:   "cluster",
						FiredAt:  time.Now(),
					})
				}
			}
		}
	case "worker_joined":
		// Handled externally via event detection, not periodic eval
	case "role_detected":
		// Handled externally via event detection, not periodic eval
	}
	return alerts
}

func defaultRules() []AlertRule {
	names := []string{
		"itl_spike", "scrape_failure", "kv_critical", "ecc_error",
		"prefill_queue", "cache_hit_drop", "dcgm_stale", "kv_pressure", "pd_ratio",
	}
	rules := make([]AlertRule, len(names))
	for i, n := range names {
		rules[i] = AlertRule{Name: n}
	}
	return rules
}
