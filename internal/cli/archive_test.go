package cli_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/testutil"
)

// runShell runs a cleanup command from a stop op exactly as the session
// would: the printed string, verbatim, through a shell in the repository.
func runShell(t *testing.T, dir, script string) (int, string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "bash", "-c", script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if e, ok := errors.AsType[*exec.ExitError](err); ok {
		return e.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// adoptedPRURL is the pull request the scripted session reports opening for
// the adopted-branch run.
const adoptedPRURL = "https://example.invalid/pr/1"

// finishedRun drives a run to the branch_finish question.
func finishedRun(t *testing.T) (*driver, string, map[string]any) {
	t.Helper()
	d, bdir := finishRun(t, "--no-goals")
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
	return linkedWorktreeRunFrom(t, root)
}

// linkedWorktreeRunFrom is linkedWorktreeRun over a repository a run has
// already been initialised in, so a test can put the primary worktree
// somewhere awkward.
func linkedWorktreeRunFrom(t *testing.T, root string) (*driver, string, map[string]any) {
	t.Helper()
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
		t.Fatalf("the archive commit finishes takt's bookkeeping: %+v", st.Disposition)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf("the archive commit is the run's last write: %q", s)
	}
	// A further call re-derives from git rather than replaying a record: the
	// merge is not made a second time, and the hand-off stands for as long as
	// the branch it names does.
	before := testutil.Git(t, root, "rev-parse", "HEAD")
	o = d.nextOp()
	again, _ := o["cleanup"].([]any)
	if o["op"] != "stop" || len(again) != 1 || again[0] != cleanup[0] {
		t.Fatalf("the hand-off stands while the branch does: %v", o)
	}
	if testutil.Git(t, root, "rev-parse", "HEAD") != before {
		t.Fatal("the primary must not take a second merge commit")
	}
}

// TestArchivedMergeWaitsForThePrimaryToComeBack is the check that keeps a
// merge on the base branch. The primary moves to another branch between the
// answer and the archive, so takt merges nothing, says why, and hands over
// the command — then makes the merge itself once the primary is back.
func TestArchivedMergeWaitsForThePrimaryToComeBack(t *testing.T) {
	t.Parallel()
	d, root, _ := linkedWorktreeRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "merge", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	testutil.Git(t, root, "checkout", "-b", "release/1.0")
	o := d.nextOp()
	if o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	ctxm, _ := o["context"].(map[string]any)
	if errText, _ := ctxm["error"].(string); !strings.Contains(errText, "main") || ctxm["merged"] != nil {
		t.Fatalf("the error names the base and claims no merge: %v", ctxm)
	}
	if s := testutil.Git(t, root, "log", "-1", "--format=%s"); strings.HasPrefix(s, "Merge") {
		t.Fatalf("release/1.0 must not receive the run: %s", s)
	}
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) != 1 || !strings.Contains(cleanup[0].(string), "merge --no-ff takt/demo") {
		t.Fatalf("the merge is handed to the session meanwhile: %v", cleanup)
	}
	// The primary comes back to the base; the next call makes the merge.
	testutil.Git(t, root, "checkout", "main")
	o = d.nextOp()
	if ctxm, _ = o["context"].(map[string]any); ctxm["merged"] == nil || ctxm["error"] != nil {
		t.Fatalf("the merge lands once the primary is back: %v", o)
	}
	if s := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.HasPrefix(s, "Merge takt/demo") {
		t.Fatalf("main is the merge commit: %s", s)
	}
	// The base can only have received this state.json through the merge, so
	// what it says about the disposition is true there by construction.
	if s := testutil.Git(t, root, "show", "main:docs/takt/demo/state.json"); !strings.Contains(s, `"applied": true`) {
		t.Fatalf("the merged state.json records the disposition as applied:\n%s", s)
	}
}

