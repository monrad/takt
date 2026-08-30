package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/testutil"
)

// atRetroOp drives a run to the retro op and leaves it there: verified, the
// pair on disk, and the retrospective not yet written. It is where `takt
// retro --rewrite` has something to rewrite and the run is still in the
// finish phase.
func atRetroOp(t *testing.T) (*driver, string) {
	t.Helper()
	d, bdir := finishRun(t, "--no-goals")
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	if o := d.nextOp(); o["op"] != "run" || o["step"] != "retro" {
		t.Fatalf("the run must be sitting at the retro op: %v", o)
	}
	return d, bdir
}

// retroPair is the two files a rewrite replaces, in the order the op names
// them.
func retroPair(bdir string) (string, string) {
	return filepath.Join(bdir, "finish", "retro-inputs.json"),
		filepath.Join(bdir, "finish", "retro-skeleton.md")
}

// assertRewriteOp checks the op a successful `takt retro --rewrite` printed
// is the retro `run` op `takt next` emits — same kind, same step, all three
// paths — under this command's own narration, and that the pair it names is
// really on disk. It returns the re-derived skeleton for the callers that
// assert on what it renders.
func assertRewriteOp(t *testing.T, o map[string]any, bdir string) string {
	t.Helper()
	if o["op"] != "run" || o["step"] != "retro" || o["narration"] != "rewrite the retrospective" {
		t.Fatalf("a rewrite prints the retro run op, narrated as a rewrite: %v", o)
	}
	// The done line is the one `next` prints too: what a rewrite produces is
	// recorded by the same verb, which is what makes it a redo rather than a
	// second kind of retro.
	if o["done"] != "takt done --step retro --slug demo" {
		t.Fatalf("done = %v", o["done"])
	}
	in, ok := o["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("run op without inputs: %v", o)
	}
	inputsPath, skeletonPath := retroPair(bdir)
	for k, want := range map[string]string{
		"inputs_path":   inputsPath,
		"retro_path":    filepath.Join(bdir, "retro.md"),
		"skeleton_path": skeletonPath,
	} {
		if in[k] != want {
			t.Fatalf("inputs[%q] = %v, want %s", k, in[k], want)
		}
	}
	if !fileExists(inputsPath) {
		t.Fatalf("the rewrite must have written %s", inputsPath)
	}
	b, err := os.ReadFile(skeletonPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "# Retro — demo") {
		t.Fatalf("the re-derived skeleton is this run's:\n%s", b)
	}
	return string(b)
}

// TestRetroRewriteEmitsTheOpAndWritesBothFiles is the command end to end:
// with the pair deleted from under it, a rewrite derives both files again
// and hands back the retro op naming all three paths (spec §7).
func TestRetroRewriteEmitsTheOpAndWritesBothFiles(t *testing.T) {
	t.Parallel()
	d, bdir := atRetroOp(t)
	inputsPath, skeletonPath := retroPair(bdir)
	for _, p := range []string{inputsPath, skeletonPath} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	code, o, errb := d.cmd("retro", "--rewrite", "--slug", "demo")
	if code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	assertRewriteOp(t, o, bdir)
}

// TestRetroWithoutRewriteIsAUsageError pins the verb's intent: re-deriving
// is harmless, so the flag guards nothing — but `takt retro` alone reads
// like a query and this command writes, so the bare form is refused and the
// message names the flag it wants (spec §7).
func TestRetroWithoutRewriteIsAUsageError(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	code, _, errb := runIn(t, root, nil, "retro", "--slug", "demo")
	if code != 2 || !strings.Contains(errb, "--rewrite") {
		t.Fatalf("bare `takt retro` is a usage error naming --rewrite: %d %s", code, errb)
	}
}

