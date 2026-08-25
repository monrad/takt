package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/testutil"
)

// adoptedPRURL is the pull request the scripted session reports opening for
// the adopted-branch run.
const adoptedPRURL = "https://example.invalid/pr/1"

// finishedRun drives a run to the branch_finish question.
func finishedRun(t *testing.T, initFlags ...string) (*driver, string, map[string]any) {
	t.Helper()
	d, bdir := finishRun(t, append([]string{"--no-goals"}, initFlags...)...)
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp()
	testutil.WriteFile(t, d.root, "docs/takt/demo/retro.md", "# Retro\n\nok\n")
	d.cmd("done", "--step", "retro", "--slug", "demo")
	o := d.nextOp()
	if o["op"] != "ask" || o["gate"] != "branch_finish" {
		t.Fatalf("%v", o)
	}
	return d, bdir, o
}

// optionsByChoice indexes an ask op's options by their choice.
func optionsByChoice(o map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, x := range o["options"].([]any) {
		m := x.(map[string]any)
		out[m["choice"].(string)] = m
	}
	return out
}

// TestBranchFinishInPlainCheckoutDisablesMergeAndHandsOff is the ordinary
// single-worktree run: the primary worktree is the one holding the run
// branch, so takt cannot merge from it and says so; keep archives the run.
func TestBranchFinishInPlainCheckoutDisablesMergeAndHandsOff(t *testing.T) {
	t.Parallel()
	d, bdir, o := finishedRun(t)
	opts := optionsByChoice(o)
	if opts["merge"]["disabled"] == nil || !strings.Contains(opts["merge"]["disabled"].(string), "takt/demo") {
		t.Fatalf("merge must be disabled with the reason (primary worktree is on the run branch): %v", opts["merge"])
	}
	if code, _, _ := d.cmd("answer", "--gate", "branch_finish", "--choice", "merge", "--slug", "demo"); code == 0 {
		t.Fatal("a disabled choice is refused")
	}
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "keep", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	o = d.nextOp()
	if o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	st, _ := bundle.LoadState(bdir)
	if st.Phase != bundle.PhaseArchived || st.Session != nil || st.Disposition == nil || !st.Disposition.Applied {
		t.Fatalf("%+v", st)
	}
	if s := testutil.Git(t, d.root, "log", "-1", "--format=%s"); s != "takt(demo): archive" {
		t.Fatalf("archive commit: %s", s)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf("tree not clean: %q", s)
	}
	if o = d.nextOp(); o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("row 26: %v", o)
	}
	// --json because runIn insists stdout is a JSON document; the plain
	// renderer is exercised by cmd_status_test.go.
	if code, got, _ := d.cmd("status", "--json", "--slug", "demo"); code != 0 || got["phase"] != "archived" {
		t.Fatalf("status still works on an archived run: %d %v", code, got)
	}
}

// linkedWorktreeRun builds the layout merge exists for — the primary
// worktree left clean on the base branch, the run driven from a linked
// worktree on takt/demo — and drives it to the branch_finish question. It
// returns the driver, the primary's path and that question.
func linkedWorktreeRun(t *testing.T) (*driver, string, map[string]any) {
	t.Helper()
	root, _ := setupRunWith(t, "--no-goals")
	testutil.Git(t, root, "checkout", "main")
	linked := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, root, "worktree", "add", linked, "takt/demo")
	d := &driver{
		t: t, root: linked, bdir: filepath.Join(linked, "docs", "takt", "demo"),
		env: map[string]string{"TAKT_SESSION": "S"},
	}
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp()
	testutil.WriteFile(t, linked, "docs/takt/demo/retro.md", "# Retro\n")
	d.cmd("done", "--step", "retro", "--slug", "demo")
	o := d.nextOp()
	if o["op"] != "ask" || o["gate"] != "branch_finish" {
		t.Fatalf("%v", o)
	}
	return d, root, o
}

