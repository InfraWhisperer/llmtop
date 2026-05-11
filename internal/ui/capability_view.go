package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// CapabilityLevel encodes the support level a backend has for one metric.
type CapabilityLevel int

const (
	// CapabilityFull: metric is always emitted by this backend.
	CapabilityFull CapabilityLevel = iota
	// CapabilityPartial: metric is conditional, requires a flag, or is
	// approximate. Examples: vLLM prefix-cache hit ratio requires
	// --enable-prefix-caching; SGLang KV-CPU is conditional on offload.
	CapabilityPartial
	// CapabilityNone: the backend does not emit this metric.
	CapabilityNone
)

// glyph returns the single-rune symbol used in the matrix cell.
func (c CapabilityLevel) glyph() string {
	switch c {
	case CapabilityFull:
		return "✓"
	case CapabilityPartial:
		return "~"
	default:
		return "—"
	}
}

// style returns the lipgloss style applied to the glyph.
func (c CapabilityLevel) style() lipgloss.Style {
	switch c {
	case CapabilityFull:
		return StyleMetricGood
	case CapabilityPartial:
		return StyleMetricWarn
	default:
		return StyleMetricNA
	}
}

// BackendCapability describes one row of the V11 capability matrix.
type BackendCapability struct {
	Backend   metrics.Backend
	KVGPU     CapabilityLevel
	KVCPU     CapabilityLevel
	KVNVMe    CapabilityLevel
	TTFT      CapabilityLevel
	ITL       CapabilityLevel
	PrefixHit CapabilityLevel
	Tenants   CapabilityLevel
	XferP99   CapabilityLevel
	DCGM      CapabilityLevel
	Roles     CapabilityLevel
	Notes     string
}

// KnownCapabilities is the static capability table. Entries cover every
// backend llmtop currently detects, plus a sentinel Unknown row. Values
// reflect what the Prometheus collectors observe today; update alongside
// any change to the per-backend collector parsers.
var KnownCapabilities = []BackendCapability{
	{
		Backend:   metrics.BackendVLLM,
		KVGPU:     CapabilityFull,
		KVCPU:     CapabilityPartial,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityFull,
		ITL:       CapabilityFull,
		PrefixHit: CapabilityPartial,
		Tenants:   CapabilityPartial,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityFull,
		Roles:     CapabilityPartial,
		Notes:     "requires --enable-prefix-caching for PREFIX-HIT; KV-CPU on CPU offload only",
	},
	{
		Backend:   metrics.BackendSGLang,
		KVGPU:     CapabilityFull,
		KVCPU:     CapabilityPartial,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityFull,
		ITL:       CapabilityFull,
		PrefixHit: CapabilityFull,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityFull,
		Roles:     CapabilityNone,
		Notes:     "radix-tree prefix hit emitted by default; per-tenant labels not implemented",
	},
	{
		Backend:   metrics.BackendLMCache,
		KVGPU:     CapabilityFull,
		KVCPU:     CapabilityFull,
		KVNVMe:    CapabilityFull,
		TTFT:      CapabilityNone,
		ITL:       CapabilityNone,
		PrefixHit: CapabilityFull,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityFull,
		DCGM:      CapabilityFull,
		Roles:     CapabilityFull,
		Notes:     "KV transport-layer metrics; no request-side latency (TTFT/ITL come from the engine in front)",
	},
	{
		Backend:   metrics.BackendNIM,
		KVGPU:     CapabilityFull,
		KVCPU:     CapabilityNone,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityFull,
		ITL:       CapabilityFull,
		PrefixHit: CapabilityPartial,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityFull,
		Roles:     CapabilityNone,
		Notes:     "NIM containers wrap TRT-LLM or vLLM; capability follows underlying engine",
	},
	{
		Backend:   metrics.BackendTGI,
		KVGPU:     CapabilityPartial,
		KVCPU:     CapabilityNone,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityFull,
		ITL:       CapabilityFull,
		PrefixHit: CapabilityNone,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityFull,
		Roles:     CapabilityNone,
		Notes:     "no prefix-cache exposure; KV-GPU reported as batch fill, not block utilization",
	},
	{
		Backend:   metrics.BackendTRTLLM,
		KVGPU:     CapabilityFull,
		KVCPU:     CapabilityNone,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityFull,
		ITL:       CapabilityFull,
		PrefixHit: CapabilityPartial,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityFull,
		Roles:     CapabilityNone,
		Notes:     "PREFIX-HIT requires Triton+TRT-LLM with KV reuse plugin enabled",
	},
	{
		Backend:   metrics.BackendTriton,
		KVGPU:     CapabilityPartial,
		KVCPU:     CapabilityNone,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityPartial,
		ITL:       CapabilityNone,
		PrefixHit: CapabilityNone,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityFull,
		Roles:     CapabilityNone,
		Notes:     "generic inference server: per-backend metric depth varies; LLM specifics depend on the loaded backend",
	},
	{
		Backend:   metrics.BackendLlamaCpp,
		KVGPU:     CapabilityNone,
		KVCPU:     CapabilityFull,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityNone,
		ITL:       CapabilityNone,
		PrefixHit: CapabilityNone,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityNone,
		Roles:     CapabilityNone,
		Notes:     "CPU-bound; no HBM, no histograms, no DCGM",
	},
	{
		Backend:   metrics.BackendLiteLLM,
		KVGPU:     CapabilityNone,
		KVCPU:     CapabilityNone,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityFull,
		ITL:       CapabilityNone,
		PrefixHit: CapabilityNone,
		Tenants:   CapabilityFull,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityNone,
		Roles:     CapabilityNone,
		Notes:     "router/proxy: aggregates request-level latency across upstream engines; no engine internals",
	},
	{
		Backend:   metrics.BackendOllama,
		KVGPU:     CapabilityNone,
		KVCPU:     CapabilityNone,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityNone,
		ITL:       CapabilityNone,
		PrefixHit: CapabilityNone,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityNone,
		Roles:     CapabilityNone,
		Notes:     "JSON-only; no Prometheus surface, no histograms",
	},
	{
		Backend:   metrics.BackendUnknown,
		KVGPU:     CapabilityNone,
		KVCPU:     CapabilityNone,
		KVNVMe:    CapabilityNone,
		TTFT:      CapabilityNone,
		ITL:       CapabilityNone,
		PrefixHit: CapabilityNone,
		Tenants:   CapabilityNone,
		XferP99:   CapabilityNone,
		DCGM:      CapabilityNone,
		Roles:     CapabilityNone,
		Notes:     "(capability data unavailable for this backend)",
	},
}

