// Package config handles loading and parsing llmtop configuration files.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// EndpointConfig represents a single endpoint configuration.
type EndpointConfig struct {
	URL         string `yaml:"url"`
	Backend     string `yaml:"backend"`      // optional: vllm, sglang, lmcache, nim
	Label       string `yaml:"label"`         // optional display label
	MetricsPath string `yaml:"metrics_path"`  // optional: defaults to /metrics, /v1/metrics for nim
}

// KubernetesConfig holds Kubernetes discovery settings.
type KubernetesConfig struct {
	Enabled        *bool  `yaml:"enabled"`        // nil = auto-detect
	Namespace      string `yaml:"namespace"`       // "" = all namespaces
	LabelSelector  string `yaml:"labelSelector"`
	MaxConcurrent  int    `yaml:"maxConcurrent"`   // default 10
	RequestTimeout string `yaml:"requestTimeout"`  // default "2s"
}

// AlertThresholdsConfig is the YAML deserialization shape for alert thresholds.
// Zero-value fields are replaced with production defaults during Parse().
// Mirrors metrics.AlertThresholds but lives in the config package.
type AlertThresholdsConfig struct {
	KVCriticalPct       float64 `yaml:"kv_critical_pct"`
	KVPressurePct       float64 `yaml:"kv_pressure_pct"`
	ITLSpikeMs          float64 `yaml:"itl_spike_ms"`
	PrefillQueueDepth   int     `yaml:"prefill_queue_depth"`
	CacheHitDropPP      float64 `yaml:"cache_hit_drop_pp"`
	TTFTEventMs         float64 `yaml:"ttft_event_ms"`
	KVEventPct          float64 `yaml:"kv_event_pct"`
	KVCriticalDelayS    int     `yaml:"kv_critical_delay_s"`
	KVPressureDelayS    int     `yaml:"kv_pressure_delay_s"`
	ITLSpikeDelayS      int     `yaml:"itl_spike_delay_s"`
	PrefillQueueDelayS  int     `yaml:"prefill_queue_delay_s"`
	ScrapeFailureDelayS int     `yaml:"scrape_failure_delay_s"`

	// CustomSet is true when at least one threshold field was explicitly set
	// in the source YAML. The TUI uses this to decide whether to render the
	// "alerts: custom" header indicator. Not serialized to YAML.
	CustomSet bool `yaml:"-"`
}

// Config represents the top-level llmtop configuration.
type Config struct {
	Interval     int                   `yaml:"interval"`      // refresh interval in seconds (default 2)
	DCGMEndpoint string                `yaml:"dcgm_endpoint"` // optional DCGM exporter URL
	Kubernetes   KubernetesConfig      `yaml:"kubernetes"`
	Endpoints    []EndpointConfig      `yaml:"endpoints"`
	Alerts       AlertThresholdsConfig `yaml:"alerts"`
}

// alertDefaults returns the canonical default thresholds.
// Kept in sync with metrics.DefaultAlertThresholds() — see spec §5.
func alertDefaults() AlertThresholdsConfig {
	return AlertThresholdsConfig{
		KVCriticalPct:       90,
		KVPressurePct:       75,
		ITLSpikeMs:          2000,
		PrefillQueueDepth:   5,
		CacheHitDropPP:      10,
		TTFTEventMs:         5000,
		KVEventPct:          75,
		KVCriticalDelayS:    5,
		KVPressureDelayS:    30,
		ITLSpikeDelayS:      10,
		PrefillQueueDelayS:  10,
		ScrapeFailureDelayS: 15,
	}
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		Interval: 2,
		Alerts:   alertDefaults(),
	}
}

