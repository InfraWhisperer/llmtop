// Package app owns the llmtop application lifecycle: collector creation,
// start/stop sequencing, reconcile loop, and TUI/once-mode dispatch.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/InfraWhisperer/llmtop/internal/collector"
	"github.com/InfraWhisperer/llmtop/internal/discovery"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/internal/ui"
)

// Options holds the resolved configuration for an App instance.
type Options struct {
	WorkerConfigs    []collector.WorkerConfig
	Interval         time.Duration
	DCGMEndpoint     string
	DCGMFetchFuncs   []func(ctx context.Context) (string, error)
	K8sContext       string
	Discoverer       *discovery.KubernetesDiscoverer // nil if no K8s
	Once             bool
	OutputFormat     string
	Version          string
	AlertThresholds  metrics.AlertThresholds
	AlertsCustomized bool

	// V12: --demo flag wires DemoMode=true and a ScenarioSwitcher closure
	// that calls into the in-process simulator. ScenarioSwitcher is nil when
	// not in demo mode; the TUI's S key handler must guard against nil.
	DemoMode         bool
	ScenarioSwitcher func(name string)
}

// App owns the llmtop application lifecycle.
type App struct {
	opts      Options
	collector collector.MetricsSource
	gpu       collector.GPUSource
}

// New creates an App from resolved options.
func New(opts Options) *App {
	return &App{opts: opts}
}

// Run starts the application. It creates collectors, starts polling,
// and either runs once or starts the TUI.
func (a *App) Run(ctx context.Context) error {
	// Create worker metrics collector
	c := collector.New(a.opts.WorkerConfigs, a.opts.Interval)
	a.collector = c

	// Create DCGM GPU collector if configured
	var dc *collector.DCGMCollector
	if len(a.opts.DCGMFetchFuncs) > 0 {
		dc = collector.NewDCGMCollectorWithFetchFuncs(
			a.opts.DCGMEndpoint,
			a.opts.Interval,
			a.opts.DCGMFetchFuncs,
		)
	} else if a.opts.DCGMEndpoint != "" {
		dc = collector.NewDCGMCollector(
			strings.TrimRight(a.opts.DCGMEndpoint, "/"),
			a.opts.Interval,
		)
	}

	// Nil-interface guard: a nil *DCGMCollector wrapped in an interface
	// is non-nil. Assign explicitly to get a true nil interface value.
	if dc != nil {
		a.gpu = dc
	}

	if a.opts.Once {
		return a.runOnce(ctx)
	}
	return a.runTUI(ctx)
}

func (a *App) runOnce(ctx context.Context) error {
	a.collector.PollNow(ctx)
	workers := a.collector.GetAll()
	summary := metrics.ComputeFleetSummary(workers)

	if a.gpu != nil {
		a.gpu.PollNow(ctx)
	}

	switch a.opts.OutputFormat {
	case "json":
		modelGroups := metrics.GroupWorkersByModel(workers)
		envelope := struct {
			Summary     metrics.FleetSummary    `json:"summary"`
			Workers     []*metrics.WorkerMetrics `json:"workers"`
			ModelGroups []metrics.ModelGroup     `json:"model_groups,omitempty"`
			GPUSummary  *metrics.GPUSummary      `json:"gpu_summary,omitempty"`
			GPUs        []*metrics.GPUInfo       `json:"gpus,omitempty"`
		}{Summary: summary, Workers: workers, ModelGroups: modelGroups}

		if a.gpu != nil {
			gpus := a.gpu.GetAll()
			if len(gpus) > 0 {
				gpuSummary := a.gpu.GetSummary()
				envelope.GPUSummary = &gpuSummary
				envelope.GPUs = gpus
			}
		}

		data, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	default:
		printTable(workers, summary, a.opts.Version)
	}
	return nil
}

func (a *App) runTUI(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.collector.Start(ctx)
	defer a.collector.Stop()

	if a.gpu != nil {
		a.gpu.Start(ctx)
		defer a.gpu.Stop()
	}

	intervalSec := int(a.opts.Interval.Seconds())

	// Periodic re-discovery: reconcile pods every 15 seconds so that
	// not-ready pods get removed and newly-ready pods get added.
	if a.opts.Discoverer != nil {
		go reconcileLoop(ctx, a.opts.Discoverer, a.collector, 15*time.Second)
	}

	thresholds := a.opts.AlertThresholds
	if thresholds == (metrics.AlertThresholds{}) {
		thresholds = metrics.DefaultAlertThresholds()
	}
	// V12 seam: forward DemoMode and ScenarioSwitcher to ui.NewModel. When
	// --demo is set, ScenarioSwitcher is the closure that calls into
	// sim.Simulator.SwitchScenario; nil otherwise.
	model := ui.NewModel(a.collector, a.gpu, a.opts.Version, intervalSec, a.opts.K8sContext, thresholds, a.opts.DemoMode, a.opts.ScenarioSwitcher)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}

