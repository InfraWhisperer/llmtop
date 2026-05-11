// Black-box contract tests for V11 (Backend Capability Matrix).
// Spec: docs/feature-spec-v2.md §3 V11, §7 glossary.
//
// Pinned contracts:
//   - KnownCapabilities is a package-level slice with at least 11 entries
//     (vLLM, SGLang, LMCache, NIM, TGI, TRT-LLM, Triton, llama.cpp, LiteLLM, Ollama, Unknown).
//   - CapabilityFor(BackendVLLM).TTFT == CapabilityFull.
//   - CapabilityFor(BackendOllama).TTFT == CapabilityNone.
//   - CapabilityFor(BackendVLLM).PrefixHit == CapabilityPartial.
//   - CapabilityFor round-trip: every entry in KnownCapabilities is returned by CapabilityFor.
//   - Specific cells (SGLang/LMCache/TGI/LiteLLM/llama.cpp/TRT-LLM/Unknown)
//     are locked individually — those drive routing/observability advice.
//   - RenderCapabilityMatrix non-empty for non-empty detected slice.
//   - RenderCapabilityMatrix with detected=nil contains "no workers detected".

package ui_test

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/internal/ui"
)

// ─── KnownCapabilities ───────────────────────────────────────────────────────

func TestKnownCapabilities_HasAtLeast11Entries(t *testing.T) {
	if got := len(ui.KnownCapabilities); got < 11 {
		t.Errorf("KnownCapabilities should have at least 11 entries (10 backends + Unknown), got %d", got)
	}
}

func TestKnownCapabilities_AllEntriesHaveDistinctBackend(t *testing.T) {
	seen := make(map[metrics.Backend]bool)
	for i, e := range ui.KnownCapabilities {
		if seen[e.Backend] {
			t.Errorf("KnownCapabilities[%d]: duplicate Backend value %v", i, e.Backend)
		}
		seen[e.Backend] = true
	}
}

// ─── CapabilityFor — vLLM and Ollama (spec-named explicitly) ─────────────────

func TestCapabilityFor_VLLM_TTFT_Full(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendVLLM)
	if got.TTFT != ui.CapabilityFull {
		t.Errorf("CapabilityFor(BackendVLLM).TTFT should be CapabilityFull, got %v", got.TTFT)
	}
}

func TestCapabilityFor_VLLM_PrefixHit_Partial(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendVLLM)
	if got.PrefixHit != ui.CapabilityPartial {
		t.Errorf("CapabilityFor(BackendVLLM).PrefixHit should be CapabilityPartial (requires --enable-prefix-caching), got %v", got.PrefixHit)
	}
}

func TestCapabilityFor_Ollama_TTFT_None(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendOllama)
	if got.TTFT != ui.CapabilityNone {
		t.Errorf("CapabilityFor(BackendOllama).TTFT should be CapabilityNone (JSON-only, no histograms), got %v", got.TTFT)
	}
}

func TestCapabilityFor_Ollama_ITL_None(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendOllama)
	if got.ITL != ui.CapabilityNone {
		t.Errorf("CapabilityFor(BackendOllama).ITL should be CapabilityNone, got %v", got.ITL)
	}
}

// Round-trip: every Backend value in KnownCapabilities must be retrievable.
func TestCapabilityFor_RoundTrip_KnownEntriesRetrievable(t *testing.T) {
	for i, entry := range ui.KnownCapabilities {
		got := ui.CapabilityFor(entry.Backend)
		if got.Backend != entry.Backend {
			t.Errorf("KnownCapabilities[%d]: CapabilityFor(%v) returned Backend=%v",
				i, entry.Backend, got.Backend)
		}
	}
}

// ─── Specific cells — V11 correctness ────────────────────────────────────────
// These pins lock the matrix entries that drive routing/observability advice
// in the UI. A regression in any of them silently lies to the user.

func TestCapabilityFor_SGLang_PrefixHit_Full(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendSGLang)
	if got.PrefixHit != ui.CapabilityFull {
		t.Errorf("SGLang.PrefixHit should be Full (SGLang emits sgl:cache_hit_rate), got %v", got.PrefixHit)
	}
}

func TestCapabilityFor_LMCache_KVNVMe_Full(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendLMCache)
	if got.KVNVMe != ui.CapabilityFull {
		t.Errorf("LMCache.KVNVMe should be Full (NVMe is LMCache's tier-3), got %v", got.KVNVMe)
	}
}

func TestCapabilityFor_LMCache_TTFT_None(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendLMCache)
	if got.TTFT != ui.CapabilityNone {
		t.Errorf("LMCache.TTFT should be None (transport-layer only, no request latency), got %v", got.TTFT)
	}
}

func TestCapabilityFor_TGI_PrefixHit_None(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendTGI)
	if got.PrefixHit != ui.CapabilityNone {
		t.Errorf("TGI.PrefixHit should be None (no prefix-cache exposure), got %v", got.PrefixHit)
	}
}

