package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/testutil"
)

// armedSpecGate walks a run to the point where `takt review spec` is the
// next thing it needs: spec.md and goals.md are on disk and both steps are
// recorded, so the spec gate is armed at a hash the review will bind to.
func armedSpecGate(t *testing.T) (string, string) {
	t.Helper()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")
	return root, bdir
}

// refuseEventAppends makes the run's event log read-only and returns the
// function that puts it back. Reads still succeed, so a review pass runs the
// whole way to its gate_reviewed append and only that write is refused —
// the same seam TestRecordGoalsReportsALostStreakResetAppend uses. Root
// writes a mode-444 file regardless, so the failure cannot be provoked there.
func refuseEventAppends(t *testing.T, bdir string) func() {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("this user can write a mode-444 file, so the refused append cannot be provoked")
	}
	p := bundle.EventsPath(bdir)
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	restore := func() { _ = os.Chmod(p, 0o600) }
	t.Cleanup(restore)
	return restore
}

// specReceipt returns the spec gate's receipt, failing the test unless one
// exists and answers at the gate's current hash.
func specReceipt(t *testing.T, bdir string) gate.Receipt {
	t.Helper()
	h, _, err := gate.Hash(gate.Spec, bdir)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := gate.ReadReceipt(bdir, gate.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil {
		t.Fatal("no gates/spec.json on disk")
	}
	if rc.Hash != h {
		t.Fatalf("receipt hash %q, want the gate's current %q", rc.Hash, h)
	}
	return *rc
}

// noSpecReceipt fails the test unless gates/spec.json is not there at all.
func noSpecReceipt(t *testing.T, bdir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(bdir, "gates", "spec.json")); !os.IsNotExist(err) {
		rc, _ := gate.ReadReceipt(bdir, gate.Spec)
		t.Fatalf("gates/spec.json must not exist: %v %+v", err, rc)
	}
}

// followUpCount returns how many findings the run has carried forward.
func followUpCount(t *testing.T, bdir string) int {
	t.Helper()
	f, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	return len(f.Items)
}

// approveWithAFinding is a reviewer's answer that closes the gate and still
// leaves one minor nobody was asked to act on — so the pass has a carry to
// lose if the write order is wrong.
const approveWithAFinding = `{"verdict":"approve","summary":"looks fine",` +
	`"findings":[{"severity":"minor","file":"spec.md","line":7,"title":"wording","detail":"ambiguous"}]}`

// TestReviewFailureBeforeTheReceiptLeavesNoReceipt is the first half of the
// write-order guarantee: any failure BEFORE the receipt — the findings, the
// carry, the gate_reviewed event — must leave no receipt, so cachedReceipt
// cannot answer and the next `takt review` re-runs the pass instead of
// returning a cached verdict with the carry lost.
//
// The refused event append is the failure this provokes, because it is the
// one that used to be unchecked and used to come after the receipt. The
// re-run then proves the retry costs nothing: gate.AppendFollowUps keys a
// follow-up by its identity, so carrying the same finding a second time
// leaves follow-ups.json exactly as one carry did.
func TestReviewFailureBeforeTheReceiptLeavesNoReceipt(t *testing.T) {
	t.Parallel()
	root, bdir := armedSpecGate(t)
	env := map[string]string{"TAKT_FAKE_REVIEW": approveWithAFinding}

	restore := refuseEventAppends(t, bdir)
	if c, _, e := runIn(t, root, env, "review", "spec", "--slug", "demo"); c != 1 {
		t.Fatalf("a refused gate_reviewed append must fail the command: exit %d %s", c, e)
	}
	noSpecReceipt(t, bdir)
	if n := followUpCount(t, bdir); n != 1 {
		t.Fatalf("the carry ran before the event, so it is on disk: %d follow-ups", n)
	}

	restore()
	c, r, e := runIn(t, root, env, "review", "spec", "--slug", "demo")
	if c != 0 || r["verdict"] != "approve" {
		t.Fatalf("%d %v %s", c, r, e)
	}
	if r["cached"] != nil {
		t.Fatalf("no receipt answered, so the pass must have re-run: %v", r)
	}
	if rc := specReceipt(t, bdir); rc.Verdict != gate.VerdictApprove {
		t.Fatalf("the re-run's receipt = %+v", rc)
	}
	if n := followUpCount(t, bdir); n != 1 {
		t.Fatalf("the retried carry is idempotent: want 1 follow-up, got %d", n)
	}
	if n := countEventsOfType(t, bdir, gate.EvReviewed); n != 1 {
		t.Fatalf("the failed pass appended none, so the re-run's is the only one: %d", n)
	}
}

