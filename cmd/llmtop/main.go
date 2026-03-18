// llmtop - htop for LLM inference clusters
// Real-time terminal dashboard for vLLM, SGLang, and LMCache workers.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/InfraWhisperer/llmtop/internal/app"
	"github.com/InfraWhisperer/llmtop/internal/collector"
	"github.com/InfraWhisperer/llmtop/internal/discovery"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/pkg/config"
)

func init() {
	// Suppress ALL klog output to prevent client-go REST errors from corrupting
	// the bubbletea alt screen. The flag-based approach (logtostderr, stderrthreshold)
	// does not work with client-go v0.33+ because it uses klog v2's structured
	// logging API (klog.FromContext(ctx).Error(...)) which bypasses flag controls.
	//
	// Belt and suspenders:
	// 1. Replace klog's entire logging backend with a no-op logr.Logger.
	//    This catches structured logging calls (logger.Error, logger.Info).
	// 2. Redirect klog's legacy text output to io.Discard.
	//    This catches any remaining fmt-style klog.Errorf/klog.Warningf calls.
	klog.SetLogger(logr.Discard())
	klog.SetOutput(io.Discard)
}

// k8sFlags holds Kubernetes-related CLI flags.
type k8sFlags struct {
	kubeconfig    string
	namespace     string
	selector      string
	allNamespaces bool
	maxConcurrent int
	noK8s         bool
}

