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
