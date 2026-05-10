package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestKnownCapabilities_HasMinimumEntries(t *testing.T) {
	wanted := []metrics.Backend{
		metrics.BackendVLLM,
		metrics.BackendSGLang,
		metrics.BackendNIM,
		metrics.BackendTGI,
		metrics.BackendTRTLLM,
		metrics.BackendTriton,
		metrics.BackendLlamaCpp,
		metrics.BackendLiteLLM,
		metrics.BackendOllama,
	}
	for _, w := range wanted {
		found := false
		for _, c := range KnownCapabilities {
			if c.Backend == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("KnownCapabilities missing entry for %s", w)
		}
	}
}

func TestCapabilityFor_VLLM_TTFTFull(t *testing.T) {
	c := CapabilityFor(metrics.BackendVLLM)
	if c.TTFT != CapabilityFull {
		t.Errorf("vLLM TTFT expected CapabilityFull, got %v", c.TTFT)
	}
}

func TestCapabilityFor_Ollama_TTFTNone(t *testing.T) {
	c := CapabilityFor(metrics.BackendOllama)
	if c.TTFT != CapabilityNone {
		t.Errorf("Ollama TTFT expected CapabilityNone, got %v", c.TTFT)
	}
}

func TestCapabilityFor_VLLM_PrefixHitPartial(t *testing.T) {
	c := CapabilityFor(metrics.BackendVLLM)
	if c.PrefixHit != CapabilityPartial {
		t.Errorf("vLLM PrefixHit expected CapabilityPartial, got %v", c.PrefixHit)
	}
}

func TestCapabilityFor_UnknownBackend_ReturnsNoneRow(t *testing.T) {
	c := CapabilityFor(metrics.Backend("MysteryEngine"))
	if c.Backend != "MysteryEngine" {
		t.Errorf("expected echoed backend name, got %s", c.Backend)
	}
	if c.TTFT != CapabilityNone {
		t.Errorf("unknown backend TTFT expected CapabilityNone, got %v", c.TTFT)
	}
}

func TestRenderCapabilityMatrix_VLLMDetected_HasCheckmark(t *testing.T) {
	out := RenderCapabilityMatrix([]metrics.Backend{metrics.BackendVLLM}, 120, 40)
	if !strings.Contains(out, "vLLM") {
		t.Errorf("expected vLLM label in output")
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("expected at least one ✓ checkmark in output")
	}
}

func TestRenderCapabilityMatrix_NoWorkers_ShowsReference(t *testing.T) {
	out := RenderCapabilityMatrix(nil, 120, 40)
	if !strings.Contains(out, "no workers detected") {
		t.Errorf("expected 'no workers detected' message, got %q", out)
	}
}

func TestRenderCapabilityMatrix_NarrowWidth_UsesShortHeaders(t *testing.T) {
	// Width < 80 → short labels in the column header row. The column
	// header is the first table row — assert that the short token "PFX"
	// appears (the abbreviation for PREFIX-HIT).
	out := RenderCapabilityMatrix([]metrics.Backend{metrics.BackendVLLM}, 70, 40)
	if !strings.Contains(out, "PFX") {
		t.Errorf("narrow output should use short column label 'PFX', got %q", out)
	}
}

func TestRenderCapabilityMatrix_VeryNarrowWidth_FallbackOK(t *testing.T) {
	out := RenderCapabilityMatrix([]metrics.Backend{metrics.BackendVLLM}, 50, 40)
	if !strings.Contains(out, "vLLM") {
		t.Errorf("very-narrow fallback should still show backend name, got %q", out)
	}
}

func TestDetectedBackends_DedupesAndExcludesNil(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Backend: metrics.BackendVLLM},
		{Backend: metrics.BackendVLLM},
		nil,
		{Backend: metrics.BackendSGLang},
	}
	got := detectedBackends(workers)
	if len(got) != 2 {
		t.Errorf("expected 2 unique backends, got %d (%v)", len(got), got)
	}
}
