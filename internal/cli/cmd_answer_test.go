package cli_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/testutil"
)

func TestAnswerOnNoPendingGateIsIgnored(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	code, o, _ := runIn(t, root, nil, "answer", "--gate", "gate_review", "--choice", "revise", "--slug", "demo")
	if code != 0 || o["ignored"] != true {
		t.Fatalf("%d %v", code, o)
	}
	testutil.Git(t, root, "status", "--porcelain")
}

// specCapFixture drives a run through three spec-review rounds, editing
// spec.md after each one so the receipt never answers at the hash `next`
// sees next. That leaves the gate unsatisfied with no verdict pending
// (Verdict "" — never "rework"), which is exactly the state where
// decideBrainstorm's rework-verdict question is skipped and the round cap
// applies instead: three gate_reviewed events for the spec gate, current
// hash unreviewed.
func specCapFixture(t *testing.T) (string, string) {
	t.Helper()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec v0\n")
	if c, _, e := runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	if c, _, e := runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"still missing something",` +
		`"findings":[{"severity":"blocking","file":"spec.md","line":1,"title":"gap","detail":"say more"}]}`}
	for _, v := range []string{"# spec v0\n", "# spec v1\n", "# spec v2\n"} {
		testutil.WriteFile(t, root, "docs/takt/demo/spec.md", v)
		if c, r, e := runIn(t, root, rework, "review", "spec", "--slug", "demo"); c != 0 || r["verdict"] != "rework" {
			t.Fatalf("review round at %q: %d %v %s", v, c, r, e)
		}
	}
	// One more edit, unreviewed: the receipt no longer answers at the
	// current hash and no revision was accepted, so the gate reads
	// unsatisfied with no verdict — the state the round cap is for.
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec v3\n")
	return root, bdir
}

func TestSpecReviewRoundCapAsksThenRetryReviewsAgain(t *testing.T) {
	t.Parallel()
	root, bdir := specCapFixture(t)
	_, o, _ := next(t, root, nil)
	if o["op"] != "ask" || o["gate"] != "gate_review_capped" {
		t.Fatalf("three review rounds without the gate closing must ask instead of reviewing a fourth time: %v", o)
	}
	ctxm, _ := o["context"].(map[string]any)
	if ctxm["gate"] != "spec" || ctxm["attempts"] != float64(3) {
		t.Fatalf("the question must name the gate and the round count: %v", ctxm)
	}
	if c, _, e := runIn(t, root, nil,
		"answer", "--gate", "gate_review_capped", "--choice", "retry", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == "gate_rounds_reset" && e.Data["gate"] == "spec" {
			found = true
		}
	}
	if !found {
		t.Fatal("retry must record a gate_rounds_reset event for the spec gate")
	}
	if _, o, _ = next(t, root, nil); o["op"] != "exec" {
		t.Fatalf("retry must reset the round count so the run reviews once more: %v", o)
	}
}

func TestSpecReviewRoundCapAcceptOverridesAndMovesOn(t *testing.T) {
	t.Parallel()
	root, bdir := specCapFixture(t)
	if _, o, _ := next(t, root, nil); o["op"] != "ask" || o["gate"] != "gate_review_capped" {
		t.Fatalf("%v", o)
	}
	if c, _, e := runIn(t, root, nil,
		"answer", "--gate", "gate_review_capped", "--choice", "accept", "--slug", "demo"); c == 0 {
		t.Fatal("accept without --reason must fail", e)
	}
	if c, _, e := runIn(
		t,
		root,
		nil,
		"answer",
		"--gate",
		"gate_review_capped",
		"--choice",
		"accept",
		"--reason",
		"known gap",
		"--slug",
		"demo",
	); c != 0 {
		t.Fatal(e)
	}
	// #29 fix round 1, finding 1b: the user declined to act on the capped
	// review's finding by overriding it, so overrideGate must carry it into
	// follow-ups.json — the same rule an approving pass follows. If the
	// carryFindings call were ever deleted from overrideGate, this run
	// would still move on to planning below; only this assertion would
	// catch the loss.
	got, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("the overridden finding must be carried, got %d follow-ups", len(got.Items))
	}
	if got.Items[0].Source != gate.SourceOverride || got.Items[0].Gate != gate.Spec {
		t.Fatalf("provenance must survive: %+v", got.Items[0])
	}
	if got.Items[0].Severity != "blocking" || got.Items[0].Title != "gap" {
		t.Fatalf("finding detail must survive: %+v", got.Items[0])
	}
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("accept must override the gate and move the run on to planning: %v", o)
	}
}

func TestSpecReviewRoundCapStopKeepsTheGateOpen(t *testing.T) {
	t.Parallel()
	root, _ := specCapFixture(t)
	if _, o, _ := next(t, root, nil); o["op"] != "ask" || o["gate"] != "gate_review_capped" {
		t.Fatalf("%v", o)
	}
	code, o, e := runIn(t, root, nil, "answer", "--gate", "gate_review_capped", "--choice", "stop", "--slug", "demo")
	if code != 0 || o["kept"] != true {
		t.Fatalf("stop must keep the gate open: %d %v %s", code, o, e)
	}
	if code, o, e = next(t, root, nil); code != 0 || o["op"] != "ask" || o["gate"] != "gate_review_capped" {
		t.Fatalf("the capped gate must still be pending: %d %v %s", code, o, e)
	}
}

// openReviewerInvalidGate opens a pending agent_invalid gate for the
// reviewer directly on the bundle's state — the shape decide.askAgentInvalid
// builds once the reviewer's unusable-reply cap is reached (two-layers
// design D14). Built directly rather than driven there through three
// rejected `record --agent reviewer` replies and `next`, because reaching
// the cap through cmd_next also dispatches the lens fan-out (Task 9), which
// this test has no need to exercise — only the answer's own effect on a
// pending gate whose context names the reviewer.
func openReviewerInvalidGate(t *testing.T, bdir string) {
	t.Helper()
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(op.Op{
		Op: op.Ask, Gate: "agent_invalid", Question: "q",
		Context: map[string]any{
			"slug": "demo", "agent": op.AgentReviewer, "attempts": 3, "problems": []string{"no fenced json block"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	st.PendingGate = &bundle.PendingGate{ID: "agent_invalid", OpenedAt: time.Now().UTC(), Payload: payload}
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
}

// TestAnswerAgentInvalidSkipRecordsInternalReviewSkipped covers the
// reviewer's skip answer (two-layers design §4.3, D14): the internal layer
// is advisory, so skipping at the cap records internal_review_skipped for
// the active dispatch rather than the auditor's alignment skip.
func TestAnswerAgentInvalidSkipRecordsInternalReviewSkipped(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	openReviewerInvalidGate(t, bdir)
	code, o, errb := runIn(t, root, nil, "answer", "--gate", "agent_invalid", "--choice", "skip", "--slug", "demo")
	if code != 0 || o["cleared"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	ev := lastEventOfType(t, bdir, "internal_review_skipped")
	if ev.Data["wave"] != 0.0 || ev.Data["slice"] != 1.0 || ev.Data["attempt"] != 1.0 {
		t.Fatalf("internal_review_skipped event must carry the active wave/slice/attempt: %+v", ev.Data)
	}
}

// TestAnswerAgentInvalidRetryResetsReviewerAttempts covers the reviewer's
// retry answer: it resets the reviewer's attempt streak through
// reviewer_attempts_reset, exactly as the auditor's and the assessor's do
// through their own reset events.
func TestAnswerAgentInvalidRetryResetsReviewerAttempts(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	openReviewerInvalidGate(t, bdir)
	code, o, errb := runIn(t, root, nil, "answer", "--gate", "agent_invalid", "--choice", "retry", "--slug", "demo")
	if code != 0 || o["cleared"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if !hasEventOfType(t, bdir, "reviewer_attempts_reset") {
		t.Fatal("retry must append a reviewer_attempts_reset event")
	}
}
