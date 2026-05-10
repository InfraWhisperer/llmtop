// Black-box contract tests for V12 (Sim-as-First-Class Demo).
// Spec: docs/feature-spec-v2.md §3 V12, §7 glossary.
//
// Pinned contracts:
//   - sim.Simulator has SwitchScenario(name string) method.
//   - The method satisfies the ScenarioSwitcher functional shape used by ui.Model
//     (i.e., a func(string) callback can wrap *sim.Simulator.SwitchScenario without
//     adapter code).
//
// Behavior tests for simTime=0 reset, InitWorker re-run, and -race safety require
// a constructor and field access the public spec does not document. They are
// flagged in the test-engineer report rather than written here.

package sim_test

import (
	"reflect"
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/sim"
)

// scenarioSwitcher mirrors the interface declared in app.Options per spec §3 V12.
// We re-declare it locally to avoid an import on internal/app from this _test pkg.
type scenarioSwitcher interface {
	SwitchScenario(name string)
}

// TestSimulator_HasSwitchScenarioMethod_TypeAssertion proves *sim.Simulator
// satisfies the documented method-set without depending on internal field layout.
// We use reflect to introspect the declared method: a *sim.Simulator value
// must have a method named "SwitchScenario" whose first non-receiver argument
// is a string.
func TestSimulator_HasSwitchScenarioMethod(t *testing.T) {
	ptrType := reflect.TypeOf((*sim.Simulator)(nil))
	m, ok := ptrType.MethodByName("SwitchScenario")
	if !ok {
		t.Fatalf("*sim.Simulator does not have method SwitchScenario")
	}
	// Method type's signature: func(*Simulator, string).
	if m.Type.NumIn() != 2 {
		t.Fatalf("SwitchScenario should accept 1 arg (plus receiver), got NumIn=%d", m.Type.NumIn())
	}
	if m.Type.In(1).Kind() != reflect.String {
		t.Fatalf("SwitchScenario arg 0 should be string, got %v", m.Type.In(1).Kind())
	}
	if m.Type.NumOut() != 0 {
		t.Errorf("SwitchScenario should return nothing, got %d return values", m.Type.NumOut())
	}
}

// TestSimulator_SatisfiesScenarioSwitcherInterface — *sim.Simulator must satisfy
// the documented ScenarioSwitcher interface so app/ui layers can wire it up.
func TestSimulator_SatisfiesScenarioSwitcherInterface(t *testing.T) {
	var _ scenarioSwitcher = (*sim.Simulator)(nil)
}