// TestArchivedMergeAbortsAConflictAndRetries: a merge that stops on a
// conflict must not be left in the primary worktree — takt is not there to
// resolve it, and that worktree is not the one the session is sitting in.
func TestArchivedMergeAbortsAConflictAndRetries(t *testing.T) {
	t.Parallel()
	d, root, _ := linkedWorktreeRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "merge", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	before := testutil.Git(t, root, "rev-parse", "HEAD")
	testutil.WriteFile(t, root, "a.go", "package a // the base wrote its own\n")
	testutil.Commit(t, root, "conflicting a.go on main")
	conflicting := testutil.Git(t, root, "rev-parse", "HEAD")
	o := d.nextOp()
	ctxm, _ := o["context"].(map[string]any)
	if o["op"] != "stop" || ctxm["error"] == nil || ctxm["merged"] != nil {
		t.Fatalf("a conflicted merge is reported and claims nothing: %v", o)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the conflicted merge must be aborted, not left in the primary: %v", err)
	}
	if s := testutil.Git(t, root, "status", "--porcelain"); s != "" {
		t.Fatalf("no conflict markers and nothing staged: %q", s)
	}
	if testutil.Git(t, root, "rev-parse", "HEAD") != conflicting {
		t.Fatal("the abort restores the primary's HEAD")
	}
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) != 1 || !strings.Contains(cleanup[0].(string), "merge --no-ff takt/demo") {
		t.Fatalf("the merge is handed to the session: %v", cleanup)
	}
	// Drop the conflicting commit; the next call merges.
	testutil.Git(t, root, "reset", "--hard", before)
	o = d.nextOp()
	if ctxm, _ = o["context"].(map[string]any); ctxm["merged"] == nil || ctxm["error"] != nil {
		t.Fatalf("the merge lands once the conflict is gone: %v", o)
	}
	if s := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.HasPrefix(s, "Merge takt/demo") {
		t.Fatalf("main is the merge commit: %s", s)
	}
}

// TestArchivedDiscardCleanupRunsAsPrinted: a hand-off is only worth printing
// if it works, so this runs the printed command verbatim. It is also what the
// clean tree buys — `git checkout <base>` would refuse over a modified
// state.json, which is why nothing is written after the archive commit.
func TestArchivedDiscardCleanupRunsAsPrinted(t *testing.T) {
	t.Parallel()
	d, _, _ := finishedRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "discard",
		"--confirm", "demo", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	o := d.nextOp()
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) != 1 {
		t.Fatalf("one hand-off command: %v", o)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf("the tree is clean before the hand-off runs: %q", s)
	}
	// The sweep clears the run's own untracked litter, nothing else: -x
	// would add the power to delete files the base branch ignores under that
	// path, which are not this run's to remove.
	if sweep := cleanup[0].(string); !strings.Contains(sweep, "git clean -fd --") ||
		strings.Contains(sweep, "-fdx") {
		t.Fatalf("the discard sweep must not be forced past the base's ignores: %q", sweep)
	}
	if code, out := runShell(t, d.root, cleanup[0].(string)); code != 0 {
		t.Fatalf("the printed cleanup must run as-is: exit %d\n%s", code, out)
	}
	if b := testutil.Git(t, d.root, "branch", "--list", "takt/demo"); b != "" {
		t.Fatalf("takt/demo must be gone: %q", b)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf("the repository is clean once the hand-off has run: %q", s)
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
	// The copy is ignored and the archive commit was the last write, so the
	// tree the hand-off has to run in is clean.
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf(".discarded must be ignored and nothing else left behind: %q", s)
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

// TestMergeHandOffRunsFromAPathWithASpace covers the hand-off strings: takt
// prints these for a session to run through a shell, so every path it
// interpolates has to survive word splitting. Here the primary worktree —
// the path the merge command names — sits under a directory with a space in
// its name, and the printed command is run verbatim.
func TestMergeHandOffRunsFromAPathWithASpace(t *testing.T) {
	t.Parallel()
	spaced := testutil.NewRepoAt(t, filepath.Join(t.TempDir(), "my worktrees", "primary"))
	root, _ := setupRunIn(t, spaced, "--no-goals")
	d, _, _ := linkedWorktreeRunFrom(t, root)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "merge", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	// The primary moves off the base, so takt hands the merge over rather
	// than making it — which is the command under test.
	testutil.Git(t, root, "checkout", "-b", "release/1.0")
	o := d.nextOp()
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) != 1 || !strings.Contains(cleanup[0].(string), "merge --no-ff takt/demo") {
		t.Fatalf("the merge is handed to the session: %v", o)
	}
	testutil.Git(t, root, "checkout", "main")
	if code, out := runShell(t, root, cleanup[0].(string)); code != 0 {
		t.Fatalf("the printed cleanup must run as-is: exit %d\n%s", code, out)
	}
	// git's own subject for a hand-run merge, not takt's — what matters is
	// that the command ran and main took the run.
	if s := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.HasPrefix(s, "Merge") ||
		!strings.Contains(s, "takt/demo") {
		t.Fatalf("the hand-off must merge the run into main: %s", s)
	}
}

// doctorSays reports whether `takt doctor --all` prints a finding at this
// level with this message. Archived bundles are only judged with --all
// (spec §11), which is exactly the run this reports on.
func doctorSays(t *testing.T, root, level, msg string) bool {
	t.Helper()
	code, got, errb := runIn(t, root, nil, "doctor", "--json", "--all")
	if got == nil {
		t.Fatalf("doctor --json printed nothing: %d %s", code, errb)
	}
	fs, ok := got["findings"].([]any)
	if !ok {
		t.Fatalf("doctor --json has no findings: %v", got)
	}
	for _, x := range fs {
		f, isMap := x.(map[string]any)
		if !isMap {
			t.Fatalf("finding is not an object: %v", x)
		}
		if f["level"] == level && f["message"] == msg {
			return true
		}
	}
	return false
}