// TestForcedReviewFailureLeavesNoStaleReceipt extends the same guarantee to
// a forced pass. `takt review --force` retires the receipt that already
// answers before the backend is called, so a forced pass that fails before
// writing its own receipt leaves none rather than the prior one.
//
// Without that removal the run would be worse off than if the force had
// never been asked for: the old receipt would still be at the current hash,
// and the next unforced `takt review` would return that superseded verdict
// as cached instead of re-running the pass the user forced.
func TestForcedReviewFailureLeavesNoStaleReceipt(t *testing.T) {
	t.Parallel()
	root, bdir := armedSpecGate(t)
	approve := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"approve","summary":"fine","findings":[]}`}
	if c, r, e := runIn(t, root, approve, "review", "spec", "--slug", "demo"); c != 0 || r["verdict"] != "approve" {
		t.Fatalf("%d %v %s", c, r, e)
	}
	specReceipt(t, bdir)

	restore := refuseEventAppends(t, bdir)
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"needs work","findings":[]}`}
	if c, _, e := runIn(t, root, rework, "review", "spec", "--force", "--slug", "demo"); c != 1 {
		t.Fatalf("a refused gate_reviewed append must fail the forced pass: exit %d %s", c, e)
	}
	noSpecReceipt(t, bdir)

	restore()
	c, r, e := runIn(t, root, approve, "review", "spec", "--slug", "demo")
	if c != 0 || r["cached"] != nil {
		t.Fatalf("nothing answers at the hash, so an unforced review must re-run: %d %v %s", c, r, e)
	}
	if rc := specReceipt(t, bdir); rc.Verdict != gate.VerdictApprove {
		t.Fatalf("the re-run's receipt = %+v", rc)
	}
	if n := countEventsOfType(t, bdir, gate.EvReviewed); n != 2 {
		t.Fatalf("the first pass and the re-run; the failed forced pass appended none: %d", n)
	}
}