// TestBranchFinishMergeInLinkedWorktree is the worktree layout merge exists
// for: the run is driven from a linked worktree on the run branch while the
// primary sits clean on the base, so takt can merge there without ever
// moving this worktree's HEAD.
func TestBranchFinishMergeInLinkedWorktree(t *testing.T) {
	t.Parallel()
	d, root, o := linkedWorktreeRun(t)
	if opts := optionsByChoice(o); opts["merge"]["disabled"] != nil {
		t.Fatalf("merge is available when the primary is on main and clean: %v", opts["merge"])
	}
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "merge", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	o = d.nextOp()
	if o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	if s := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.HasPrefix(s, "Merge takt/demo") {
		t.Fatalf("primary HEAD is the merge commit: %s", s)
	}
	if !strings.Contains(testutil.Git(t, root, "log", "--format=%s", "-5"), "takt(demo): archive") {
		t.Fatal("the merge carries the archive commit")
	}
	if got, _ := o["context"].(map[string]any); got["merged"] != testutil.Git(t, root, "rev-parse", "HEAD") {
		t.Fatalf("the stop op names the merge commit: %v", o["context"])
	}
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) != 1 || !strings.Contains(cleanup[0].(string), "git branch -d takt/demo") {
		t.Fatalf("the branch is still checked out in the linked worktree, so deletion is handed off: %v", cleanup)
	}
	if testutil.Git(t, root, "status", "--porcelain") != "" {
		t.Fatal("primary must stay clean")
	}
	st, _ := bundle.LoadState(d.bdir)
	if st.Disposition == nil || !st.Disposition.Applied {
		t.Fatalf("the merge landed, so the disposition is applied: %+v", st.Disposition)
	}
	// Nothing is owed any more, so a further call is the plain row-26 stop —
	// it does not re-run the disposition or repeat its hand-off.
	before := testutil.Git(t, root, "rev-parse", "HEAD")
	if o = d.nextOp(); o["op"] != "stop" || o["reason"] != "archived" || o["cleanup"] != nil {
		t.Fatalf("an applied disposition is not applied again: %v", o)
	}
	if testutil.Git(t, root, "rev-parse", "HEAD") != before {
		t.Fatal("the primary must not take a second merge commit")
	}
}

// TestArchivedRunReappliesAnUnappliedMerge is the crash this ordering exists
// to survive: the archive commits, its merge then fails, and `applied` stays
// false — so the run is archived, the merge is still owed, and the next
// `takt next` finishes it rather than declaring a merge nobody made.
func TestArchivedRunReappliesAnUnappliedMerge(t *testing.T) {
	t.Parallel()
	d, root, _ := linkedWorktreeRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "merge", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	// The primary goes unmergeable between the answer and the archive: an
	// untracked a.go is exactly what the merge would have to overwrite.
	testutil.WriteFile(t, root, "a.go", "package a // not what the branch has\n")
	o := d.nextOp()
	if o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	if ctx, _ := o["context"].(map[string]any); ctx["error"] == nil || ctx["merged"] != nil {
		t.Fatalf("a failed merge is reported and claims nothing: %v", o["context"])
	}
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) != 1 || !strings.Contains(cleanup[0].(string), "merge --no-ff takt/demo") {
		t.Fatalf("the merge is handed to the session meanwhile: %v", cleanup)
	}
	st, _ := bundle.LoadState(d.bdir)
	if st.Phase != bundle.PhaseArchived || st.Disposition.Applied {
		t.Fatalf("a merge that did not happen is not applied: %+v", st.Disposition)
	}
	if s := testutil.Git(t, root, "log", "-1", "--format=%s"); strings.HasPrefix(s, "Merge") {
		t.Fatalf("nothing was merged: %s", s)
	}
	// The obstacle goes away; the next call owes the merge and makes it.
	if err := os.Remove(filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}
	o = d.nextOp()
	if ctx, _ := o["context"].(map[string]any); o["op"] != "stop" || ctx["merged"] == nil {
		t.Fatalf("the re-applied merge is reported: %v", o)
	}
	if s := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.HasPrefix(s, "Merge takt/demo") {
		t.Fatalf("the merge lands on the retry: %s", s)
	}
	if st, _ = bundle.LoadState(d.bdir); !st.Disposition.Applied {
		t.Fatal("a merge that happened is applied")
	}
	if n := countEvents(t, d.bdir, "disposition_applied"); n != 1 {
		t.Fatalf("one disposition_applied event, got %d", n)
	}
	before := testutil.Git(t, root, "rev-parse", "HEAD")
	if o = d.nextOp(); o["op"] != "stop" || o["cleanup"] != nil {
		t.Fatalf("%v", o)
	}
	if testutil.Git(t, root, "rev-parse", "HEAD") != before {
		t.Fatal("an applied merge is never made twice")
	}
}

