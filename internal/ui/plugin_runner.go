package ui

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/pkg/plugins"
)

// pluginDoneMsg is sent when a background plugin command completes.
type pluginDoneMsg struct {
	name   string
	stdout string
	stderr string
	err    error
}

// runPluginForeground returns a tea.ExecProcess command that suspends the TUI,
// runs the plugin command, then resumes the TUI on exit.
func runPluginForeground(name string, command string, args []string) tea.Cmd {
	c := exec.Command(command, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return pluginDoneMsg{name: name, err: err}
	})
}

// runPluginBackground returns a tea.Cmd that runs the plugin in a goroutine
// and sends a pluginDoneMsg when complete.
func runPluginBackground(name string, command string, args []string) tea.Cmd {
	return func() tea.Msg {
		var stdout, stderr bytes.Buffer
		c := exec.Command(command, args...)
		c.Stdout = &stdout
		c.Stderr = &stderr
		err := c.Run()
		return pluginDoneMsg{
			name:   name,
			stdout: stdout.String(),
			stderr: stderr.String(),
			err:    err,
		}
	}
}

// workerEnv builds the environment variable map for a selected worker.
func workerEnv(w *metrics.WorkerMetrics, k8sContext string) map[string]string {
	env := map[string]string{
		"$ENDPOINT":     w.Endpoint,
		"$BACKEND":      string(w.Backend),
		"$MODEL":        w.ModelName,
		"$WORKER_LABEL": w.Label,
		"$CONTEXT":      k8sContext,
	}

	// Extract K8s metadata from the endpoint.
	// K8s-discovered workers use endpoints like "k8s://namespace/pod-name?ip=10.0.0.1&port=8000".
	if trimmed, ok := strings.CutPrefix(w.Endpoint, "k8s://"); ok {
		// Split off query string first.
		path := trimmed
		var query url.Values
		if qIdx := strings.Index(trimmed, "?"); qIdx >= 0 {
			path = trimmed[:qIdx]
			query, _ = url.ParseQuery(trimmed[qIdx+1:])
		}
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 {
			env["$NAMESPACE"] = parts[0]
			env["$POD_NAME"] = parts[1]
		}
		// Build a real HTTP endpoint from pod IP + port for curl-based plugins.
		if query != nil {
			if ip := query.Get("ip"); ip != "" {
				port := query.Get("port")
				if port == "" {
					port = "8000"
				}
				env["$ENDPOINT"] = "http://" + ip + ":" + port
				env["$PORT"] = port
			}
		}
	}

	// Parse label for pod name if not already set from endpoint.
	// Dynamo labels use "role:pod-name" format; strip the role prefix.
	if env["$POD_NAME"] == "" && w.Label != "" {
		label := w.Label
		if colonIdx := strings.Index(label, ":"); colonIdx >= 0 {
			label = label[colonIdx+1:]
		}
		env["$POD_NAME"] = label
	}

	// Fill empty values with placeholders so args don't have bare $VAR tokens.
	for _, key := range []string{"$NAMESPACE", "$POD_NAME", "$NODE_NAME"} {
		if env[key] == "" {
			env[key] = ""
		}
	}

	return env
}

// gpuEnv builds the environment variable map for a selected GPU.
func gpuEnv(g *metrics.GPUInfo, k8sContext string) map[string]string {
	return map[string]string{
		"$POD_NAME":  g.Pod,
		"$NAMESPACE": g.Namespace,
		"$NODE_NAME": g.Hostname,
		"$CONTEXT":   k8sContext,
		"$ENDPOINT":  "",
		"$BACKEND":   "",
		"$MODEL":     "",
	}
}

// modelEnv builds the environment variable map for a selected model group.
func modelEnv(mg metrics.ModelGroup, k8sContext string) map[string]string {
	return map[string]string{
		"$MODEL":   mg.ModelName,
		"$BACKEND": string(mg.Backend),
		"$CONTEXT": k8sContext,
	}
}

// resolvePluginCmd interpolates args and returns the resolved command string
// for display in confirmation dialogs.
func resolvePluginCmd(p plugins.Plugin, env map[string]string) string {
	args := plugins.InterpolateArgs(p.Args, env)
	return fmt.Sprintf("%s %s", p.Command, strings.Join(args, " "))
}

// viewScope maps a View enum to the plugin scope string.
func viewScope(v View) string {
	switch v {
	case ViewMain, ViewDetail:
		return "worker"
	case ViewGPU, ViewGPUDetail:
		return "gpu"
	case ViewModelGroup:
		return "model"
	default:
		return ""
	}
}