// TestReceiptSurvivesACommitFailure is the second half of the guarantee: a
// failure AT the commit, after the receipt, loses nothing. The receipt and
// everything before it are on disk, uncommitted; the next takt command that
// commits stages the whole bundle directory and sweeps them up, and in the
// meantime the next `takt review` correctly returns that receipt as cached
// — the receipt is the record of a review that really happened, and the
// cost of that review was already paid.
//
// A stranded .git/index.lock is the failure: git refuses to add while it is
// there, which is the same seam the doctor's index-lock tests rely on.
func TestReceiptSurvivesACommitFailure(t *testing.T) {
	t.Parallel()
	root, bdir := armedSpecGate(t)
	env := map[string]string{"TAKT_FAKE_REVIEW": approveWithAFinding}

	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if c, _, e := runIn(t, root, env, "review", "spec", "--slug", "demo"); c != 1 {
		t.Fatalf("a commit git will not take must fail the command: exit %d %s", c, e)
	}
	if rc := specReceipt(t, bdir); rc.Verdict != gate.VerdictApprove {
		t.Fatalf("the receipt is written before the commit: %+v", rc)
	}
	if n := countEventsOfType(t, bdir, gate.EvReviewed); n != 1 {
		t.Fatalf("the pass appended its event before the commit failed: %d", n)
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	c, r, e := runIn(t, root, env, "review", "spec", "--slug", "demo")
	if c != 0 || r["cached"] != true || r["verdict"] != "approve" {
		t.Fatalf("the uncommitted receipt is still a real review's record: %d %v %s", c, r, e)
	}
	if n := countEventsOfType(t, bdir, gate.EvReviewed); n != 1 {
		t.Fatalf("a cached review runs no pass and appends no event: %d", n)
	}

	// The approve satisfies the gate, so `next` moves brainstorm → plan and
	// commits the bundle — which is what picks the stranded receipt up.
	if code, o, errb := next(t, root, nil); code != 0 {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if s := testutil.Git(t, root, "status", "--porcelain", "--", "docs/takt/demo/gates"); s != "" {
		t.Fatalf("the next command's bundle commit must sweep the receipt up: %q", s)
	}
	if s := testutil.Git(t, root, "ls-tree", "HEAD", "docs/takt/demo/gates/spec.json"); s == "" {
		t.Fatal("gates/spec.json must be in the tree at HEAD")
	}
}

// TestErroredPassCarriesItsReasonAndOffersRetry follows the backend's own
// account of a failure all the way to the user: onto the receipt, onto the
// gate_reviewed event, into the gate_review question, and out again through
// the retry answer that re-runs the review (#43.2). An error verdict is not
// a reviewer's word, so it never satisfies the gate and never short-circuits
// the re-run; only the pass that really answers moves the run on.
func TestErroredPassCarriesItsReasonAndOffersRetry(t *testing.T) {
	t.Parallel()
	root, bdir := armedSpecGate(t)
	broken := map[string]string{"TAKT_FAKE_REVIEW": "not json at all"}
	if c, r, e := runIn(t, root, broken, "review", "spec", "--slug", "demo"); c != 0 || r["verdict"] != "error" {
		t.Fatalf("the fake backend must report an error verdict: %d %v %s", c, r, e)
	}
	rc := specReceipt(t, bdir)
	if rc.Verdict != gate.VerdictError || rc.Reason == "" {
		t.Fatalf("an errored pass must record why: %+v", rc)
	}
	if got := lastEventOfType(t, bdir, gate.EvReviewed).Data["reason"]; got != rc.Reason {
		t.Fatalf("the event's reason = %v, want the receipt's %q", got, rc.Reason)
	}

	code, o, errb := next(t, root, nil)
	if code != 0 || o["op"] != "ask" || o["gate"] != "gate_review" {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	ctx, _ := o["context"].(map[string]any)
	if ctx["reason"] != rc.Reason {
		t.Fatalf("the ask must carry the receipt's reason: %v", ctx)
	}
	q, _ := o["question"].(string)
	if !strings.Contains(q, rc.Reason) {
		t.Fatalf("question = %q, want it to state the reason", q)
	}

	code, res, errb := runIn(t, root, nil, "answer", "--gate", "gate_review", "--choice", "retry", "--slug", "demo")
	if code != 0 || res["cleared"] != true {
		t.Fatalf("%d %v %s", code, res, errb)
	}
	approve := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"approve","summary":"fine","findings":[]}`}
	c, r, e := runIn(t, root, approve, "review", "spec", "--slug", "demo")
	if c != 0 || r["verdict"] != "approve" || r["cached"] != nil {
		t.Fatalf("an error receipt never answers, so the retried review re-runs: %d %v %s", c, r, e)
	}
	if code, o, errb = next(t, root, nil); code != 0 || o["op"] != "dispatch" {
		t.Fatalf("the answered gate must move the run to planning: %d %v %s", code, o, errb)
	}
}

// TestFindingsFileCarriesTheGateHashAndRound pins the two fields that say
// which pass wrote reviews/<gate>.json (#43.3). Without them the findings
// file was a floating referent — the scoped confirming pass reads it, and
// nothing could tell a file written at this hash from one left over from an
// earlier one. The doctor's review-record check is what surfaces the drift;
// this is the writer's half.
func TestFindingsFileCarriesTheGateHashAndRound(t *testing.T) {
	t.Parallel()
	root, bdir := armedSpecGate(t)
	approve := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"approve","summary":"fine","findings":[]}`}
	if c, r, e := runIn(t, root, approve, "review", "spec", "--slug", "demo"); c != 0 || r["verdict"] != "approve" {
		t.Fatalf("%d %v %s", c, r, e)
	}
	rc := specReceipt(t, bdir)
	rec := specFindingsRecord(t, bdir)
	if rec["hash"] != rc.Hash {
		t.Fatalf("reviews/spec.json hash = %v, want the receipt's %q", rec["hash"], rc.Hash)
	}
	if rec["round"] != float64(1) {
		t.Fatalf("the first pass is round 1, got %v", rec["round"])
	}

	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"needs work","findings":[]}`}
	if c, r, e := runIn(t, root, rework, "review", "spec", "--force", "--slug", "demo"); c != 0 ||
		r["verdict"] != "rework" {
		t.Fatalf("%d %v %s", c, r, e)
	}
	forced := specReceipt(t, bdir)
	if forced.Verdict != gate.VerdictRework {
		t.Fatalf("the receipt must be the forced pass's: %+v", forced)
	}
	rec = specFindingsRecord(t, bdir)
	if rec["hash"] != forced.Hash {
		t.Fatalf("reviews/spec.json hash = %v, want %q", rec["hash"], forced.Hash)
	}
	if rec["round"] != float64(2) {
		t.Fatalf("a forced re-run is another round: %v", rec["round"])
	}
}

// specFindingsRecord decodes reviews/spec.json as the raw object on disk, so
// the test sees the keys a reader outside the package sees rather than the
// struct the writer used.
func specFindingsRecord(t *testing.T, bdir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bdir, "reviews", "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