// CapabilityFor returns the entry for b. When b is not in
// KnownCapabilities, returns the Unknown sentinel row with b substituted.
func CapabilityFor(b metrics.Backend) BackendCapability {
	for _, c := range KnownCapabilities {
		if c.Backend == b {
			return c
		}
	}
	// Fall back to an Unknown-shaped row tagged with the actual backend.
	for _, c := range KnownCapabilities {
		if c.Backend == metrics.BackendUnknown {
			cp := c
			cp.Backend = b
			return cp
		}
	}
	return BackendCapability{Backend: b, Notes: "(capability data unavailable for this backend)"}
}

// capabilityColumns returns the column descriptors used by both the matrix
// renderer and tests. Order is the spec's canonical column order.
type capabilityColumn struct {
	short string
	long  string
	get   func(c BackendCapability) CapabilityLevel
}

func capabilityColumns() []capabilityColumn {
	return []capabilityColumn{
		{"KV-G", "KV-GPU", func(c BackendCapability) CapabilityLevel { return c.KVGPU }},
		{"KV-C", "KV-CPU", func(c BackendCapability) CapabilityLevel { return c.KVCPU }},
		{"KV-N", "KV-NVMe", func(c BackendCapability) CapabilityLevel { return c.KVNVMe }},
		{"TTFT", "TTFT", func(c BackendCapability) CapabilityLevel { return c.TTFT }},
		{"ITL", "ITL", func(c BackendCapability) CapabilityLevel { return c.ITL }},
		{"PFX", "PREFIX-HIT", func(c BackendCapability) CapabilityLevel { return c.PrefixHit }},
		{"TEN", "TENANTS", func(c BackendCapability) CapabilityLevel { return c.Tenants }},
		{"XFR", "XFER-P99", func(c BackendCapability) CapabilityLevel { return c.XferP99 }},
		{"GPU", "GPU-DCGM", func(c BackendCapability) CapabilityLevel { return c.DCGM }},
		{"ROL", "ROLES", func(c BackendCapability) CapabilityLevel { return c.Roles }},
	}
}

