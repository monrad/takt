package op_test

import (
	"slices"
	"testing"

	"github.com/monrad/takt/internal/op"
)

func TestStepsAreTheFourRunStepsInLoopOrder(t *testing.T) {
	t.Parallel()
	want := []string{"brainstorm", "goals", "retro", "push_pr"}
	if got := op.Steps(); !slices.Equal(got, want) {
		t.Fatalf("Steps() = %v, want %v", got, want)
	}
	if op.StepBrainstorm != "brainstorm" || op.StepGoals != "goals" || op.StepRetro != "retro" ||
		op.StepPushPR != "push_pr" {
		t.Fatal("step constants drifted from their JSON spellings")
	}
}

// TestAgentsAreDistinct is the steps test's counterpart: the names are the
// wire format of `takt record --agent` and of a dispatch op's agent field,
// so they must stay exactly what the hosts and the agent definitions spell,
// and two agents sharing one name would make `record` ambiguous.
func TestAgentsAreDistinct(t *testing.T) {
	t.Parallel()
	want := []string{"planner", "alignment-auditor", "goal-assessor", "implementer"}
	if got := op.Agents(); !slices.Equal(got, want) {
		t.Fatalf("Agents() = %v, want %v", got, want)
	}
	if op.AgentPlanner != "planner" || op.AgentAlignmentAuditor != "alignment-auditor" ||
		op.AgentGoalAssessor != "goal-assessor" || op.AgentImplementer != "implementer" {
		t.Fatal("agent constants drifted from their JSON spellings")
	}
	seen := make(map[string]bool, len(want))
	for _, a := range op.Agents() {
		if a == "" || seen[a] {
			t.Fatalf("agent name %q is empty or repeated: %v", a, op.Agents())
		}
		seen[a] = true
	}
}
