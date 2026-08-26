package cli_test

import (
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

// TestAutonomyStepConfirmsImplementerDispatch is the task-4 addendum: a
// wave's dispatch op carries confirm: true when the run's autonomy is step
// (spec §5.5) — set by launchWave/dispatchOp on the op itself, not read from
// config by the prompt.
func TestAutonomyStepConfirmsImplementerDispatch(t *testing.T) {
	t.Parallel()
	root, _ := executeRunWith(t, "--autonomy", "step")
	code, o, errb := next(t, root, nil)
	if code != 0 || o["op"] != "dispatch" || o["wave"] != float64(0) {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if o["confirm"] != true {
		t.Fatalf("wave dispatch under --autonomy step must carry confirm: true, got %v", o)
	}
}

// TestAutonomyAutoDispatchHasNoConfirmKey pins the other half: the default
// (auto) run's dispatch op has no confirm key at all — omitempty, not
// confirm: false.
func TestAutonomyAutoDispatchHasNoConfirmKey(t *testing.T) {
	t.Parallel()
	root, _ := executeRun(t)
	code, o, errb := next(t, root, nil)
	if code != 0 || o["op"] != "dispatch" {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if _, ok := o["confirm"]; ok {
		t.Fatalf("the default run's dispatch op must have no confirm key: %v", o)
	}
}

// TestAutonomyStepPlannerDispatchNeverConfirms pins the addendum's third
// case: the planner/auditor/assessor dispatch never carries confirm, even
// under --autonomy step — only a wave of implementers does.
func TestAutonomyStepPlannerDispatchNeverConfirms(t *testing.T) {
	t.Parallel()
	root, _ := setupRunWith(t, "--autonomy", "step")
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md",
		"# spec\n\n## Assumptions & Open Decisions\n| q | d | r | s |\n")
	if code, _, errb := runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	if code, _, errb := runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if code, _, errb := runIn(t, root, nil, "review", "spec", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}

	code, o, errb := next(t, root, nil)
	if code != 0 || o["op"] != "dispatch" {
		t.Fatalf("expected the planner dispatch: %d %v %s", code, o, errb)
	}
	agents, ok := o["agents"].([]any)
	if !ok || len(agents) != 1 || agents[0].(map[string]any)["agent"] != "planner" {
		t.Fatalf("expected the planner dispatch: %v", o)
	}
	if _, confirms := o["confirm"]; confirms {
		t.Fatalf("the planner dispatch must never carry confirm: %v", o)
	}
}
