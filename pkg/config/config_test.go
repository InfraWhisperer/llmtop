package config

import (
	"strings"
	"testing"
)

func TestParse_AlertsAbsent_AppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`interval: 3
endpoints:
  - url: http://localhost:8000
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := alertDefaults()
	if cfg.Alerts.KVCriticalPct != def.KVCriticalPct ||
		cfg.Alerts.KVPressurePct != def.KVPressurePct ||
		cfg.Alerts.ITLSpikeMs != def.ITLSpikeMs ||
		cfg.Alerts.PrefillQueueDepth != def.PrefillQueueDepth ||
		cfg.Alerts.KVCriticalDelayS != def.KVCriticalDelayS {
		t.Errorf("expected defaults applied when alerts absent; got %+v", cfg.Alerts)
	}
	if cfg.Alerts.CustomSet {
		t.Errorf("CustomSet should be false when alerts: section absent")
	}
}

func TestParse_AlertsPartial_FillsZeroFieldsWithDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`alerts:
  kv_critical_pct: 85
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Alerts.KVCriticalPct != 85 {
		t.Errorf("expected KVCriticalPct=85, got %f", cfg.Alerts.KVCriticalPct)
	}
	if cfg.Alerts.KVPressurePct != 75 {
		t.Errorf("expected default KVPressurePct=75, got %f", cfg.Alerts.KVPressurePct)
	}
	if cfg.Alerts.ITLSpikeMs != 2000 {
		t.Errorf("expected default ITLSpikeMs=2000, got %f", cfg.Alerts.ITLSpikeMs)
	}
	if !cfg.Alerts.CustomSet {
		t.Errorf("CustomSet should be true after partial override")
	}
}

func TestParse_KVCriticalNotGreaterThanPressure_Errors(t *testing.T) {
	_, err := Parse([]byte(`alerts:
  kv_critical_pct: 60
  kv_pressure_pct: 70
`))
	if err == nil {
		t.Fatal("expected error for kv_critical_pct <= kv_pressure_pct")
	}
	if !strings.Contains(err.Error(), "kv_critical_pct") {
		t.Errorf("error message should mention kv_critical_pct: %v", err)
	}
}

func TestParse_NegativeDelay_Errors(t *testing.T) {
	_, err := Parse([]byte(`alerts:
  kv_critical_delay_s: -1
`))
	if err == nil {
		t.Fatal("expected error for negative delay")
	}
}

func TestParse_FullCustomBlock(t *testing.T) {
	cfg, err := Parse([]byte(`alerts:
  kv_critical_pct: 95
  kv_pressure_pct: 70
  itl_spike_ms: 1500
  prefill_queue_depth: 3
  cache_hit_drop_pp: 5
  ttft_event_ms: 4000
  kv_event_pct: 60
  kv_critical_delay_s: 2
  kv_pressure_delay_s: 20
  itl_spike_delay_s: 5
  prefill_queue_delay_s: 7
  scrape_failure_delay_s: 8
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := cfg.Alerts
	if a.KVCriticalPct != 95 || a.KVPressurePct != 70 || a.ITLSpikeMs != 1500 ||
		a.PrefillQueueDepth != 3 || a.CacheHitDropPP != 5 || a.TTFTEventMs != 4000 ||
		a.KVEventPct != 60 || a.KVCriticalDelayS != 2 || a.ITLSpikeDelayS != 5 ||
		a.ScrapeFailureDelayS != 8 {
		t.Errorf("full custom block did not deserialize: %+v", a)
	}
	if !a.CustomSet {
		t.Errorf("CustomSet should be true")
	}
}