// LoadFile loads a Config from a YAML file path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse parses a Config from raw YAML bytes.
func Parse(data []byte) (*Config, error) {
	cfg := DefaultConfig()
	// Pre-zero the alerts block so we can detect which fields the user explicitly set
	// (yaml.v3 leaves zero-valued fields untouched by default; we want to fill them
	// from defaults but still know whether the user touched the section at all).
	var raw struct {
		Interval     int                   `yaml:"interval"`
		DCGMEndpoint string                `yaml:"dcgm_endpoint"`
		Kubernetes   KubernetesConfig      `yaml:"kubernetes"`
		Endpoints    []EndpointConfig      `yaml:"endpoints"`
		Alerts       AlertThresholdsConfig `yaml:"alerts"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg.Interval = raw.Interval
	cfg.DCGMEndpoint = raw.DCGMEndpoint
	cfg.Kubernetes = raw.Kubernetes
	cfg.Endpoints = raw.Endpoints

	// Merge alerts: any zero field falls back to the production default.
	user := raw.Alerts
	defaults := alertDefaults()
	merged := defaults
	customSet := false
	if user.KVCriticalPct != 0 {
		merged.KVCriticalPct = user.KVCriticalPct
		customSet = true
	}
	if user.KVPressurePct != 0 {
		merged.KVPressurePct = user.KVPressurePct
		customSet = true
	}
	if user.ITLSpikeMs != 0 {
		merged.ITLSpikeMs = user.ITLSpikeMs
		customSet = true
	}
	if user.PrefillQueueDepth != 0 {
		merged.PrefillQueueDepth = user.PrefillQueueDepth
		customSet = true
	}
	if user.CacheHitDropPP != 0 {
		merged.CacheHitDropPP = user.CacheHitDropPP
		customSet = true
	}
	if user.TTFTEventMs != 0 {
		merged.TTFTEventMs = user.TTFTEventMs
		customSet = true
	}
	if user.KVEventPct != 0 {
		merged.KVEventPct = user.KVEventPct
		customSet = true
	}
	if user.KVCriticalDelayS != 0 {
		merged.KVCriticalDelayS = user.KVCriticalDelayS
		customSet = true
	}
	if user.KVPressureDelayS != 0 {
		merged.KVPressureDelayS = user.KVPressureDelayS
		customSet = true
	}
	if user.ITLSpikeDelayS != 0 {
		merged.ITLSpikeDelayS = user.ITLSpikeDelayS
		customSet = true
	}
	if user.PrefillQueueDelayS != 0 {
		merged.PrefillQueueDelayS = user.PrefillQueueDelayS
		customSet = true
	}
	if user.ScrapeFailureDelayS != 0 {
		merged.ScrapeFailureDelayS = user.ScrapeFailureDelayS
		customSet = true
	}
	merged.CustomSet = customSet
	cfg.Alerts = merged

	// Validate invariants.
	if cfg.Alerts.KVCriticalPct <= cfg.Alerts.KVPressurePct {
		return nil, fmt.Errorf("kv_critical_pct (%g) must be greater than kv_pressure_pct (%g)",
			cfg.Alerts.KVCriticalPct, cfg.Alerts.KVPressurePct)
	}
	if cfg.Alerts.KVCriticalDelayS < 0 || cfg.Alerts.KVPressureDelayS < 0 ||
		cfg.Alerts.ITLSpikeDelayS < 0 || cfg.Alerts.PrefillQueueDelayS < 0 ||
		cfg.Alerts.ScrapeFailureDelayS < 0 {
		return nil, fmt.Errorf("alert delay fields must be >= 0")
	}

	if cfg.Interval <= 0 {
		cfg.Interval = 2
	}
	if cfg.Kubernetes.MaxConcurrent <= 0 {
		cfg.Kubernetes.MaxConcurrent = 10
	}
	// Normalize backend strings to lowercase; apply NIM metrics path default
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].Backend = strings.ToLower(cfg.Endpoints[i].Backend)
		if cfg.Endpoints[i].Backend == "nim" && cfg.Endpoints[i].MetricsPath == "" {
			cfg.Endpoints[i].MetricsPath = "/v1/metrics"
		}
	}
	return cfg, nil
}
