// SPDX-License-Identifier: Apache-2.0
// Spec-only contract tests for pkg/config (F4: Configurable Alert Thresholds).
// Source of truth: docs/feature-spec.md F4 — YAML round-trip, default-fill,
// and KVCriticalPct > KVPressurePct invariant.

package config_test

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/pkg/config"
)

// --- Defaults applied when section absent -----------------------------------

func TestParse_AlertsAbsent_AppliesProductionDefaults(t *testing.T) {
	yaml := `interval: 2
endpoints:
  - url: http://localhost:8000
`
	c, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := map[string]struct {
		got, want any
	}{
		"KVCriticalPct":       {c.Alerts.KVCriticalPct, float64(90)},
		"KVPressurePct":       {c.Alerts.KVPressurePct, float64(75)},
		"ITLSpikeMs":          {c.Alerts.ITLSpikeMs, float64(2000)},
		"PrefillQueueDepth":   {c.Alerts.PrefillQueueDepth, 5},
		"CacheHitDropPP":      {c.Alerts.CacheHitDropPP, float64(10)},
		"TTFTEventMs":         {c.Alerts.TTFTEventMs, float64(5000)},
		"KVEventPct":          {c.Alerts.KVEventPct, float64(75)},
		"KVCriticalDelayS":    {c.Alerts.KVCriticalDelayS, 5},
		"KVPressureDelayS":    {c.Alerts.KVPressureDelayS, 30},
		"ITLSpikeDelayS":      {c.Alerts.ITLSpikeDelayS, 10},
		"PrefillQueueDelayS":  {c.Alerts.PrefillQueueDelayS, 10},
		"ScrapeFailureDelayS": {c.Alerts.ScrapeFailureDelayS, 15},
	}
	for name, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %v want %v", name, tc.got, tc.want)
		}
	}
}