// TestArchiveKeepStaysClean is the other half of the applied rule: keep has
// no git work at all, so it is applied by the archive commit itself and the
// run leaves nothing uncommitted behind.
func TestArchiveKeepStaysClean(t *testing.T) {
	t.Parallel()
	d, bdir, _ := finishedRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "keep", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o := d.nextOp(); o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf("keep is applied by the archive commit, which is the run's last write: %q", s)
	}
	st, _ := bundle.LoadState(bdir)
	if st.Disposition == nil || !st.Disposition.Applied {
		t.Fatalf("%+v", st.Disposition)
	}
}

// TestBranchFinishDiscardCopiesTheBundle covers the destructive choice: it
// takes --confirm <slug>, and the bundle survives the branch deletion as an
// ignored copy under <dir>/.discarded/.
func TestBranchFinishDiscardCopiesTheBundle(t *testing.T) {
	t.Parallel()
	d, bdir, _ := finishedRun(t)
	// An older run of this slug left a copy behind; it must be replaced
	// wholesale, not merged into.
	stale := filepath.Join(filepath.Dir(bdir), ".discarded", "demo", "stale.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("from an older run of this slug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := d.cmd("answer", "--gate", "branch_finish", "--choice", "discard", "--slug", "demo"); code == 0 {
		t.Fatal("discard needs --confirm <slug>")
	}
	if code, _, _ := d.cmd("answer", "--gate", "branch_finish", "--choice", "discard",
		"--confirm", "nope", "--slug", "demo"); code == 0 {
		t.Fatal("confirm must equal the slug")
	}
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "discard",
		"--confirm", "demo", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	o := d.nextOp()
	if o["op"] != "stop" {
		t.Fatalf("%v", o)
	}
	copied := filepath.Join(filepath.Dir(bdir), ".discarded", "demo", "state.json")
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("bundle copied before discard: %v", err)
	}
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) == 0 || !strings.Contains(cleanup[0].(string), "git branch -D takt/demo") {
		t.Fatalf("branch deletion handed off (checked out here): %v", cleanup)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a stale copy of the slug must be replaced, not merged into: %v", err)
	}
	// discard's own last two writes (applied, the event) are deliberately
	// left uncommitted on a branch that is going away; what must never show
	// up in the tree is the copy.
	if s := testutil.Git(t, d.root, "status", "--porcelain"); strings.Contains(s, ".discarded") {
		t.Fatalf(".discarded must be ignored: %q", s)
	}
}

// TestBranchFinishAdoptedOffersPrAndKeepOnly covers the adopted branch: takt
// created neither the branch nor its history, so it never merges, deletes or
// discards it — only pr and keep are on the table, and the archive leaves the
// recorded pull request alone.
func TestBranchFinishAdoptedOffersPrAndKeepOnly(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, ".takt.json", fakeCfg)
	testutil.Commit(t, root, "config")
	testutil.Git(t, root, "checkout", "-b", "feature")
	code, got, errb := runIn(t, root, nil, "init", "--slug", "demo", "--no-goals", "Add a greeting")
	if code != 0 || got["branch_adopted"] != true {
		t.Fatalf("init must adopt feature: %d %v %s", code, got, errb)
	}
	bdir := filepath.Join(root, "docs", "takt", "demo")
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"}}
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp()
	testutil.WriteFile(t, root, "docs/takt/demo/retro.md", "# Retro\n")
	d.cmd("done", "--step", "retro", "--slug", "demo")
	o := d.nextOp()
	if o["op"] != "ask" || o["gate"] != "branch_finish" {
		t.Fatalf("%v", o)
	}
	opts := optionsByChoice(o)
	if len(opts) != 2 || opts["pr"] == nil || opts["keep"] == nil {
		t.Fatalf("an adopted branch offers exactly pr and keep: %v", o["options"])
	}
	if code, _, errb = d.cmd("answer", "--gate", "branch_finish", "--choice", "pr", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o = d.nextOp(); o["op"] != "run" || o["step"] != "push_pr" ||
		o["inputs"].(map[string]any)["branch"] != "feature" {
		t.Fatalf("pr → push_pr on the adopted branch: %v", o)
	}
	if code, _, errb = d.cmd("done", "--step", "push_pr", "--url", adoptedPRURL, "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o = d.nextOp(); o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	if o["cleanup"] != nil {
		t.Fatalf("takt never touches an adopted branch: %v", o["cleanup"])
	}
	st, _ := bundle.LoadState(bdir)
	if st.Disposition == nil || st.Disposition.PRURL != adoptedPRURL || !st.Disposition.Applied {
		t.Fatalf("the archive must keep the recorded pull request: %+v", st.Disposition)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "feature" {
		t.Fatalf("the adopted branch must still be checked out: %s", b)
	}
}
