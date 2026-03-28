package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePluginYAML(t *testing.T) {
	yaml := []byte(`
plugins:
  tail-logs:
    shortCut: ctrl-l
    description: Tail worker logs
    scopes: [worker]
    command: kubectl
    args: ["logs", "-f", "$POD_NAME", "-n", "$NAMESPACE"]
    background: false
  restart-worker:
    shortCut: shift-r
    description: Restart worker pod
    scopes: [worker]
    command: kubectl
    args: ["delete", "pod", "$POD_NAME"]
    confirm: true
    dangerous: true
`)

	plugins, err := Parse(yaml)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}

	byName := make(map[string]Plugin)
	for _, p := range plugins {
		byName[p.Name] = p
	}

	tail, ok := byName["tail-logs"]
	if !ok {
		t.Fatal("missing tail-logs plugin")
	}
	if tail.ShortCut != "ctrl-l" {
		t.Errorf("tail-logs shortcut: got %q, want %q", tail.ShortCut, "ctrl-l")
	}
	if tail.Command != "kubectl" {
		t.Errorf("tail-logs command: got %q, want %q", tail.Command, "kubectl")
	}
	if len(tail.Scopes) != 1 || tail.Scopes[0] != "worker" {
		t.Errorf("tail-logs scopes: got %v, want [worker]", tail.Scopes)
	}
	if tail.Dangerous {
		t.Error("tail-logs should not be dangerous")
	}

	restart := byName["restart-worker"]
	if !restart.Confirm {
		t.Error("restart-worker should require confirmation")
	}
	if !restart.Dangerous {
		t.Error("restart-worker should be dangerous")
	}
}

func TestInterpolateArgs(t *testing.T) {
	args := []string{
		"logs", "-f", "$POD_NAME", "-n", "$NAMESPACE",
		"--context", "$CONTEXT",
	}
	env := map[string]string{
		"$POD_NAME":  "vllm-worker-0",
		"$NAMESPACE": "inference",
		"$CONTEXT":   "prod-cluster",
	}

	got := InterpolateArgs(args, env)
	want := []string{
		"logs", "-f", "vllm-worker-0", "-n", "inference",
		"--context", "prod-cluster",
	}

	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestInterpolateArgsEmbedded(t *testing.T) {
	args := []string{
		"-d", `{"model":"$MODEL","prompt":"Hello"}`,
	}
	env := map[string]string{
		"$MODEL": "llama-70b",
	}

	got := InterpolateArgs(args, env)
	if got[1] != `{"model":"llama-70b","prompt":"Hello"}` {
		t.Errorf("embedded interpolation failed: got %q", got[1])
	}
}

func TestInterpolateArgsMissingVar(t *testing.T) {
	args := []string{"$POD_NAME", "$UNKNOWN"}
	env := map[string]string{
		"$POD_NAME": "worker-0",
	}

	got := InterpolateArgs(args, env)
	if got[0] != "worker-0" {
		t.Errorf("arg[0]: got %q, want %q", got[0], "worker-0")
	}
	if got[1] != "$UNKNOWN" {
		t.Errorf("arg[1]: got %q, want %q (should be left as-is)", got[1], "$UNKNOWN")
	}
}

func TestValidateBuiltinConflict(t *testing.T) {
	plugins := []Plugin{
		{Name: "bad", ShortCut: "s", Command: "echo", Scopes: []string{"worker"}},
	}
	errs := Validate(plugins)
	if len(errs) == 0 {
		t.Fatal("expected validation error for built-in shortcut conflict")
	}
	found := false
	for _, e := range errs {
		if contains(e.Error(), "conflicts with built-in") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'conflicts with built-in' error, got: %v", errs)
	}
}

func TestValidateDuplicateShortcut(t *testing.T) {
	plugins := []Plugin{
		{Name: "a", ShortCut: "ctrl-l", Command: "echo", Scopes: []string{"worker"}},
		{Name: "b", ShortCut: "ctrl-l", Command: "echo", Scopes: []string{"worker"}},
	}
	errs := Validate(plugins)
	found := false
	for _, e := range errs {
		if contains(e.Error(), "already used by") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate shortcut error, got: %v", errs)
	}
}

func TestValidateMissingFields(t *testing.T) {
	plugins := []Plugin{
		{Name: "no-shortcut", Command: "echo", Scopes: []string{"worker"}},
		{Name: "no-command", ShortCut: "ctrl-x", Scopes: []string{"worker"}},
		{Name: "no-scope", ShortCut: "ctrl-y", Command: "echo"},
		{Name: "bad-scope", ShortCut: "ctrl-z", Command: "echo", Scopes: []string{"invalid"}},
	}
	errs := Validate(plugins)
	if len(errs) < 4 {
		t.Errorf("expected at least 4 validation errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateValidPlugins(t *testing.T) {
	plugins := []Plugin{
		{Name: "a", ShortCut: "ctrl-l", Command: "kubectl", Scopes: []string{"worker"}},
		{Name: "b", ShortCut: "shift-r", Command: "curl", Scopes: []string{"worker", "gpu"}},
	}
	errs := Validate(plugins)
	if len(errs) != 0 {
		t.Errorf("expected no validation errors, got: %v", errs)
	}
}

func TestForScope(t *testing.T) {
	all := []Plugin{
		{Name: "a", Scopes: []string{"worker"}},
		{Name: "b", Scopes: []string{"gpu"}},
		{Name: "c", Scopes: []string{"worker", "gpu"}},
		{Name: "d", Scopes: []string{"model"}},
	}

	workers := ForScope(all, "worker")
	if len(workers) != 2 {
		t.Errorf("worker scope: expected 2, got %d", len(workers))
	}
	gpus := ForScope(all, "gpu")
	if len(gpus) != 2 {
		t.Errorf("gpu scope: expected 2, got %d", len(gpus))
	}
	models := ForScope(all, "model")
	if len(models) != 1 {
		t.Errorf("model scope: expected 1, got %d", len(models))
	}
}

func TestShortcutKeyNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ctrl-l", "ctrl+l"},
		{"shift-r", "R"},
		{"Ctrl-L", "ctrl+l"},
		{"SHIFT-T", "T"},
		{"shift-x", "X"},
		{"ctrl-m", "enter"},
		{"ctrl-h", "backspace"},
		{"ctrl-i", "tab"},
		{"ctrl-j", "enter"},
	}
	for _, tt := range tests {
		got := ShortcutKey(Plugin{ShortCut: tt.input})
		if got != tt.want {
			t.Errorf("ShortcutKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-plugins.yaml")
	data := []byte(`
plugins:
  test:
    shortCut: ctrl-t
    description: Test plugin
    scopes: [worker]
    command: echo
    args: ["hello"]
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].Name != "test" {
		t.Errorf("expected name 'test', got %q", plugins[0].Name)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := LoadFile("/nonexistent/path.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