func TestParse_AlertsAbsent_CustomSetFalse(t *testing.T) {
	c, err := config.Parse([]byte(`interval: 2`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Alerts.CustomSet {
		t.Fatalf("CustomSet must be false when alerts section is absent")
	}
}

// --- Custom thresholds preserved with defaults filled -----------------------

func TestParse_AlertsPartial_KVCriticalPctSetOthersDefault(t *testing.T) {
	yaml := `alerts:
  kv_critical_pct: 85
`
	c, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Alerts.KVCriticalPct != 85 {
		t.Fatalf("KVCriticalPct: got %v want 85", c.Alerts.KVCriticalPct)
	}
	if c.Alerts.KVPressurePct != 75 {
		t.Fatalf("KVPressurePct: got %v want default 75", c.Alerts.KVPressurePct)
	}
	if c.Alerts.ITLSpikeMs != 2000 {
		t.Fatalf("ITLSpikeMs: got %v want default 2000", c.Alerts.ITLSpikeMs)
	}
}

func TestParse_AlertsPresent_CustomSetTrue(t *testing.T) {
	yaml := `alerts:
  kv_critical_pct: 85
`
	c, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !c.Alerts.CustomSet {
		t.Fatalf("CustomSet must be true when alerts section is present")
	}
}

func TestParse_AllAlertFields_RoundTrip(t *testing.T) {
	yaml := `alerts:
  kv_critical_pct: 88
  kv_pressure_pct: 70
  itl_spike_ms: 1500
  prefill_queue_depth: 8
  cache_hit_drop_pp: 12
  ttft_event_ms: 4000
  kv_event_pct: 65
  kv_critical_delay_s: 3
  kv_pressure_delay_s: 20
  itl_spike_delay_s: 7
  prefill_queue_delay_s: 6
  scrape_failure_delay_s: 12
`
	c, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a := c.Alerts
	if a.KVCriticalPct != 88 {
		t.Errorf("KVCriticalPct: got %v want 88", a.KVCriticalPct)
	}
	if a.KVPressurePct != 70 {
		t.Errorf("KVPressurePct: got %v want 70", a.KVPressurePct)
	}
	if a.ITLSpikeMs != 1500 {
		t.Errorf("ITLSpikeMs: got %v want 1500", a.ITLSpikeMs)
	}
	if a.PrefillQueueDepth != 8 {
		t.Errorf("PrefillQueueDepth: got %v want 8", a.PrefillQueueDepth)
	}
	if a.CacheHitDropPP != 12 {
		t.Errorf("CacheHitDropPP: got %v want 12", a.CacheHitDropPP)
	}
	if a.TTFTEventMs != 4000 {
		t.Errorf("TTFTEventMs: got %v want 4000", a.TTFTEventMs)
	}
	if a.KVEventPct != 65 {
		t.Errorf("KVEventPct: got %v want 65", a.KVEventPct)
	}
	if a.KVCriticalDelayS != 3 {
		t.Errorf("KVCriticalDelayS: got %v want 3", a.KVCriticalDelayS)
	}
	if a.KVPressureDelayS != 20 {
		t.Errorf("KVPressureDelayS: got %v want 20", a.KVPressureDelayS)
	}
	if a.ITLSpikeDelayS != 7 {
		t.Errorf("ITLSpikeDelayS: got %v want 7", a.ITLSpikeDelayS)
	}
	if a.PrefillQueueDelayS != 6 {
		t.Errorf("PrefillQueueDelayS: got %v want 6", a.PrefillQueueDelayS)
	}
	if a.ScrapeFailureDelayS != 12 {
		t.Errorf("ScrapeFailureDelayS: got %v want 12", a.ScrapeFailureDelayS)
	}
}

// --- KVCriticalPct > KVPressurePct invariant --------------------------------

func TestParse_KVCriticalLessThanPressure_ReturnsError(t *testing.T) {
	yaml := `alerts:
  kv_critical_pct: 60
  kv_pressure_pct: 70
`
	_, err := config.Parse([]byte(yaml))
	if err == nil {
		t.Fatalf("expected error when kv_critical_pct < kv_pressure_pct")
	}
}

func TestParse_KVCriticalEqualsPressure_ReturnsError(t *testing.T) {
	yaml := `alerts:
  kv_critical_pct: 75
  kv_pressure_pct: 75
`
	_, err := config.Parse([]byte(yaml))
	if err == nil {
		t.Fatalf("expected error when kv_critical_pct == kv_pressure_pct (must be strictly greater)")
	}
}

func TestParse_KVCriticalGreaterThanPressure_NoError(t *testing.T) {
	yaml := `alerts:
  kv_critical_pct: 88
  kv_pressure_pct: 70
`
	if _, err := config.Parse([]byte(yaml)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParse_KVCriticalErrorMessageMentionsBothFields(t *testing.T) {
	yaml := `alerts:
  kv_critical_pct: 60
  kv_pressure_pct: 70
`
	_, err := config.Parse([]byte(yaml))
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "kv_critical_pct") || !strings.Contains(msg, "kv_pressure_pct") {
		t.Fatalf("error must reference both kv_critical_pct and kv_pressure_pct; got: %v", err)
	}
}

// --- Negative-delay rejection (spec invariant: "All delay fields must be ≥ 0") ---

func TestParse_NegativeKVCriticalDelay_ReturnsError(t *testing.T) {
	yaml := `alerts:
  kv_critical_delay_s: -1
`
	_, err := config.Parse([]byte(yaml))
	if err == nil {
		t.Fatalf("expected error for negative delay")
	}
}

func TestParse_NegativeKVPressureDelay_ReturnsError(t *testing.T) {
	yaml := `alerts:
  kv_pressure_delay_s: -5
`
	_, err := config.Parse([]byte(yaml))
	if err == nil {
		t.Fatalf("expected error for negative kv_pressure_delay_s")
	}
}

// --- Defaults via DefaultConfig ----------------------------------------------

func TestDefaultConfig_HasAlertDefaults(t *testing.T) {
	c := config.DefaultConfig()
	if c.Alerts.KVCriticalPct != 90 {
		t.Fatalf("DefaultConfig().Alerts.KVCriticalPct: got %v want 90", c.Alerts.KVCriticalPct)
	}
	if c.Alerts.KVPressurePct != 75 {
		t.Fatalf("DefaultConfig().Alerts.KVPressurePct: got %v want 75", c.Alerts.KVPressurePct)
	}
	if c.Alerts.CustomSet {
		t.Fatalf("DefaultConfig().Alerts.CustomSet must be false")
	}
}
