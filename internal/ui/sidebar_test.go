package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestRenderSidebarNilWorker(t *testing.T) {
	out := RenderSidebar(nil, nil, nil, 30, nil)
	if out == "" {
		t.Error("expected non-empty sidebar even with nil worker")
	}
}

func TestRenderSidebarGPUSection(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:           "prefill-a1b2",
		NodeName:        "node-gpu-04",
		KVCacheUsagePct: 61,
		CacheHitRatePct: 31,
	}
	gpus := []*metrics.GPUInfo{
		{
			Index:      0,
			Name:       "H100 NVL",
			Hostname:   "node-gpu-04",
			UtilPct:    72,
			MemBWUtil:  58,
			TempC:      74,
			PowerW:     312,
			NVLinkGBs:  88,
			LastScrape: time.Now(),
		},
	}

	out := RenderSidebar(worker, gpus, nil, 30, nil)

	if !strings.Contains(out, "GPU") {
		t.Error("expected GPU section header")
	}
	if !strings.Contains(out, "node-gpu-04") {
		t.Error("expected node name in GPU section")
	}
}

func TestRenderSidebarKVTiers(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:              "prefill-a1b2",
		KVCacheUsagePct:    61,
		KVCacheUsageCPUPct: 34,
		CacheHitRatePct:    31,
		EvictPerSec:        12,
		TTFT_P50:           4.2,
	}

	out := RenderSidebar(worker, nil, nil, 30, nil)

	if !strings.Contains(out, "KV TIERS") {
		t.Error("expected KV TIERS section header")
	}
	if !strings.Contains(out, "GPU HBM") {
		t.Error("expected GPU HBM tier")
	}
	if !strings.Contains(out, "CPU DRAM") {
		t.Error("expected CPU DRAM tier when KVCacheUsageCPUPct > 0")
	}
	if !strings.Contains(out, "Hit rate") {
		t.Error("expected hit rate metric")
	}
}

func TestRenderSidebarEvents(t *testing.T) {
	worker := &metrics.WorkerMetrics{Label: "w1"}
	ring := metrics.NewEventRing(5)
	ring.Push(metrics.SeverityError, "scrape fail w1")
	ring.Push(metrics.SeverityOK, "w2 joined pool")

	events := ring.All()
	out := RenderSidebar(worker, nil, events, 30, nil)

	if !strings.Contains(out, "RECENT EVENTS") {
		t.Error("expected RECENT EVENTS section header")
	}
}

func TestRenderSidebarNoGPUData(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:    "w1",
		NodeName: "node-1",
	}

	out := RenderSidebar(worker, nil, nil, 30, nil)

	if !strings.Contains(out, "no DCGM data") {
		t.Error("expected 'no DCGM data' when no GPUs match node")
	}
}

func TestRenderSidebarECCErrors(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:    "w1",
		NodeName: "node-1",
	}
	gpus := []*metrics.GPUInfo{
		{
			Index:      0,
			Hostname:   "node-1",
			ECCErrors:  5,
			LastScrape: time.Now(),
		},
	}

	out := RenderSidebar(worker, gpus, nil, 30, nil)

	if !strings.Contains(out, "ECC") {
		t.Error("expected ECC error display when ECCErrors > 0")
	}
}

// TestRenderSidebar_NVMeTierVisibleWhenNonzero exercises F1: the NVMe row
// appears only when the worker reports KVCacheUsageNVMePct > 0.
func TestRenderSidebar_NVMeTierVisibleWhenNonzero(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:               "prefill-1",
		KVCacheUsagePct:     60,
		KVCacheUsageCPUPct:  20,
		KVCacheUsageNVMePct: 12,
	}
	out := RenderSidebar(worker, nil, nil, 30, nil)
	if !strings.Contains(out, "NVMe SSD") {
		t.Errorf("expected NVMe SSD tier row when nonzero, got:\n%s", out)
	}
}

// TestRenderSidebar_NVMeTierHiddenWhenZero ensures we never render the row
// for a worker that doesn't report NVMe.
func TestRenderSidebar_NVMeTierHiddenWhenZero(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:           "decode-1",
		KVCacheUsagePct: 60,
	}
	out := RenderSidebar(worker, nil, nil, 30, nil)
	if strings.Contains(out, "NVMe SSD") {
		t.Errorf("expected no NVMe row when zero, got:\n%s", out)
	}
}

// TestRenderSidebar_KVTransferP99Replaces TTFTProxy verifies F6: the KV
// xfer p99 row reads KVTransferP99Ms, not TTFT_P50.
func TestRenderSidebar_KVTransferP99ReplacesTTFTProxy(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:           "prefill-1",
		KVCacheUsagePct: 60,
		TTFT_P50:        800, // would have shown 800ms under old proxy
		KVTransferP99Ms: 12,
	}
	out := RenderSidebar(worker, nil, nil, 30, nil)
	if !strings.Contains(out, "KV xfer p99") {
		t.Errorf("expected 'KV xfer p99' label, got:\n%s", out)
	}
	if !strings.Contains(out, "12ms") {
		t.Errorf("expected '12ms' from KVTransferP99Ms, got:\n%s", out)
	}
	if strings.Contains(out, "Prestage lat") {
		t.Errorf("legacy 'Prestage lat' label must be removed, got:\n%s", out)
	}
}

// TestRenderSidebar_KVTransferP99DashWhenZero a worker with no histogram
// shows "—" rather than 0ms or "Err".
func TestRenderSidebar_KVTransferP99DashWhenZero(t *testing.T) {
	worker := &metrics.WorkerMetrics{
		Label:           "decode-1",
		KVCacheUsagePct: 50,
		KVTransferP99Ms: 0,
	}
	out := RenderSidebar(worker, nil, nil, 30, nil)
	if !strings.Contains(out, "KV xfer p99") {
		t.Errorf("expected label even when zero, got:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash placeholder for zero, got:\n%s", out)
	}
}

func TestSidebarCollapsesBelowMinWidth(t *testing.T) {
	// The sidebar should not render at widths < MinWidthForSidebar.
	// This is controlled in renderMain, not RenderSidebar itself,
	// but verify the constant is reasonable.
	if MinWidthForSidebar < 80 {
		t.Errorf("MinWidthForSidebar = %d, expected >= 80", MinWidthForSidebar)
	}
}
