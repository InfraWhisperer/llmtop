package ui

import (
	"strings"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// TestRenderTenantCard_WithoutTopTenants leaves the legacy placeholder
// fields intact (no regression for backends that don't emit request_tag).
func TestRenderTenantCard_WithoutTopTenants(t *testing.T) {
	g := metrics.ModelGroup{
		ModelName: "llama-3.1",
		Workers:   3,
		// TopTenants is empty.
	}
	out := renderTenantCard(g, 40)
	if strings.Contains(out, "Top tenants") {
		t.Errorf("expected no top tenants block when TopTenants empty, got:\n%s", out)
	}
}

// TestRenderTenantCard_WithTopTenants renders up to 5 tenant rows with TAG,
// SHARE%, TOK/S — and never a TTFT warning (F3 acceptance).
func TestRenderTenantCard_WithTopTenants(t *testing.T) {
	g := metrics.ModelGroup{
		ModelName: "llama-3.1",
		Workers:   3,
		TopTenants: []metrics.TenantMetrics{
			{Tag: "team-search", RequestShare: 0.35, TokPerSec: 1234},
			{Tag: "team-rag", RequestShare: 0.25, TokPerSec: 890},
		},
	}
	out := renderTenantCard(g, 50)
	if !strings.Contains(out, "Top tenants") {
		t.Errorf("expected 'Top tenants' header, got:\n%s", out)
	}
	if !strings.Contains(out, "team-search") || !strings.Contains(out, "team-rag") {
		t.Errorf("expected tenant tags rendered, got:\n%s", out)
	}
	if !strings.Contains(out, "35%") || !strings.Contains(out, "25%") {
		t.Errorf("expected share %% values rendered, got:\n%s", out)
	}
	// Per spec Open Q §1, the tenant rows must not carry the "⚠ TTFT"
	// indicator (vLLM does not emit per-tag histograms). The card-wide
	// "TTFT p99" field belongs to the model group, not a tenant.
	if strings.Contains(out, "⚠") {
		t.Errorf("F3: tenant rows must not carry warning indicator, got:\n%s", out)
	}
}

// TestTenantRowsForCard_CapsAtFive defends against an upstream contract
// violation that shipped >5 entries.
func TestTenantRowsForCard_CapsAtFive(t *testing.T) {
	tt := []metrics.TenantMetrics{
		{Tag: "a"}, {Tag: "b"}, {Tag: "c"}, {Tag: "d"},
		{Tag: "e"}, {Tag: "f"}, {Tag: "g"},
	}
	if got := len(tenantRowsForCard(tt)); got != 5 {
		t.Errorf("expected at most 5 rows, got %d", got)
	}
}

// TestRenderTenantRow_FormatsKShorthand confirms the tenant row uses the
// "X.Xk tok/s" abbreviation for >1000.
func TestRenderTenantRow_FormatsKShorthand(t *testing.T) {
	row := renderTenantRow(metrics.TenantMetrics{Tag: "team-search", RequestShare: 0.35, TokPerSec: 1234}, 60)
	if !strings.Contains(row, "1.2k") {
		t.Errorf("expected k-shorthand in tenant row, got:\n%s", row)
	}
}
