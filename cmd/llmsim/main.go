// llmsim — realistic simulation harness for llmtop.
// Runs a fake inference cluster that speaks every protocol llmtop scrapes.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/InfraWhisperer/llmtop/internal/sim"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	cfg := sim.DefaultConfig()
	var durationStr string
	var tickRateStr string
	var quiet bool

	cmd := &cobra.Command{
		Use:   "llmsim",
		Short: "Simulation harness for llmtop — fake inference cluster",
		Long: `llmsim runs a simulated LLM inference cluster that speaks every protocol
llmtop scrapes: vLLM /metrics, DCGM exporter, and Kubernetes API.

Use it as an end-to-end test target or demo recording backend.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if durationStr != "" {
				d, err := time.ParseDuration(durationStr)
				if err != nil {
					return fmt.Errorf("parse --duration: %w", err)
				}
				cfg.Duration = d
			}
			if tickRateStr != "" {
				tr, err := time.ParseDuration(tickRateStr)
				if err != nil {
					return fmt.Errorf("parse --tick-rate: %w", err)
				}
				cfg.TickRate = tr
			}

			s := sim.New(cfg)
			if err := s.Start(); err != nil {
				return fmt.Errorf("starting simulator: %w", err)
			}
			defer s.Stop()

			if !quiet {
				fmt.Println("llmsim ready")
				fmt.Printf("  K8s API:    http://localhost:%d\n", cfg.K8sPort)
				fmt.Printf("  DCGM:       http://localhost:%d\n", cfg.DCGMPort)
				fmt.Printf("  Workers:    http://localhost:%d … :%d\n", cfg.PortBase, cfg.PortBase+cfg.Prefill+cfg.Workers)
				fmt.Printf("  Scenario:   %s\n", cfg.Scenario)
				fmt.Printf("  Seed:       %d\n", cfg.Seed)
				fmt.Println()
				fmt.Println("Connect llmtop:")
				fmt.Printf("  llmtop --endpoint http://localhost:%d --dcgm-endpoint http://localhost:%d\n", cfg.PortBase, cfg.DCGMPort)
			}

			if cfg.Duration > 0 {
				time.Sleep(cfg.Duration)
				return nil
			}

			// Wait for signal
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			fmt.Println("\nshutting down")
			return nil
		},
	}

	cmd.Flags().StringVar(&cfg.Scenario, "scenario", cfg.Scenario, `Scenario: "steady", "demo", "stress", "chaos"`)
	cmd.Flags().IntVar(&cfg.Workers, "workers", cfg.Workers, "Number of decode workers")
	cmd.Flags().IntVar(&cfg.Prefill, "prefill", cfg.Prefill, "Number of prefill workers")
	cmd.Flags().StringVar(&tickRateStr, "tick-rate", "500ms", "Sim tick interval")
	cmd.Flags().IntVar(&cfg.PortBase, "port-base", cfg.PortBase, "First worker metrics port")
	cmd.Flags().IntVar(&cfg.DCGMPort, "dcgm-port", cfg.DCGMPort, "DCGM metrics port")
	cmd.Flags().IntVar(&cfg.K8sPort, "k8s-port", cfg.K8sPort, "Fake K8s API port")
	cmd.Flags().Int64Var(&cfg.Seed, "seed", cfg.Seed, "RNG seed (0 = random)")
	cmd.Flags().StringVar(&durationStr, "duration", "", "How long to run (e.g., 30s, 5m; default: forever)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress startup log lines")

	return cmd
}
