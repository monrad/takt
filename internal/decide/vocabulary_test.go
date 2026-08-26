package decide_test

import (
	"slices"
	"testing"

	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/op"
)

func TestVocabularyIsComplete(t *testing.T) {
	t.Parallel()
	v := decide.Vocab()
	for _, want := range []string{"owner", "gate_review", "alignment_confirm", "plan_invalid", "agent_invalid",
		"wave_failures", "review_error", "verification_failed", "no_verification", "goals_unmet",
		"branch_finish"} {
		if !slices.Contains(v.Gates, want) {
			t.Errorf("gate %s missing", want)
		}
	}
	if !slices.Equal(v.RunSteps, []string{"brainstorm", "goals", "retro", "push_pr"}) {
		t.Errorf("run steps %v", v.RunSteps)
	}
	if !slices.Equal(v.ExecCommands, []string{"review", "close-wave", "verify"}) {
		t.Errorf("exec commands %v", v.ExecCommands)
	}
	if !slices.Equal(v.StopReasons, []string{"wave_in_flight", "archived"}) {
		t.Errorf("stop reasons %v", v.StopReasons)
	}
	if !slices.Equal(op.Kinds(), []op.Kind{op.Dispatch, op.Ask, op.Run, op.Exec, op.Stop}) {
		t.Errorf("kinds %v", op.Kinds())
	}
}

// Every gate in the vocabulary renders a question with at least one option
// and its answer command; an unknown gate falls to the default filler.
func TestQuestionCoversEveryGate(t *testing.T) {
	t.Parallel()
	for _, g := range decide.Vocab().Gates {
		q := decide.Question(
			g,
			map[string]any{"slug": "demo", "adopted": false, "merge_allowed": true, "discard_allowed": true},
		)
		if q.Gate != g || len(q.Options) == 0 || q.Answer == "" || q.Question == "" {
			t.Errorf("gate %s renders %+v", g, q)
		}
	}
}
