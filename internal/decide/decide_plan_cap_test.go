package decide_test

// The plan gate's round cap, mirroring the two spec-gate precedents this
// package already holds — TestSpecReviewRoundsAreCapped (decide_test.go:1067)
// and TestPendingReworkVerdictOutranksTheRoundCap (decide_test.go:1115). They
// live in their own file rather than beside those precedents so that
// decide_test.go stays byte for byte what it was: the spec gate's own capped
// behaviour is unchanged by this branch, and an unmodified file is the only
// complete proof of that.

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/decide"
)

// TestPlanReviewRoundsAreCapped is row 9's cap: with the plan index present
// and valid, the plan gate unsatisfied and no verdict to answer, decidePlan
// reviews while the round count is under maxAgentAttempts and asks
// gate_review_capped once it reaches it.
func TestPlanReviewRoundsAreCapped(t *testing.T) {
	t.Parallel()
	base := func() (*bundle.State, decide.Facts) {
		return state(bundle.PhasePlan), decide.Facts{HasIndex: true, IndexValid: true}
	}
	st, f := base()
	f.PlanRounds = 2
	d, err := decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActExec || !strings.HasPrefix(d.Op.Command, "takt review plan") {
		t.Fatalf("under the cap the run must still review: %+v", d)
	}

	st, f = base()
	f.PlanRounds = 3
	d, err = decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActAsk || d.Op.Gate != "gate_review_capped" {
		t.Fatalf("at the cap the run must ask instead of reviewing a fourth time: %+v", d)
	}
	if d.Op.Context["attempts"] != 3 || d.Op.Context["gate"] != "plan" {
		t.Fatalf("the question must name the gate and the round count: %+v", d.Op.Context)
	}
	var choices []string
	for _, o := range d.Op.Options {
		choices = append(choices, o.Choice)
	}
	if len(choices) != 3 {
		t.Fatalf("choices = %v, want accept/retry/stop", choices)
	}
}

// TestPendingPlanReworkVerdictOutranksTheRoundCap pins the load-bearing order
// in decidePlan: a rework verdict waiting to be answered must win even when
// the round count has also reached the cap. Immediately after a third
// consecutive rework verdict with no intervening edit, both conditions are
// true at once — needsRework(f.PlanGate) and f.PlanRounds >= maxAgentAttempts
// — and the user must still be shown gate_review (there is a verdict to
// answer), never gate_review_capped. If the two checks in decidePlan were
// ever swapped, this test would fail where TestPlanReviewRoundsAreCapped
// could not: that test never sets f.PlanGate.Verdict, so needsRework is false
// throughout and it cannot tell the checks apart.
func TestPendingPlanReworkVerdictOutranksTheRoundCap(t *testing.T) {
	t.Parallel()
	st := state(bundle.PhasePlan)
	f := decide.Facts{HasIndex: true, IndexValid: true}
	f.PlanGate = decide.GateStatus{Satisfied: false, Verdict: "rework"}
	f.PlanRounds = 3
	d, err := decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActAsk || d.Op.Gate != "gate_review" {
		t.Fatalf("a verdict waiting to be answered must outrank the round cap: %+v", d)
	}
}