// TestArchiveCommitIsRetriedAfterAStrandedIndexLock covers review I1's first
// window. `archive` writes phase, applied and the discard copy before its
// commit, so a commit git will not take — here a stranded .git/index.lock,
// tomorrow a hook that says no — leaves the run archived with its own record
// only in the worktree. Every later `next` used to walk straight past it,
// printing a discard hand-off whose `git checkout` cannot run over a
// modified state.json. Now the commit is taken again, along with any
// bookkeeping that was lost with it.
func TestArchiveCommitIsRetriedAfterAStrandedIndexLock(t *testing.T) {
	t.Parallel()
	d, bdir, _ := finishedRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "discard",
		"--confirm", "demo", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	lock := filepath.Join(d.root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errb := d.cmd("next", "--slug", "demo")
	if code != 1 || !strings.Contains(errb, "index.lock") {
		t.Fatalf("an archive whose commit fails must exit 1 with git's error: %d %s", code, errb)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil || st.Phase != bundle.PhaseArchived {
		t.Fatalf("the bookkeeping is written before the commit: %v %+v", err, st)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s == "" {
		t.Fatal("the failed commit must leave the bundle uncommitted")
	}
	if !doctorSays(t, d.root, "ERROR", "archived run has an uncommitted bundle") {
		t.Fatal("doctor must report the archive that never got its commit")
	}
	// A kill can land before the copy as easily as after it, so the retry
	// has to redo the discard bookkeeping and not only the commit.
	discarded := filepath.Join(filepath.Dir(bdir), ".discarded", "demo")
	if err = os.RemoveAll(discarded); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	o := d.nextOp()
	if o["op"] != "stop" || o["reason"] != stopArchived {
		t.Fatalf("%v", o)
	}
	if s := testutil.Git(t, d.root, "log", "-1", "--format=%s"); s != "takt(demo): archive" {
		t.Fatalf("the retry must take the archive commit: %s", s)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf("tree not clean: %q", s)
	}
	if _, serr := os.Stat(filepath.Join(discarded, "state.json")); serr != nil {
		t.Fatalf("the retry must redo the discard copy: %v", serr)
	}
	if doctorSays(t, d.root, "ERROR", "archived run has an uncommitted bundle") {
		t.Fatal("a committed archive must not be reported")
	}
	// And the hand-off the same call printed now runs as written — which is
	// what the commit buys: `git checkout` refuses over a modified state.json.
	cleanup, _ := o["cleanup"].([]any)
	if len(cleanup) != 1 {
		t.Fatalf("one hand-off command: %v", o)
	}
	if scode, out := runShell(t, d.root, cleanup[0].(string)); scode != 0 {
		t.Fatalf("the printed cleanup must run as-is: exit %d\n%s", scode, out)
	}
}

// TestArchiveCommitIsRetriedAfterASoftReset covers review I1's other window:
// the kill between the archive's writes and its commit. Nothing distinguishes
// that from a commit that was taken and then undone, so a soft reset stands
// in for it — and the next call re-derives the same commit from the same
// files.
func TestArchiveCommitIsRetriedAfterASoftReset(t *testing.T) {
	t.Parallel()
	d, bdir, _ := finishedRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "keep", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o := d.nextOp(); o["op"] != "stop" || o["reason"] != stopArchived {
		t.Fatalf("%v", o)
	}
	testutil.Git(t, d.root, "reset", "--soft", "HEAD~1")
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s == "" {
		t.Fatal("the reset must leave the archive's writes uncommitted")
	}
	if !doctorSays(t, d.root, "ERROR", "archived run has an uncommitted bundle") {
		t.Fatal("doctor must report the archive whose commit is gone")
	}
	o := d.nextOp()
	if o["op"] != "stop" || o["reason"] != stopArchived {
		t.Fatalf("%v", o)
	}
	if s := testutil.Git(t, d.root, "log", "-1", "--format=%s"); s != "takt(demo): archive" {
		t.Fatalf("the retry must take the archive commit: %s", s)
	}
	if s := testutil.Git(t, d.root, "status", "--porcelain"); s != "" {
		t.Fatalf("tree not clean: %q", s)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil || st.Phase != bundle.PhaseArchived || st.Disposition == nil || !st.Disposition.Applied {
		t.Fatalf("%v %+v", err, st)
	}
}