// reconcileLoop periodically re-discovers pods and reconciles the collector's
// worker set. Pods that are no longer Running+Ready are removed; new pods are
// added. This prevents scraping not-ready pods that would produce errors and
// clutter the TUI.
func reconcileLoop(ctx context.Context, disc *discovery.KubernetesDiscoverer, c collector.MetricsSource, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pods, err := disc.DiscoverPods(ctx)
			if err != nil {
				continue
			}

			// Build the desired set of endpoints from freshly discovered pods
			desired := make(map[string]struct{}, len(pods))
			targets := disc.ToTargets(pods)
			for _, t := range targets {
				desired[t.Endpoint] = struct{}{}
			}

			// Remove workers that are no longer in the desired set
			current := c.Endpoints()
			for ep := range current {
				if !strings.HasPrefix(ep, "k8s://") {
					continue // only reconcile K8s-discovered workers
				}
				if _, ok := desired[ep]; !ok {
					c.RemoveWorker(ep)
				}
			}

			// Add newly discovered workers
			for _, t := range targets {
				c.AddWorker(TargetToWorkerConfig(t))
			}
		}
	}
}

// TargetToWorkerConfig converts a discovery Target to a collector WorkerConfig.
func TargetToWorkerConfig(t discovery.Target) collector.WorkerConfig {
	return collector.WorkerConfig{
		Endpoint:    t.Endpoint,
		Label:       t.Label,
		Backend:     t.Backend,
		MetricsPath: t.MetricsPath,
		Role:        t.Role,
		NodeName:    t.NodeName,
		FetchFunc:   t.FetchFunc,
	}
}

// TargetsToWorkerConfigs converts a slice of Targets to WorkerConfigs.
func TargetsToWorkerConfigs(targets []discovery.Target) []collector.WorkerConfig {
	configs := make([]collector.WorkerConfig, len(targets))
	for i, t := range targets {
		configs[i] = TargetToWorkerConfig(t)
	}
	return configs
}

func printTable(workers []*metrics.WorkerMetrics, summary metrics.FleetSummary, version string) {
	fmt.Printf("\nllmtop %s  ·  %d workers (%d online)  ·  %.0f tok/s  ·  cache hit %.0f%%  ·  P99 TTFT %.0fms\n\n",
		version, summary.TotalWorkers, summary.OnlineWorkers,
		summary.TotalTokPerSec, summary.AvgCacheHit, summary.P99TTFT)

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ROLE\tENDPOINT\tBACKEND\tNODE\tMODEL\tKV%\tQUEUE\tRUN\tTTFT P99\tITL P99\tXFER P99\tHIT%\tTOK/S")
	_, _ = fmt.Fprintln(w, strings.Repeat("\u2500", 120))
	for _, worker := range workers {
		status := "\u25cf"
		if !worker.Online {
			status = "\u25cb"
		}
		var ep string
		if strings.HasPrefix(worker.Endpoint, "k8s://") {
			ep = worker.Label
			if ep == "" {
				ep = strings.TrimPrefix(worker.Endpoint, "k8s://")
			}
		} else {
			ep = strings.TrimPrefix(worker.Endpoint, "http://")
			if worker.Label != "" {
				ep = ep + " (" + worker.Label + ")"
			}
		}
		model := worker.ModelName
		if model == "" {
			model = "\u2014"
		}
		role := worker.Role
		if role == "" {
			role = "mono"
		}
		node := truncateField(worker.NodeName, 15)
		if node == "" {
			node = "\u2014"
		}
		xfer := "\u2014"
		if worker.KVTransferP99Ms > 0 {
			xfer = fmt.Sprintf("%.0fms", worker.KVTransferP99Ms)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s %s\t%s\t%s\t%s\t%.0f%%\t%d\t%d\t%.0fms\t%.0fms\t%s\t%.0f%%\t%.0f\n",
			role, status, ep,
			string(worker.Backend),
			node,
			model,
			worker.KVCacheUsagePct,
			worker.RequestsWaiting,
			worker.RequestsRunning,
			worker.TTFT_P99,
			worker.ITL_P99,
			xfer,
			worker.CacheHitRatePct,
			worker.GenTokPerSec+worker.PromptTokPerSec,
		)
	}
	_ = w.Flush()
}

// truncateField clips a string to at most n runes, appending an ellipsis when truncated.
func truncateField(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "\u2026"
}
