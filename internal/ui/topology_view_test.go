package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/pkg/config"
)

func keyMsg(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	// Multi-char keys like "enter"
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestDetectRoleFromLabels(t *testing.T) {
	tests := []struct {
		name  string
		label string
		ep    string
		want  WorkerRole
	}{
		{"prefill in label", "prefill-1", "http://10.0.0.1:8000", RolePrefill},
		{"decode in label", "decode-worker-0", "http://10.0.0.2:8000", RoleDecode},
		{"prefill in endpoint", "", "http://prefill-svc:8000", RolePrefill},
		{"decode in endpoint", "", "k8s://decode-0.ns:8000", RoleDecode},
		{"no hint", "vllm-worker-0", "http://10.0.0.3:8000", RoleUnknown},
		{"case insensitive", "Prefill-GPU0", "http://10.0.0.4:8000", RolePrefill},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &metrics.WorkerMetrics{Label: tt.label, Endpoint: tt.ep}
			got := detectRoleFromLabels(w)
			if got != tt.want {
				t.Errorf("detectRoleFromLabels(%q, %q) = %d, want %d", tt.label, tt.ep, got, tt.want)
			}
		})
	}
}

func TestBuildTopologyExplicitConfig(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://10.0.0.4:8000", Label: "p1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 91, RequestsWaiting: 18},
		{Endpoint: "http://10.0.0.5:8000", Label: "p2", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 88, RequestsWaiting: 14},
		{Endpoint: "http://10.0.0.6:8000", Label: "d1", Backend: metrics.BackendSGLang, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 45, RequestsWaiting: 2, GenTokPerSec: 500},
		{Endpoint: "http://10.0.0.7:8000", Label: "d2", Backend: metrics.BackendSGLang, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 52, RequestsWaiting: 4, GenTokPerSec: 620},
	}

	topoConfig := []config.TopologyEntry{
		{
			Role:      "prefill",
			Endpoints: []string{"http://10.0.0.4:8000", "http://10.0.0.5:8000"},
			Feeds:     []string{"http://10.0.0.6:8000", "http://10.0.0.7:8000"},
		},
		{
			Role:      "decode",
			Endpoints: []string{"http://10.0.0.6:8000", "http://10.0.0.7:8000"},
		},
	}

	g := BuildTopology(workers, topoConfig)

	if !g.Detected {
		t.Fatal("expected topology to be detected with explicit config")
	}

	nodes, ok := g.ModelGroups["llama-70b"]
	if !ok {
		t.Fatal("expected llama-70b model group")
	}
	if len(nodes) != 4 {
		t.Errorf("expected 4 nodes, got %d", len(nodes))
	}

	// Verify edges
	feeds, ok := g.Edges["http://10.0.0.4:8000"]
	if !ok {
		t.Fatal("expected edges from prefill-1")
	}
	if len(feeds) != 2 {
		t.Errorf("expected 2 decode targets, got %d", len(feeds))
	}
}

func TestBuildTopologyAutoDetectByName(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://prefill-1:8000", Label: "prefill-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 90, RequestsWaiting: 15},
		{Endpoint: "http://decode-1:8000", Label: "decode-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 40, RequestsWaiting: 2, GenTokPerSec: 500},
	}

	g := BuildTopology(workers, nil)

	if !g.Detected {
		t.Fatal("expected topology to be detected from naming convention")
	}

	nodes := g.ModelGroups["llama-70b"]
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}

	var prefillCount, decodeCount int
	for _, n := range nodes {
		if n.Role == RolePrefill {
			prefillCount++
		}
		if n.Role == RoleDecode {
			decodeCount++
		}
	}
	if prefillCount != 1 || decodeCount != 1 {
		t.Errorf("expected 1 prefill + 1 decode, got %d prefill + %d decode", prefillCount, decodeCount)
	}
}

