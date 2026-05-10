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
