package cli_test

import (
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

// TestBranchFinishMergeInLinkedWorktree is the worktree layout merge exists
// for: the run is driven from a linked worktree on the run branch while the
// primary sits clean on the base, so takt can merge there without ever
// moving this worktree's HEAD.
func TestBranchFinishMergeInLinkedWorktree(t *testing.T) {
	t.Parallel()
	// Primary worktree on main; the run lives in a linked worktree on takt/demo.
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
}

// TestBranchFinishDiscardCopiesTheBundle covers the destructive choice: it
// takes --confirm <slug>, and the bundle survives the branch deletion as an
// ignored copy under <dir>/.discarded/.
func TestBranchFinishDiscardCopiesTheBundle(t *testing.T) {
	t.Parallel()
	d, bdir, _ := finishedRun(t)
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
	if testutil.Git(t, d.root, "status", "--porcelain") != "" {
		t.Fatal(".discarded must be ignored")
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