func TestBuildTopologyAutoDetectByMetrics(t *testing.T) {
	// Prefill workers: high KV, high queue, zero gen tok/s
	// Decode workers: lower KV, low queue, has gen tok/s
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://10.0.0.1:8000", Label: "worker-a", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 92, RequestsWaiting: 20, GenTokPerSec: 0, PromptTokPerSec: 150},
		{Endpoint: "http://10.0.0.2:8000", Label: "worker-b", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 40, RequestsWaiting: 2, GenTokPerSec: 500},
	}

	g := BuildTopology(workers, nil)

	if !g.Detected {
		t.Fatal("expected topology to be detected from metric heuristics")
	}

	nodes := g.ModelGroups["llama-70b"]
	byEndpoint := make(map[string]TopologyNode)
	for _, n := range nodes {
		byEndpoint[n.Worker.Endpoint] = n
	}

	if byEndpoint["http://10.0.0.1:8000"].Role != RolePrefill {
		t.Error("expected worker-a (zero gen tok/s, high KV) to be classified as prefill")
	}
	if byEndpoint["http://10.0.0.2:8000"].Role != RoleDecode {
		t.Error("expected worker-b (has gen tok/s, low KV) to be classified as decode")
	}
}

func TestBuildTopologyNoDisaggregation(t *testing.T) {
	// All workers look the same — no disaggregation detected.
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://10.0.0.1:8000", Label: "w1", Online: true, KVCacheUsagePct: 50, GenTokPerSec: 400},
		{Endpoint: "http://10.0.0.2:8000", Label: "w2", Online: true, KVCacheUsagePct: 52, GenTokPerSec: 410},
	}

	g := BuildTopology(workers, nil)

	if g.Detected {
		t.Error("expected no topology detection for uniform workers")
	}
}

func TestBuildTopologyEmpty(t *testing.T) {
	g := BuildTopology(nil, nil)
	if g.Detected {
		t.Error("expected no topology detection for empty workers")
	}
}

func TestFlattenTopologyNodes(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://prefill-1:8000", Label: "prefill-1", ModelName: "llama-70b", Online: true, KVCacheUsagePct: 90},
		{Endpoint: "http://decode-1:8000", Label: "decode-1", ModelName: "llama-70b", Online: true, GenTokPerSec: 500},
	}

	g := BuildTopology(workers, nil)
	flat := FlattenTopologyNodes(g)

	if len(flat) != 2 {
		t.Fatalf("expected 2 flattened nodes, got %d", len(flat))
	}

	// Prefill should come first.
	if !strings.Contains(flat[0].Label, "prefill") {
		t.Errorf("expected first node to be prefill, got %q", flat[0].Label)
	}
	if !strings.Contains(flat[1].Label, "decode") {
		t.Errorf("expected second node to be decode, got %q", flat[1].Label)
	}
}

func TestRenderTopologyNoDetection(t *testing.T) {
	g := TopologyGraph{Detected: false}
	out := RenderTopology(g, 0, 120, 40)

	if !strings.Contains(out, "No prefill/decode topology detected") {
		t.Error("expected 'no topology detected' message")
	}
	if !strings.Contains(out, "unified serving") {
		t.Error("expected 'unified serving' message")
	}
}