func TestCapabilityFor_LiteLLM_Tenants_Full(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendLiteLLM)
	if got.Tenants != ui.CapabilityFull {
		t.Errorf("LiteLLM.Tenants should be Full (router-level tenant labels), got %v", got.Tenants)
	}
}

func TestCapabilityFor_LiteLLM_KVGPU_None(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendLiteLLM)
	if got.KVGPU != ui.CapabilityNone {
		t.Errorf("LiteLLM.KVGPU should be None (proxy, no engine internals), got %v", got.KVGPU)
	}
}

func TestCapabilityFor_LlamaCpp_KVCPU_Full(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendLlamaCpp)
	if got.KVCPU != ui.CapabilityFull {
		t.Errorf("llama.cpp.KVCPU should be Full (CPU-bound by design), got %v", got.KVCPU)
	}
}

func TestCapabilityFor_LlamaCpp_KVGPU_None(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendLlamaCpp)
	if got.KVGPU != ui.CapabilityNone {
		t.Errorf("llama.cpp.KVGPU should be None (no HBM exposure), got %v", got.KVGPU)
	}
}

func TestCapabilityFor_LlamaCpp_DCGM_None(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendLlamaCpp)
	if got.DCGM != ui.CapabilityNone {
		t.Errorf("llama.cpp.DCGM should be None (no GPU surface), got %v", got.DCGM)
	}
}

func TestCapabilityFor_TRTLLM_PrefixHit_Partial(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendTRTLLM)
	if got.PrefixHit != ui.CapabilityPartial {
		t.Errorf("TRT-LLM.PrefixHit should be Partial (requires KV reuse plugin), got %v", got.PrefixHit)
	}
}

func TestCapabilityFor_VLLM_DCGM_Full(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendVLLM)
	if got.DCGM != ui.CapabilityFull {
		t.Errorf("vLLM.DCGM should be Full, got %v", got.DCGM)
	}
}

func TestCapabilityFor_Unknown_AllNone(t *testing.T) {
	got := ui.CapabilityFor(metrics.BackendUnknown)
	cells := map[string]ui.CapabilityLevel{
		"KVGPU":     got.KVGPU,
		"KVCPU":     got.KVCPU,
		"KVNVMe":    got.KVNVMe,
		"TTFT":      got.TTFT,
		"ITL":       got.ITL,
		"PrefixHit": got.PrefixHit,
		"Tenants":   got.Tenants,
		"XferP99":   got.XferP99,
		"DCGM":      got.DCGM,
		"Roles":     got.Roles,
	}
	for name, lvl := range cells {
		if lvl != ui.CapabilityNone {
			t.Errorf("Unknown.%s should be None, got %v", name, lvl)
		}
	}
}

// ─── CapabilityLevel constants ───────────────────────────────────────────────

func TestCapabilityLevel_FullPartialNoneAreDistinct(t *testing.T) {
	if ui.CapabilityFull == ui.CapabilityPartial {
		t.Errorf("CapabilityFull and CapabilityPartial must differ")
	}
	if ui.CapabilityPartial == ui.CapabilityNone {
		t.Errorf("CapabilityPartial and CapabilityNone must differ")
	}
	if ui.CapabilityFull == ui.CapabilityNone {
		t.Errorf("CapabilityFull and CapabilityNone must differ")
	}
}

// ─── RenderCapabilityMatrix ──────────────────────────────────────────────────

func TestRenderCapabilityMatrix_DetectedVLLM_NonEmptyOutput(t *testing.T) {
	out := ui.RenderCapabilityMatrix([]metrics.Backend{metrics.BackendVLLM}, 120, 30)
	if out == "" {
		t.Errorf("RenderCapabilityMatrix with detected=[vLLM] should not be empty")
	}
}

func TestRenderCapabilityMatrix_NilDetected_ContainsNoWorkersMessage(t *testing.T) {
	out := ui.RenderCapabilityMatrix(nil, 120, 30)
	// Spec: empty detected ⇒ matrix shows top-line "(no workers detected — showing full capability reference)".
	if !strings.Contains(out, "no workers detected") {
		t.Errorf("RenderCapabilityMatrix with detected=nil should contain \"no workers detected\", got %q", out)
	}
}

func TestRenderCapabilityMatrix_DetectedNonEmpty_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderCapabilityMatrix panicked: %v", r)
		}
	}()
	_ = ui.RenderCapabilityMatrix([]metrics.Backend{metrics.BackendVLLM, metrics.BackendOllama}, 120, 30)
}

func TestRenderCapabilityMatrix_NarrowWidth_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RenderCapabilityMatrix panicked at narrow width: %v", r)
		}
	}()
	_ = ui.RenderCapabilityMatrix([]metrics.Backend{metrics.BackendVLLM}, 50, 20)
}