// version is set via -ldflags at build time.
var version = "0.1.0"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		endpoints    []string
		configFile   string
		intervalSec  int
		once         bool
		outputFmt    string
		dcgmEndpoint string
		kf           k8sFlags
	)

	cmd := &cobra.Command{
		Use:   "llmtop",
		Short: "htop for your LLM inference cluster",
		Long: `llmtop — real-time terminal dashboard for vLLM, SGLang, LMCache, and NIM.

Monitor GPU cache utilization, request queues, TTFT latency, and token
throughput across your entire inference fleet in a single glorious view.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(endpoints, configFile, intervalSec, once, outputFmt, dcgmEndpoint, kf)
		},
	}

	cmd.Flags().StringArrayVarP(&endpoints, "endpoint", "e", nil, "Endpoint URL to monitor (repeatable)")
	cmd.Flags().StringVarP(&configFile, "config", "c", "", "Path to config YAML file")
	cmd.Flags().IntVarP(&intervalSec, "interval", "i", 2, "Refresh interval in seconds")
	cmd.Flags().BoolVar(&once, "once", false, "Run once and exit (non-interactive)")
	cmd.Flags().StringVar(&outputFmt, "output", "table", "Output format for --once mode: table, json")
	cmd.Flags().StringVar(&dcgmEndpoint, "dcgm-endpoint", "", "DCGM exporter URL for GPU metrics")

	// Kubernetes discovery flags
	cmd.Flags().StringVar(&kf.kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	cmd.Flags().StringVarP(&kf.namespace, "namespace", "n", "", "Kubernetes namespace for pod discovery")
	cmd.Flags().StringVarP(&kf.selector, "selector", "l", "", "Label selector for pod discovery")
	cmd.Flags().BoolVarP(&kf.allNamespaces, "all-namespaces", "A", false, "Discover across all namespaces")
	cmd.Flags().IntVar(&kf.maxConcurrent, "max-concurrent", 10, "Max concurrent K8s API proxy requests")
	cmd.Flags().BoolVar(&kf.noK8s, "no-k8s", false, "Disable Kubernetes discovery")

	cmd.AddCommand(versionCmd())

	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("llmtop %s\n", version)
		},
	}
}

func run(endpoints []string, configFile string, intervalSec int, once bool, outputFmt string, dcgmEndpoint string, kf k8sFlags) error {
	ctx := context.Background()
	interval := time.Duration(intervalSec) * time.Second

	// Build worker configs
	var workerConfigs []collector.WorkerConfig
	var cfg *config.Config
	var dcgmFetchFuncs []func(ctx context.Context) (string, error)

	if configFile != "" {
		var err error
		cfg, err = config.LoadFile(configFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if cfg.Interval > 0 && intervalSec == 2 {
			intervalSec = cfg.Interval
			interval = time.Duration(intervalSec) * time.Second
		}
		// Config-level DCGM endpoint; CLI flag overrides.
		if dcgmEndpoint == "" && cfg.DCGMEndpoint != "" {
			dcgmEndpoint = cfg.DCGMEndpoint
		}
		for _, ep := range cfg.Endpoints {
			backend := parseBackend(ep.Backend)
			workerConfigs = append(workerConfigs, collector.WorkerConfig{
				Endpoint:    strings.TrimRight(ep.URL, "/"),
				Backend:     backend,
				Label:       ep.Label,
				MetricsPath: ep.MetricsPath,
			})
		}
	}

	for _, ep := range endpoints {
		workerConfigs = append(workerConfigs, collector.WorkerConfig{
			Endpoint: strings.TrimRight(ep, "/"),
			Backend:  metrics.BackendUnknown,
		})
	}

	// Kubernetes discovery: attempt if no static endpoints and not disabled
	var k8sContext string
	var k8sDisc *discovery.KubernetesDiscoverer
	if !kf.noK8s && len(workerConfigs) == 0 {
		kubecfg := kf.kubeconfig
		if kubecfg == "" {
			kubecfg = os.Getenv("KUBECONFIG")
		}

		ns := kf.namespace
		if ns == "" && cfg != nil {
			ns = cfg.Kubernetes.Namespace
		}
		if kf.allNamespaces {
			ns = ""
		}

		sel := kf.selector
		if sel == "" && cfg != nil {
			sel = cfg.Kubernetes.LabelSelector
		}

		mc := kf.maxConcurrent
		if cfg != nil && cfg.Kubernetes.MaxConcurrent > 0 {
			mc = cfg.Kubernetes.MaxConcurrent
		}

		disc, err := discovery.NewKubernetesDiscoverer(kubecfg, ns, sel, mc, 10*time.Second)
		if err == nil {
			k8sDisc = disc
			k8sContext = disc.ContextName()

			// Run pod discovery and DCGM discovery concurrently to cut
			// startup latency — each involves K8s API calls that take
			// 100-500ms per request.
			type podResult struct {
				pods []discovery.DiscoveredPod
				err  error
			}
			type dcgmResult struct {
				funcs []func(ctx context.Context) (string, error)
				err   error
			}
			podCh := make(chan podResult, 1)
			dcgmCh := make(chan dcgmResult, 1)

			go func() {
				pods, discErr := disc.DiscoverPods(ctx)
				podCh <- podResult{pods, discErr}
			}()

			wantDCGM := dcgmEndpoint == "" && (cfg == nil || cfg.DCGMEndpoint == "")
			if wantDCGM {
				go func() {
					funcs, dcgmErr := disc.DiscoverDCGMPods(ctx)
					dcgmCh <- dcgmResult{funcs, dcgmErr}
				}()
			}

			pr := <-podCh
			if pr.err == nil && len(pr.pods) > 0 {
				workerConfigs = append(workerConfigs, app.TargetsToWorkerConfigs(disc.ToTargets(pr.pods))...)
			}

			if wantDCGM {
				dr := <-dcgmCh
				if dr.err == nil {
					dcgmEndpoint = "k8s://dcgm-exporter"
					dcgmFetchFuncs = dr.funcs
				}
			}
		}
	}

	// Fall back to localhost discovery if still no endpoints
	if len(workerConfigs) == 0 {
		fmt.Fprintln(os.Stderr, "No endpoints specified, auto-discovering localhost...")
		discovered := discovery.DiscoverLocal(ctx)
		if len(discovered) == 0 {
			fmt.Fprintln(os.Stderr, "No LLM workers found. Use --endpoint, --config, or --kubeconfig to specify endpoints.")
			fmt.Fprintln(os.Stderr, "Checked ports: 8000, 8001, 8002, 8003, 8080, 8081, 8090")
		}
		workerConfigs = append(workerConfigs, app.TargetsToWorkerConfigs(discovered)...)
	}

	return app.New(app.Options{
		WorkerConfigs:  workerConfigs,
		Interval:       interval,
		DCGMEndpoint:   dcgmEndpoint,
		DCGMFetchFuncs: dcgmFetchFuncs,
		K8sContext:     k8sContext,
		Discoverer:     k8sDisc,
		Once:           once,
		OutputFormat:   outputFmt,
		Version:        version,
	}).Run(ctx)
}

func parseBackend(s string) metrics.Backend {
	switch strings.ToLower(s) {
	case "vllm":
		return metrics.BackendVLLM
	case "sglang":
		return metrics.BackendSGLang
	case "lmcache":
		return metrics.BackendLMCache
	case "nim":
		return metrics.BackendNIM
	case "tgi":
		return metrics.BackendTGI
	case "trtllm", "tensorrt-llm":
		return metrics.BackendTRTLLM
	case "triton":
		return metrics.BackendTriton
	default:
		return metrics.BackendUnknown
	}
}