func TestRenderTopologyWithNodes(t *testing.T) {
	workers := []*metrics.WorkerMetrics{
		{Endpoint: "http://prefill-1:8000", Label: "prefill-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 91, RequestsWaiting: 18},
		{Endpoint: "http://decode-1:8000", Label: "decode-1", Backend: metrics.BackendSGLang, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 45, RequestsWaiting: 2, GenTokPerSec: 500},
	}

	g := BuildTopology(workers, nil)
	out := RenderTopology(g, 0, 120, 40)

	if !strings.Contains(out, "prefill-1") {
		t.Error("expected prefill-1 in output")
	}
	if !strings.Contains(out, "decode-1") {
		t.Error("expected decode-1 in output")
	}
	if !strings.Contains(out, "KV:") {
		t.Error("expected KV stats in output")
	}
	if !strings.Contains(out, "────▶") {
		t.Error("expected edge arrow in output")
	}
}

func TestRenderNodeBoxHealthColors(t *testing.T) {
	// Red: KV >= 85%
	red := TopologyNode{
		Worker: &metrics.WorkerMetrics{
			Label: "hot", Backend: metrics.BackendVLLM, Online: true,
			KVCacheUsagePct: 92, RequestsWaiting: 25,
		},
		Role: RolePrefill,
	}
	out := renderNodeBox(red, false, 40)
	if out == "" {
		t.Error("expected non-empty box for high-KV node")
	}

	// Green: KV < 70%
	green := TopologyNode{
		Worker: &metrics.WorkerMetrics{
			Label: "cool", Backend: metrics.BackendVLLM, Online: true,
			KVCacheUsagePct: 30, RequestsWaiting: 1,
		},
		Role: RoleDecode,
	}
	out = renderNodeBox(green, true, 40)
	if out == "" {
		t.Error("expected non-empty box for low-KV node")
	}

	// Offline
	offline := TopologyNode{
		Worker: &metrics.WorkerMetrics{
			Label: "down", Backend: metrics.BackendVLLM, Online: false,
		},
		Role: RolePrefill,
	}
	out = renderNodeBox(offline, false, 40)
	if out == "" {
		t.Error("expected non-empty box for offline node")
	}
}

func TestTopologyViewKeyHandler(t *testing.T) {
	m := newTestModel()
	m.workers = []*metrics.WorkerMetrics{
		{Endpoint: "http://prefill-1:8000", Label: "prefill-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 90},
		{Endpoint: "http://decode-1:8000", Label: "decode-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, GenTokPerSec: 500},
	}
	m.topoGraph = BuildTopology(m.workers, nil)
	m.currentView = ViewTopology

	// T returns to main
	updated, _ := m.Update(keyMsg("T"))
	model := updated.(Model)
	if model.currentView != ViewMain {
		t.Errorf("expected ViewMain after T from topology, got %d", model.currentView)
	}
}

func TestTopologyViewNavigation(t *testing.T) {
	m := newTestModel()
	m.workers = []*metrics.WorkerMetrics{
		{Endpoint: "http://prefill-1:8000", Label: "prefill-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 90},
		{Endpoint: "http://decode-1:8000", Label: "decode-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, GenTokPerSec: 500},
	}
	m.topoGraph = BuildTopology(m.workers, nil)
	m.currentView = ViewTopology

	// j → down
	updated, _ := m.Update(keyMsg("j"))
	model := updated.(Model)
	if model.topoSelectedIdx != 1 {
		t.Errorf("expected topoSelectedIdx=1, got %d", model.topoSelectedIdx)
	}

	// j at bottom → clamp
	updated, _ = model.Update(keyMsg("j"))
	model = updated.(Model)
	if model.topoSelectedIdx != 1 {
		t.Errorf("expected topoSelectedIdx=1 (clamped), got %d", model.topoSelectedIdx)
	}

	// k → up
	updated, _ = model.Update(keyMsg("k"))
	model = updated.(Model)
	if model.topoSelectedIdx != 0 {
		t.Errorf("expected topoSelectedIdx=0, got %d", model.topoSelectedIdx)
	}
}

func TestTopologyEnterGoesToDetail(t *testing.T) {
	m := newTestModel()
	m.workers = []*metrics.WorkerMetrics{
		{Endpoint: "http://prefill-1:8000", Label: "prefill-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 90},
		{Endpoint: "http://decode-1:8000", Label: "decode-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, GenTokPerSec: 500},
	}
	m.topoGraph = BuildTopology(m.workers, nil)
	m.currentView = ViewTopology
	m.topoSelectedIdx = 1 // decode-1

	updated, _ := m.Update(keyMsg("enter"))
	model := updated.(Model)
	if model.currentView != ViewDetail {
		t.Errorf("expected ViewDetail after enter, got %d", model.currentView)
	}
	// selectedIdx should point to the decode worker
	if model.workers[model.selectedIdx].Endpoint != "http://decode-1:8000" {
		t.Errorf("expected selectedIdx to point to decode-1, got %s", model.workers[model.selectedIdx].Endpoint)
	}
}

func TestMainViewTKeyOpensTopology(t *testing.T) {
	m := newTestModel()
	m.workers = testWorkers()

	updated, _ := m.Update(keyMsg("T"))
	model := updated.(Model)
	if model.currentView != ViewTopology {
		t.Errorf("expected ViewTopology after T, got %d", model.currentView)
	}
}

func TestTopologyViewRendersWithoutPanic(t *testing.T) {
	m := newTestModel()
	m.workers = []*metrics.WorkerMetrics{
		{Endpoint: "http://prefill-1:8000", Label: "prefill-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, KVCacheUsagePct: 90},
		{Endpoint: "http://decode-1:8000", Label: "decode-1", Backend: metrics.BackendVLLM, ModelName: "llama-70b", Online: true, GenTokPerSec: 500},
	}
	m.topoGraph = BuildTopology(m.workers, nil)
	m.currentView = ViewTopology

	out := m.View()
	if out == "" {
		t.Error("expected non-empty topology view output")
	}
}
