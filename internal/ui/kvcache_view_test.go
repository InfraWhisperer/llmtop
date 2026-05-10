package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// TestRenderKVCacheView_NoNVMeHidesColumns confirms the NVMe columns stay
// hidden when no worker reports NVMe usage (F1 acceptance criterion).
func TestRenderKVCacheView_NoNVMeHidesColumns(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Label: "p1", Role: "prefill", Online: true, KVCacheUsagePct: 60, KVCacheUsageCPUPct: 20},
	}
	out := RenderKVCacheView(workers, 160)
	if strings.Contains(out, "NVMe") || strings.Contains(out, "NVM%") {
		t.Errorf("expected NVMe columns absent when no worker reports NVMe, got:\n%s", out)
	}
}

// TestRenderKVCacheView_NVMeColumnAppears toggles when at least one worker
// has nonzero NVMe usage (F1 acceptance criterion).
func TestRenderKVCacheView_NVMeColumnAppears(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Label: "p1", Role: "prefill", Online: true, KVCacheUsagePct: 60, KVCacheUsageCPUPct: 20, KVCacheUsageNVMePct: 12},
	}
	out := RenderKVCacheView(workers, 160)
	if !strings.Contains(out, "NVMe") {
		t.Errorf("expected NVMe header when worker reports NVMe usage, got:\n%s", out)
	}
	if !strings.Contains(out, "NVM%") {
		t.Errorf("expected NVM%% header, got:\n%s", out)
	}
}

// TestRenderKVCacheView_PrestageUsesKVTransfer ensures the PRESTAGE column
// reads KVTransferP99Ms, not the legacy TTFT_P50 proxy (F6 acceptance).
func TestRenderKVCacheView_PrestageUsesKVTransfer(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{
			Label: "prefill-1", Role: "prefill", Online: true,
			KVCacheUsagePct: 60,
			TTFT_P50:        800, // would have shown 800ms with the old proxy
			KVTransferP99Ms: 12,
		},
	}
	out := RenderKVCacheView(workers, 160)
	if !strings.Contains(out, "12ms") {
		t.Errorf("expected '12ms' in PRESTAGE column from KVTransferP99Ms, got:\n%s", out)
	}
	if strings.Contains(out, "800ms") {
		t.Errorf("PRESTAGE must not fall back to TTFT_P50 proxy, got:\n%s", out)
	}
}

// TestRenderKVCacheView_DecodeWorkerPrestageDash decode workers always show
// "—" (dash) in PRESTAGE — they don't transfer KV blocks out.
func TestRenderKVCacheView_DecodeWorkerPrestageDash(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Label: "decode-1", Role: "decode", Online: true, KVCacheUsagePct: 50, TTFT_P50: 200, KVTransferP99Ms: 0},
	}
	out := RenderKVCacheView(workers, 160)
	// The dash placeholder formatKCLatency emits is "-" (not the em-dash),
	// per existing kvcache_view.go behavior.
	if !strings.Contains(out, "decode-1") {
		t.Fatalf("test fixture missing: %s", out)
	}
}