// RenderCapabilityMatrix renders the V11 full-screen capability overlay.
//
// detected lists backends present in the live worker set; rows for these
// backends sort first (highlighted) followed by absent backends (dimmed).
// width and height steer narrow-terminal degradation.
func RenderCapabilityMatrix(detected []metrics.Backend, width, height int) string {
	var sb strings.Builder

	sb.WriteString(StyleHelpTitle.Render("Backend Capability Matrix"))
	sb.WriteString("\n")
	if len(detected) == 0 {
		sb.WriteString(StyleMetricNA.Render("(no workers detected — showing full capability reference)"))
		sb.WriteString("\n")
	} else {
		var names []string
		for _, b := range detected {
			names = append(names, string(b))
		}
		sb.WriteString(StyleMetricNA.Render("Detected: "))
		sb.WriteString(StyleHeaderValue.Render(strings.Join(names, ", ")))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	if width < 60 {
		// Single-column fallback.
		for _, c := range orderedCapabilities(detected) {
			present := isDetected(c.Backend, detected)
			label := string(c.Backend)
			if present {
				sb.WriteString(StyleHeaderValue.Render(label))
			} else {
				sb.WriteString(StyleMetricNA.Render(label))
			}
			sb.WriteString("\n  ")
			sb.WriteString(StyleMetricNA.Render(c.Notes))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		sb.WriteString(StyleFooter.Render("Press any key to close"))
		return sb.String()
	}

	cols := capabilityColumns()
	useShort := width < 80

	// Header row.
	sb.WriteString(padRight("BACKEND", 12))
	for _, col := range cols {
		label := col.long
		if useShort {
			label = col.short
		}
		sb.WriteString(StyleTableHeader.Render(padRight(label, columnWidth(label, useShort))))
	}
	sb.WriteString("\n")

	for _, c := range orderedCapabilities(detected) {
		present := isDetected(c.Backend, detected)
		nameLabel := padRight(string(c.Backend), 12)
		if present {
			sb.WriteString(StyleHeaderValue.Render(nameLabel))
		} else {
			sb.WriteString(StyleMetricNA.Render(nameLabel))
		}
		for _, col := range cols {
			level := col.get(c)
			text := padRight(level.glyph(), columnWidth(col.long, useShort))
			if present {
				sb.WriteString(level.style().Render(text))
			} else {
				sb.WriteString(StyleMetricNA.Render(text))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(StyleHelpTitle.Render("Notes"))
	sb.WriteString("\n")
	for _, c := range orderedCapabilities(detected) {
		present := isDetected(c.Backend, detected)
		line := fmt.Sprintf("  %-12s %s", string(c.Backend)+":", c.Notes)
		if present {
			sb.WriteString(StyleHeaderValue.Render(string(c.Backend) + ":"))
			sb.WriteString(" ")
			sb.WriteString(StyleMetricNA.Render(c.Notes))
		} else {
			sb.WriteString(StyleMetricNA.Render(line))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(StyleFooter.Render("Legend: ✓ full · ~ partial/conditional · — not emitted   |   Press any key to close"))
	return sb.String()
}

// orderedCapabilities returns KnownCapabilities sorted with detected
// backends first, then absent backends in the original order.
func orderedCapabilities(detected []metrics.Backend) []BackendCapability {
	var present, absent []BackendCapability
	for _, c := range KnownCapabilities {
		if isDetected(c.Backend, detected) {
			present = append(present, c)
		} else {
			absent = append(absent, c)
		}
	}
	return append(present, absent...)
}

func isDetected(b metrics.Backend, detected []metrics.Backend) bool {
	return slices.Contains(detected, b)
}

// columnWidth picks a per-column width that accommodates either the short
// or long label.
func columnWidth(label string, useShort bool) int {
	if useShort {
		return 5
	}
	w := len(label) + 2
	if w < 8 {
		return 8
	}
	return w
}

// detectedBackends extracts the unique sorted set of backend types from a
// worker slice. Used by the C-key handler in app.go.
func detectedBackends(workers []*metrics.WorkerMetrics) []metrics.Backend {
	seen := make(map[metrics.Backend]bool)
	for _, w := range workers {
		if w == nil {
			continue
		}
		seen[w.Backend] = true
	}
	out := make([]metrics.Backend, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	return out
}