// TestRetroRefusesEarlierPhases: the retro's after-life reaches back exactly
// as far as the finish phase. A run still building has no verification, no
// goals record and no history to write a retrospective from, so the rewrite
// refuses in the same wording every other finish verb refuses in.
func TestRetroRefusesEarlierPhases(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	driveToExecute(t, d)
	code, _, errb := d.cmd("retro", "--rewrite", "--slug", "demo")
	if code != 1 {
		t.Fatalf("an execute-phase rewrite must fail: %d %s", code, errb)
	}
	if !strings.Contains(errb, "retro --rewrite runs in the finish or archived phase (now execute)") {
		t.Fatalf("%s", errb)
	}
	if _, err := os.Stat(filepath.Join(bdir, "finish", "retro-skeleton.md")); !os.IsNotExist(err) {
		t.Fatalf("a refused rewrite must derive nothing: %v", err)
	}
}

// archivedRun drives a run all the way to `keep` and leaves it archived,
// with its bundle committed and the run holding no lock — where the retro is
// worth rewriting and the archived `takt next` is what a rewrite races.
func archivedRun(t *testing.T) (*driver, string) {
	t.Helper()
	d, bdir, _ := finishedRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "keep", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o := d.nextOp(); o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("keep archives the run: %v", o)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseArchived {
		t.Fatalf("expected the archived phase, got %s", st.Phase)
	}
	return d, bdir
}

// TestRetroRewriteWorksOnAnArchivedRun is the motivating case (spec §7). It
// is also the only way the disposition ever reaches the page: decideFinish
// emits the retro one row before it asks branch_finish, so the first pass
// renders "not yet chosen" and a rewrite after archiving renders the choice.
func TestRetroRewriteWorksOnAnArchivedRun(t *testing.T) {
	t.Parallel()
	d, bdir := archivedRun(t)
	code, o, errb := d.cmd("retro", "--rewrite", "--slug", "demo")
	if code != 0 {
		t.Fatalf("a rewrite must work on an archived run: %d %s", code, errb)
	}
	skeleton := assertRewriteOp(t, o, bdir)
	if !strings.Contains(skeleton, "- disposition: keep") {
		t.Fatalf("the rewrite renders the disposition the archive recorded:\n%s", skeleton)
	}
	if strings.Contains(skeleton, "not yet chosen") {
		t.Fatalf("a chosen disposition replaces the first pass's open question:\n%s", skeleton)
	}
}

// TestRetroRefusesAHeldLock: a rewrite replaces two tracked files in
// sequence, so it takes the run lock exactly as `takt next` does — and
// because it is not an op loop, a live holder is reported rather than
// written through. The pair it would replace is what the other session may
// be committing, so neither file may be touched on the way out (spec §7).
func TestRetroRefusesAHeldLock(t *testing.T) {
	t.Parallel()
	d, bdir := atRetroOp(t)
	inputs, skeleton := retroArtifacts(t, bdir)
	stamps := modTimes(t, bdir)
	held := &bundle.Session{ID: "other", Host: "elsewhere", Heartbeat: time.Now().UTC()}
	if err := bundle.WriteSession(bdir, held); err != nil {
		t.Fatal(err)
	}
	code, _, errb := d.cmd("retro", "--rewrite", "--slug", "demo")
	if code != 1 {
		t.Fatalf("a held lock must fail the rewrite: %d %s", code, errb)
	}
	if !strings.Contains(errb, "the run is held by other") ||
		!strings.Contains(errb, "takt unlock --slug demo") {
		t.Fatalf("the error names the holder and the way out: %s", errb)
	}
	againInputs, againSkeleton := retroArtifacts(t, bdir)
	if againInputs != inputs || againSkeleton != skeleton {
		t.Fatal("a refused rewrite must leave both files alone")
	}
	for i, at := range modTimes(t, bdir) {
		if !at.Equal(stamps[i]) {
			t.Fatalf("a refused rewrite must not rewrite the pair: %v → %v", stamps[i], at)
		}
	}
	if sess, err := bundle.ReadSession(bdir); err != nil || sess == nil || sess.ID != held.ID {
		t.Fatalf("a refused rewrite must not stamp itself on the lock: %+v %v", sess, err)
	}
}

