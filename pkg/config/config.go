// Package config handles loading and parsing llmtop configuration files.
package config

import (
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

// TopologyEntry defines a role in a disaggregated prefill/decode pipeline.
type TopologyEntry struct {
	Role      string   `yaml:"role"`      // "prefill" or "decode"
	Endpoints []string `yaml:"endpoints"` // worker endpoint URLs
	Feeds     []string `yaml:"feeds"`     // endpoints this role feeds into (prefill → decode)
}

// Config represents the top-level llmtop configuration.
type Config struct {
	Interval     int              `yaml:"interval"`      // refresh interval in seconds (default 2)
	DCGMEndpoint string           `yaml:"dcgm_endpoint"` // optional DCGM exporter URL
	Kubernetes   KubernetesConfig `yaml:"kubernetes"`
	Endpoints    []EndpointConfig `yaml:"endpoints"`
	Topology     []TopologyEntry  `yaml:"topology"`      // optional disaggregated pipeline topology
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		Interval: 2,
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
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
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
