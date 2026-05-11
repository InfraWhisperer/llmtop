// Black-box contract tests for V12 (Sim-as-First-Class Demo).
// Spec: docs/feature-spec-v2.md §3 V12, §7 glossary.
//
// Pinned contracts:
//   - sim.Simulator has SwitchScenario(name string) method.
//   - SwitchScenario("stress") swaps the active scenario without panic and
//     keeps the worker topology alive (at least one online worker survives
//     across the switch).
//   - SwitchScenario("nonexistent") falls back to "steady" (the documented
//     default) and leaves the simulator usable.
//   - *sim.Simulator satisfies the ScenarioSwitcher interface used by ui.Model.

package sim_test

import (
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/sim"
)

// scenarioSwitcher mirrors the interface declared in app.Options per spec §3 V12.
// We re-declare it locally to avoid an import on internal/app from this _test pkg.
type scenarioSwitcher interface {
	SwitchScenario(name string)
}

// TestSimulator_SwitchScenario_StressKeepsTopology exercises SwitchScenario
// end-to-end. Switching to "stress" and advancing 10 simulated seconds must
// keep at least one online worker. White-box assertions on KV>0.85 live in
// sim_test.go (same package); here we pin only the public-visible side
// effect of the switch.
func TestSimulator_SwitchScenario_StressKeepsTopology(t *testing.T) {
	cfg := sim.DefaultConfig()
	cfg.Seed = 1
	cfg.PortBase = 0
	cfg.DCGMPort = 0
	cfg.K8sPort = 0
	s := sim.New(cfg)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SwitchScenario(\"stress\")+AdvanceTo panicked: %v", r)
		}
	}()
	s.SwitchScenario("stress")
	s.AdvanceTo(10 * time.Second)

	workers := s.Workers()
	if len(workers) == 0 {
		t.Fatalf("SwitchScenario(\"stress\") removed all workers")
	}
	var anyOnline bool
	for _, w := range workers {
		if w.Online {
			anyOnline = true
			break
		}
	}
	if !anyOnline {
		t.Errorf("after SwitchScenario(\"stress\") no workers are Online")
	}
}

// TestSimulator_SwitchScenario_UnknownFallsBack: switching to a name that is
// not in GetScenario's table must not panic and must leave the simulator in a
// usable state (subsequent AdvanceTo must complete).
func TestSimulator_SwitchScenario_UnknownFallsBack(t *testing.T) {
	cfg := sim.DefaultConfig()
	cfg.Seed = 2
	cfg.PortBase = 0
	cfg.DCGMPort = 0
	cfg.K8sPort = 0
	s := sim.New(cfg)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SwitchScenario(\"does-not-exist\") panicked: %v", r)
		}
	}()
	s.SwitchScenario("does-not-exist")
	s.AdvanceTo(2 * time.Second)
}

// TestSimulator_SatisfiesScenarioSwitcherInterface pins that ui.Model and
// app.Options can wire a *sim.Simulator into a ScenarioSwitcher field
// without an adapter.
func TestSimulator_SatisfiesScenarioSwitcherInterface(t *testing.T) {
	var _ scenarioSwitcher = (*sim.Simulator)(nil)
}