// modTimes is the pair's modification times, so a refusal that wrote the
// same bytes back — which comparing contents would miss — is still caught.
func modTimes(t *testing.T, bdir string) []time.Time {
	t.Helper()
	inputsPath, skeletonPath := retroPair(bdir)
	var out []time.Time
	for _, p := range []string{inputsPath, skeletonPath} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fi.ModTime())
	}
	return out
}

// TestRetroRewriteWritesNoStateAndTakesNoCommit pins the other half of the
// command's contract: it derives, it prints, and it stops. The pair is
// bundle files, swept by whichever command next commits the bundle — `takt
// done --step retro` — so this one commits nothing, and it decides nothing
// state.json records. The lock it takes lives in its own untracked session
// file, so acquiring it does not perturb the comparison (spec §7).
func TestRetroRewriteWritesNoStateAndTakesNoCommit(t *testing.T) {
	t.Parallel()
	d, bdir := atRetroOp(t)
	before, err := os.ReadFile(filepath.Join(bdir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	head := testutil.Git(t, d.root, "rev-parse", "HEAD")
	code, o, errb := d.cmd("retro", "--rewrite", "--slug", "demo")
	if code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	assertRewriteOp(t, o, bdir)
	after, err := os.ReadFile(filepath.Join(bdir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a rewrite writes no state:\n%s\n%s", before, after)
	}
	if now := testutil.Git(t, d.root, "rev-parse", "HEAD"); now != head {
		t.Fatalf("a rewrite takes no commit: %s → %s", head, now)
	}
}

// TestRetroRewriteTargetsARunByDir exercises the --dir half of the standard
// pair, which every other test here reaches the run without. TAKT_DIR points
// at a directory the run is not in, so the same call is only answerable with
// the flag: without it the command cannot find the run at all.
func TestRetroRewriteTargetsARunByDir(t *testing.T) {
	t.Parallel()
	d, bdir := atRetroOp(t)
	env := map[string]string{"TAKT_SESSION": "S", "TAKT_DIR": "docs/elsewhere"}
	code, _, errb := runIn(t, d.root, env, "retro", "--rewrite", "--slug", "demo")
	if code == 0 || !strings.Contains(errb, "elsewhere") {
		t.Fatalf("the control: without --dir the run is looked for under TAKT_DIR: %d %s", code, errb)
	}
	code, o, errb := runIn(t, d.root, env, "retro", "--rewrite", "--dir", "docs/takt", "--slug", "demo")
	if code != 0 {
		t.Fatalf("--dir must name the bundle the rewrite works on: %d %s", code, errb)
	}
	assertRewriteOp(t, o, bdir)
}

// TestRetroRewriteLockShutsOutAnArchivedNext is the rewrite's central
// guarantee, seen from the other side. Per-file atomic renames make each
// artifact a snapshot; they do not make the *pair* one, and `takt next` on
// an archived run recommits whatever in the bundle is dirty — so a rewrite
// caught between its two writes would have half a pair committed under it,
// and applyAndStop would clear its lock on the way out. The archived path
// therefore takes the same lock before it recommits: with a rewrite holding
// the run, the half-written pair stays in the worktree and HEAD stands still
// (spec §7).
func TestRetroRewriteLockShutsOutAnArchivedNext(t *testing.T) {
	t.Parallel()
	d, bdir := archivedRun(t)
	if status := bundleStatus(t, d.root); status != "" {
		t.Fatalf("the archive commits the bundle, so the race starts from a clean tree: %q", status)
	}
	// A rewrite in flight: the lock is held, retro-inputs.json has been
	// replaced and retro-skeleton.md has not — exactly the half-updated pair
	// an unlocked recommit would capture.
	held := &bundle.Session{ID: "other", Host: "elsewhere", Heartbeat: time.Now().UTC()}
	if err := bundle.WriteSession(bdir, held); err != nil {
		t.Fatal(err)
	}
	inputsPath, _ := retroPair(bdir)
	if err := os.WriteFile(inputsPath, []byte(`{"half":"written"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	head := testutil.Git(t, d.root, "rev-parse", "HEAD")
	code, o, errb := d.cmd("next", "--slug", "demo")
	if code != 0 || o["op"] != "ask" || o["gate"] != "owner" {
		t.Fatalf("an archived next must stop at the owner gate while the run is held: %d %v %s", code, o, errb)
	}
	if now := testutil.Git(t, d.root, "rev-parse", "HEAD"); now != head {
		t.Fatalf("nothing may be committed over a rewrite that holds the run: %s → %s", head, now)
	}
	if status := bundleStatus(t, d.root); status == "" {
		t.Fatal("the half-written pair must still be the rewrite's to finish, not something git now holds")
	}
	if sess, err := bundle.ReadSession(bdir); err != nil || sess == nil || sess.ID != held.ID {
		t.Fatalf("and the rewrite still holds the run it was mid-way through: %+v %v", sess, err)
	}
}

// bundleStatus is everything git has outstanding under the run's bundle —
// the same question recommitArchive asks before it commits an archive it
// thinks never landed.
func bundleStatus(t *testing.T, root string) string {
	t.Helper()
	return testutil.Git(t, root, "status", "--porcelain", "--", filepath.Join("docs", "takt", "demo"))
}

// TestRetroRewriteWithNoSessionIDTakesTheLockAsGenerated: takt invents an id
// for a session that presents none and records on the lock that it did, so a
// later call knows the holder is one no process can ever present again and
// takes the run from it silently. The rewrite borrows `takt next`'s lock
// whole, that flag included — and every other test here runs with
// TAKT_SESSION set, so this is the one call that leaves it to takt.
func TestRetroRewriteWithNoSessionIDTakesTheLockAsGenerated(t *testing.T) {
	t.Parallel()
	d, bdir := atRetroOp(t)
	if err := bundle.ClearSession(bdir); err != nil { // as `takt unlock` leaves it
		t.Fatal(err)
	}
	code, o, errb := runIn(t, d.root, nil, "retro", "--rewrite", "--slug", "demo")
	if code != 0 {
		t.Fatalf("a session that presents no id may still rewrite: %d %s", code, errb)
	}
	assertRewriteOp(t, o, bdir)
	sess, err := bundle.ReadSession(bdir)
	if err != nil || sess == nil || !sess.Generated {
		t.Fatalf("the lock must record the holder as takt's own invention: %+v %v", sess, err)
	}
}

// TestRetroRewriteReportsADerivationFailure: the rewrite is worth no more
// than what it can read, and spec.md — whose assumptions table the Decisions
// section is partly built from — is read outright rather than best-effort,
// because a finish-phase run has one. A bundle missing it fails with the read
// error rather than printing an op naming a pair the command did not write.
func TestRetroRewriteReportsADerivationFailure(t *testing.T) {
	t.Parallel()
	d, bdir := atRetroOp(t)
	inputs, skeleton := retroArtifacts(t, bdir)
	if err := os.Remove(filepath.Join(bdir, "spec.md")); err != nil {
		t.Fatal(err)
	}
	code, o, errb := d.cmd("retro", "--rewrite", "--slug", "demo")
	if code != 1 || !strings.Contains(errb, "spec.md") {
		t.Fatalf("a derivation that cannot read the bundle fails naming the file: %d %v %s", code, o, errb)
	}
	if againInputs, againSkeleton := retroArtifacts(t, bdir); againInputs != inputs || againSkeleton != skeleton {
		t.Fatal("and a failed derivation replaces neither half of the pair")
	}
}
