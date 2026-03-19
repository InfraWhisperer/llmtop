// Package plugins implements a k9s-style plugin system for llmtop.
// Plugins are user-defined shell commands triggered by keyboard shortcuts,
// scoped to specific views (worker, gpu, model), with environment variable
// interpolation from the currently selected resource.
package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Plugin defines an external command that can be triggered via a keyboard
// shortcut from within the TUI.
type Plugin struct {
	Name        string   `yaml:"-"`
	ShortCut    string   `yaml:"shortCut"`
	Description string   `yaml:"description"`
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args"`
	Scopes      []string `yaml:"scopes"`
	Background  bool     `yaml:"background"`
	Confirm     bool     `yaml:"confirm"`
	Dangerous   bool     `yaml:"dangerous"`
}

// pluginFile is the top-level YAML structure for plugin config files.
type pluginFile struct {
	Plugins map[string]Plugin `yaml:"plugins"`
}

// builtinShortcuts lists every shortcut the TUI already handles.
// Checked during validation to prevent conflicts.
var builtinShortcuts = map[string]bool{
	"q": true, "ctrl+c": true,
	"up": true, "down": true, "k": true, "j": true,
	"s": true, "f": true, "d": true, "r": true, "e": true,
	"g": true, "m": true, "?": true, "enter": true,
}

// validScopes limits the scope values a plugin may declare.
var validScopes = map[string]bool{
	"worker": true,
	"gpu":    true,
	"model":  true,
}

// Load reads plugins from the standard config locations:
//  1. ~/.config/llmtop/plugins.yaml
//  2. ~/.config/llmtop/plugins/*.yaml (alphabetical order)
//
// Plugins defined later override earlier ones with the same name.
func Load() ([]Plugin, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("plugins: resolving home dir: %w", err)
	}

	base := filepath.Join(home, ".config", "llmtop")
	var all []Plugin

	// Single file
	single := filepath.Join(base, "plugins.yaml")
	if plugins, loadErr := loadFile(single); loadErr == nil {
		all = append(all, plugins...)
	}

	// Directory of files
	dir := filepath.Join(base, "plugins")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		if plugins, loadErr := loadFile(filepath.Join(dir, e.Name())); loadErr == nil {
			all = append(all, plugins...)
		}
	}

	return all, nil
}

// LoadFile loads plugins from a single YAML file. Exposed for testing.
func LoadFile(path string) ([]Plugin, error) {
	return loadFile(path)
}

func loadFile(path string) ([]Plugin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse parses plugin definitions from raw YAML bytes.
func Parse(data []byte) ([]Plugin, error) {
	var pf pluginFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("plugins: parse error: %w", err)
	}

	plugins := make([]Plugin, 0, len(pf.Plugins))
	for name, p := range pf.Plugins {
		p.Name = name
		plugins = append(plugins, p)
	}
	return plugins, nil
}

// Validate checks that a set of plugins have valid scopes and don't collide
// with built-in shortcuts. Returns all validation errors encountered.
func Validate(plugins []Plugin) []error {
	var errs []error
	seen := make(map[string]string) // shortcut → plugin name

	for _, p := range plugins {
		key := normalizeShortcut(p.ShortCut)

		if p.ShortCut == "" {
			errs = append(errs, fmt.Errorf("plugin %q: missing shortCut", p.Name))
			continue
		}
		if p.Command == "" {
			errs = append(errs, fmt.Errorf("plugin %q: missing command", p.Name))
		}
		if len(p.Scopes) == 0 {
			errs = append(errs, fmt.Errorf("plugin %q: at least one scope required", p.Name))
		}
		for _, scope := range p.Scopes {
			if !validScopes[scope] {
				errs = append(errs, fmt.Errorf("plugin %q: invalid scope %q (valid: worker, gpu, model)", p.Name, scope))
			}
		}
		if builtinShortcuts[key] {
			errs = append(errs, fmt.Errorf("plugin %q: shortcut %q conflicts with built-in", p.Name, p.ShortCut))
		}
		if prev, dup := seen[key]; dup {
			errs = append(errs, fmt.Errorf("plugin %q: shortcut %q already used by plugin %q", p.Name, p.ShortCut, prev))
		}
		seen[key] = p.Name
	}
	return errs
}

// InterpolateArgs replaces $VARIABLE placeholders in plugin args with values
// from the provided environment map. Unknown variables are left as-is.
func InterpolateArgs(args []string, env map[string]string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		result := arg
		for k, v := range env {
			result = strings.ReplaceAll(result, k, v)
		}
		out[i] = result
	}
	return out
}

// ForScope returns the subset of plugins whose scopes include the given scope.
func ForScope(plugins []Plugin, scope string) []Plugin {
	var matched []Plugin
	for _, p := range plugins {
		if slices.Contains(p.Scopes, scope) {
			matched = append(matched, p)
		}
	}
	return matched
}

// normalizeShortcut converts shortcut names to bubbletea's key string format.
// Bubbletea represents shift+letter as the uppercase letter itself (e.g.
// Shift+R → "R"), not "shift+r". Ctrl combos stay as "ctrl+x".
func normalizeShortcut(s string) string {
	s = strings.ReplaceAll(s, "-", "+")
	lower := strings.ToLower(s)

	// "shift+x" → "X" (bubbletea sends uppercase letter for shift combos)
	if after, ok := strings.CutPrefix(lower, "shift+"); ok && len(after) == 1 {
		return strings.ToUpper(after)
	}

	return lower
}

// ShortcutKey returns the bubbletea-compatible key string for a plugin.
func ShortcutKey(p Plugin) string {
	return normalizeShortcut(p.ShortCut)
}

func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}
