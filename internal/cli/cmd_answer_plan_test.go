package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/testutil"
)

// planCapFixture mirrors specCapFixture (cmd_answer_test.go:37) one phase
// later: it drives a run through brainstorm, goals and an approved spec
// review into the plan phase, has the planner write a valid plan, and then
// takes three plan-review rounds, editing plan.md after each one so the
// receipt never answers at the hash `next` sees next. That leaves the plan
// gate unsatisfied with no verdict pending (Verdict "" — never "rework"),
// which is exactly the state where the round cap applies: three
// gate_reviewed events for the plan gate, current hash unreviewed.
func planCapFixture(t *testing.T) (string, string) {
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
	// review spec with a nil env: the fake reviewer approves, closing the
	// spec gate with an approve receipt.
	if c, r, e := runIn(t, root, nil, "review", "spec", "--slug", "demo"); c != 0 || r["verdict"] != "approve" {
		t.Fatalf("%d %v %s", c, r, e)
	}
	// next dispatches the planner, committing the brainstorm → plan
	// transition as a side effect.
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("expected dispatch planner, got %v", o)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan v0\n")
	specH := specHash(t, bdir)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", strings.Replace(validIndex, "%s", specH, 1))
	if c, r, e := runIn(
		t, root, nil, "record", "--agent", "planner", "--from", "/dev/null", "--slug", "demo",
	); c != 0 || r["valid"] != true {
		t.Fatalf("%d %v %s", c, r, e)
	}
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"still missing something",` +
		`"findings":[{"severity":"blocking","file":"plan.md","line":1,"title":"gap","detail":"say more"}]}`}
	for _, v := range []string{"# plan v0\n", "# plan v1\n", "# plan v2\n"} {
		testutil.WriteFile(t, root, "docs/takt/demo/plan.md", v)
		if c, r, e := runIn(t, root, rework, "review", "plan", "--slug", "demo"); c != 0 || r["verdict"] != "rework" {
			t.Fatalf("review round at %q: %d %v %s", v, c, r, e)
		}
	}
	// One more edit, unreviewed: the receipt no longer answers at the
	// current hash and no revision was accepted, so the gate reads
	// unsatisfied with no verdict — the state the round cap is for.
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan v3\n")
	return root, bdir
}

// requireCappedPlanAsk asserts o is the capped *plan* gate's question, whole:
// op ask on gate_review_capped, with a context naming the plan gate and the
// three rounds already spent. The context is what makes the question
// answerable, and op and gate alone do not pin it — a question raised for
// the spec gate, or one that miscounted the rounds, would carry the same op
// and the same gate name — so every test in this family checks the whole
// shape before it answers, not just the two outer fields.
func requireCappedPlanAsk(t *testing.T, o map[string]any) {
	t.Helper()
	if o["op"] != "ask" || o["gate"] != "gate_review_capped" {
		t.Fatalf("three review rounds without the gate closing must ask instead of reviewing a fourth time: %v", o)
	}
	ctxm, _ := o["context"].(map[string]any)
	if ctxm["gate"] != "plan" || ctxm["attempts"] != float64(3) {
		t.Fatalf("the question must name the gate and the round count: %v", ctxm)
	}
}

func TestPlanReviewRoundCapAsksThenRetryReviewsAgain(t *testing.T) {
	t.Parallel()
	root, bdir := planCapFixture(t)
	_, o, _ := next(t, root, nil)
	requireCappedPlanAsk(t, o)
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
		if e.Type == "gate_rounds_reset" && e.Data["gate"] == "plan" {
			found = true
		}
	}
	if !found {
		t.Fatal("retry must record a gate_rounds_reset event for the plan gate")
	}
	if _, o, _ = next(t, root, nil); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt review plan") {
		t.Fatalf("retry must reset the round count so the run reviews once more: %v", o)
	}
}

// requirePlanOverrideEvent asserts the log holds a gate_overridden event
// for the plan gate carrying want as its hash — the hash the plan artifacts
// have right now, so the override answers the version the user actually saw
// rather than some earlier one.
func requirePlanOverrideEvent(t *testing.T, bdir, want string) {
	t.Helper()
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type != "gate_overridden" || e.Data["gate"] != "plan" {
			continue
		}
		if e.Data["hash"] != want {
			t.Fatalf("the override event must carry the current plan hash: %+v", e)
		}
		found = true
	}
	if !found {
		t.Fatal("accept must record a gate_overridden event for the plan gate")
	}
}

// requireCarriedPlanFinding asserts the plan gate's reviewed finding reached
// follow-ups.json whole. #29 fix round 1, finding 1b (mirrored for the plan
// gate): the user declined to act on the capped review's finding by
// overriding it, so overrideGate must carry it forward — the same rule an
// approving pass follows. Provenance alone would not say that: a carry that
// replaced the finding with a placeholder would keep the right Gate and
// Source, so the severity, location, title and detail the three rework
// rounds reported are checked too. That is the parity check
// TestSpecReviewRoundCapAcceptOverridesAndMovesOn (cmd_answer_test.go:138)
// makes for the spec gate, widened to the whole finding.
func requireCarriedPlanFinding(t *testing.T, bdir string) {
	t.Helper()
	got, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("the overridden finding must be carried, got %d follow-ups", len(got.Items))
	}
	it := got.Items[0]
	if it.Source != gate.SourceOverride || it.Gate != gate.Plan {
		t.Fatalf("provenance must survive: %+v", it)
	}
	if it.Severity != "blocking" || it.File != "plan.md" || it.Line != 1 ||
		it.Title != "gap" || it.Detail != "say more" {
		t.Fatalf("the reviewed finding itself must survive the override, not a placeholder: %+v", it)
	}
}

// requireAlignmentAuditDispatch asserts o is the alignment audit — spec §5's
// after-accept row. "Some dispatch happened" is not that claim:
// re-dispatching the planner is also a dispatch, and would mean the override
// sent the run backwards instead of on, so the agent and its mode are named.
func requireAlignmentAuditDispatch(t *testing.T, o map[string]any) {
	t.Helper()
	if o["op"] != "dispatch" {
		t.Fatalf("accept must override the gate and move the run on to the alignment audit: %v", o)
	}
	agents, _ := o["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("the alignment audit is a one-agent dispatch: %v", o)
	}
	ag, _ := agents[0].(map[string]any)
	if ag["agent"] != "alignment-auditor" || ag["mode"] != "clauses" {
		t.Fatalf("accept must move the run on to the alignment audit, not some other dispatch: %v", o)
	}
}

func TestPlanReviewRoundCapAcceptOverridesAndMovesOn(t *testing.T) {
	t.Parallel()
	root, bdir := planCapFixture(t)
	_, ask, _ := next(t, root, nil)
	requireCappedPlanAsk(t, ask)
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
	h, _, err := gate.Hash(gate.Plan, bdir)
	if err != nil {
		t.Fatal(err)
	}
	requirePlanOverrideEvent(t, bdir, h)
	requireCarriedPlanFinding(t, bdir)
	_, o, _ := next(t, root, nil)
	requireAlignmentAuditDispatch(t, o)
}

// snapshotGateState captures gates/plan.json and events.jsonl byte for
// byte, so a caller can prove an answer left them untouched rather than
// merely that the gate re-asks — a stop that silently rewrote the receipt
// or appended an event would otherwise pass unnoticed even though the gate
// stayed open. Both files must exist: planCapFixture drives three
// `review plan` rounds, and recordReviewed writes the receipt after every
// pass whatever the verdict, so a missing file is itself a failure worth
// reporting rather than a case to tolerate.
func snapshotGateState(t *testing.T, bdir string) ([]byte, []byte) {
	t.Helper()
	planReceipt, err := os.ReadFile(filepath.Join(bdir, "gates", "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := os.ReadFile(bundle.EventsPath(bdir))
	if err != nil {
		t.Fatal(err)
	}
	return planReceipt, events
}

func TestPlanReviewRoundCapStopKeepsTheGateOpen(t *testing.T) {
	t.Parallel()
	root, bdir := planCapFixture(t)
	_, ask, _ := next(t, root, nil)
	requireCappedPlanAsk(t, ask)
	beforeReceipt, beforeEvents := snapshotGateState(t, bdir)
	code, o, e := runIn(t, root, nil, "answer", "--gate", "gate_review_capped", "--choice", "stop", "--slug", "demo")
	if code != 0 || o["kept"] != true {
		t.Fatalf("stop must keep the gate open: %d %v %s", code, o, e)
	}
	afterReceipt, afterEvents := snapshotGateState(t, bdir)
	if !bytes.Equal(beforeReceipt, afterReceipt) {
		t.Fatal("stop must not rewrite the plan gate's receipt")
	}
	if !bytes.Equal(beforeEvents, afterEvents) {
		t.Fatal("stop must not append any event")
	}
	if code, o, e = next(t, root, nil); code != 0 {
		t.Fatalf("the capped gate must still be pending: %d %v %s", code, o, e)
	}
	requireCappedPlanAsk(t, o)
}

// TestPlanReviewRoundCapAnswersLeaveTheSpecGateAlone is the negative half of
// this family (G7): the spec gate closed earlier in planCapFixture with its
// own approve receipt and its own round count, and answering the capped
// plan gate must not disturb either — the two receipts and the two round
// counts stay independent.
func TestPlanReviewRoundCapAnswersLeaveTheSpecGateAlone(t *testing.T) {
	t.Parallel()
	root, bdir := planCapFixture(t)
	_, ask, _ := next(t, root, nil)
	requireCappedPlanAsk(t, ask)
	specReceiptBefore, err := os.ReadFile(filepath.Join(bdir, "gates", "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	n := gate.Rounds(events, gate.Spec)
	if c, _, e := runIn(t, root, nil,
		"answer", "--gate", "gate_review_capped", "--choice", "retry", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}
	events, err = bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if (e.Type == "gate_rounds_reset" || e.Type == "gate_overridden") && e.Data["gate"] == "spec" {
			t.Fatalf("a plan-gate answer must not touch the spec gate: %+v", e)
		}
	}
	specReceiptAfter, err := os.ReadFile(filepath.Join(bdir, "gates", "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(specReceiptBefore, specReceiptAfter) {
		t.Fatal("the spec gate's receipt must not change when the plan gate is answered")
	}
	if got := gate.Rounds(events, gate.Spec); got != n {
		t.Fatalf("the spec gate's round count must not change: got %d want %d", got, n)
	}
}
