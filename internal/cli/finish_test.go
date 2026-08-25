package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/testutil"
)

// driveToFinish plays the loop until the first finish-phase op and returns it.
// The fixture plan's verify commands are all `true`, so verification passes
// unless the test rewrites plan.index.json before load.
func driveToFinish(t *testing.T, d *driver) map[string]any {
	t.Helper()
	for range 60 {
		o := d.nextOp()
		st, err := bundle.LoadState(d.bdir)
		if err != nil {
			t.Fatal(err)
		}
		if st.Phase == bundle.PhaseFinish {
			return o
		}
		d.step(o)
	}
	t.Fatal("never reached finish")
	return nil
}

func finishRun(t *testing.T, initFlags ...string) (*driver, string) {
	t.Helper()
	root, bdir := setupRunWith(t, initFlags...)
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"}}
	return d, bdir
}

// patchVerify rewrites every task's verify commands in the bundle's
// plan.index.json. `takt next` rewrites the index with
// [encoding/json.MarshalIndent]
// when it loads the plan (it stamps each task's wave), so what is on disk in
// the finish phase is no longer the fixture's compact text — the commands
// have to be patched through the index itself, not by string replacement.
func patchVerify(t *testing.T, bdir string, cmds []string) {
	t.Helper()
	p := filepath.Join(bdir, "plan.index.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := plan.ParseIndex(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Tasks) == 0 {
		t.Fatalf("fixture has no tasks to patch: %s", b)
	}
	for i := range idx.Tasks {
		idx.Tasks[i].Verify = cmds
	}
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPassesAndRecordsHead(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	o := driveToFinish(t, d)
	if o["op"] != "exec" || !strings.Contains(o["command"].(string), "takt verify") {
		t.Fatalf("first finish op is exec verify: %v", o)
	}
	code, got, errb := d.cmd("verify", "--slug", "demo")
	if code != 0 || got["passed"] != true || got["no_commands"] == true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	st, _ := bundle.LoadState(bdir)
	head := strings.TrimSpace(testutil.Git(t, d.root, "rev-parse", "HEAD"))
	if st.VerifiedSHA == nil || *st.VerifiedSHA != head {
		t.Fatalf("verified_sha = %v, want HEAD %s", st.VerifiedSHA, head)
	}
	rec, _ := os.ReadFile(filepath.Join(bdir, "finish", "verify.json"))
	if !strings.Contains(string(rec), `"passed": true`) {
		t.Fatalf("record: %s", rec)
	}
	// goals off → the next op is the retro run, not the verify again.
	if o = d.nextOp(); o["op"] != "run" || o["step"] != "retro" {
		t.Fatalf("after a passing verify with goals off: %v", o)
	}
}

func TestVerifiedShaSurvivesBundleOnlyCommitsButNotCodeCommits(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	// A takt bundle commit must not re-arm verification: `takt verify` has
	// just written finish/verify.json and verified_sha, and the next `takt
	// answer` commits the bundle wholesale. (retro.md is deliberately not
	// what is committed here — writing it would take the run to
	// branch_finish, and a pending gate is re-rendered whatever HEAD says.)
	testutil.Commit(t, d.root, "takt(demo): verify recorded")
	st, _ := bundle.LoadState(bdir)
	if o := d.nextOp(); o["op"] == "exec" {
		t.Fatalf("bundle-only commit re-armed verify: %v (verified_sha %v)", o, *st.VerifiedSHA)
	}
	// A code commit does.
	testutil.WriteFile(t, d.root, "z.go", "package z\n")
	testutil.Git(t, d.root, "add", "z.go")
	testutil.Commit(t, d.root, "user fix")
	if o := d.nextOp(); o["op"] != "exec" || !strings.Contains(o["command"].(string), "takt verify") {
		t.Fatalf("code commit must re-arm verify: %v", o)
	}
}

func TestVerifyFailureGateOverrideAndFix(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	// Make the plan's verify fail before it runs: the driver's planner wrote
	// the fixture index, so patch it on disk once the run is at finish.
	driveToFinish(t, d)
	patchVerify(t, bdir, []string{"false"})
	vcode, vout, _ := d.cmd("verify", "--slug", "demo")
	if vcode != 0 || vout["passed"] != false {
		t.Fatalf("a failing command is reported, not an error: %d %v", vcode, vout)
	}
	o := d.nextOp()
	if o["op"] != "ask" || o["gate"] != "verification_failed" {
		t.Fatalf("%v", o)
	}
	failed := o["context"].(map[string]any)["failed"].([]any)
	if len(failed) != 1 || failed[0].(map[string]any)["command"] != "false" {
		t.Fatalf("context names the failed command: %v", failed)
	}
	// fix: clears the gate and the record; the next call re-runs verify.
	if code, _, errb := d.cmd("answer", "--gate", "verification_failed",
		"--choice", "fix", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o = d.nextOp(); o["op"] != "exec" {
		t.Fatalf("fix → re-verify: %v", o)
	}
	d.cmd("verify", "--slug", "demo")
	d.nextOp() // the ask again
	// override without a reason is refused; with one it verifies HEAD.
	if code, _, _ := d.cmd("answer", "--gate", "verification_failed",
		"--choice", "override", "--slug", "demo"); code == 0 {
		t.Fatal("override needs --reason")
	}
	if code, _, errb := d.cmd("answer", "--gate", "verification_failed", "--choice", "override",
		"--reason", "flaky CI", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	st, _ := bundle.LoadState(bdir)
	if st.VerifiedSHA == nil || st.PendingGate != nil {
		t.Fatalf("override sets verified_sha and clears the gate: %+v", st)
	}
	events, _ := bundle.ReadEvents(bdir)
	last := events[len(events)-1]
	if last.Type != "verify" || last.Data["overridden"] != "flaky CI" {
		// the gate_answered event may follow; look back two.
		prev := events[len(events)-2]
		if prev.Type != "verify" || prev.Data["overridden"] != "flaky CI" {
			t.Fatalf("override is in the event log: %+v %+v", prev, last)
		}
	}
}

func TestNoVerificationSpecifyThenProceed(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	driveToFinish(t, d)
	patchVerify(t, bdir, []string{})
	vcode, vout, _ := d.cmd("verify", "--slug", "demo")
	if vcode != 0 || vout["no_commands"] != true {
		t.Fatalf("%d %v", vcode, vout)
	}
	if o := d.nextOp(); o["op"] != "ask" || o["gate"] != "no_verification" {
		t.Fatalf("%v", o)
	}
	if code, _, errb := d.cmd("answer", "--gate", "no_verification", "--choice", "specify",
		"--reason", "test -f a.go", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o := d.nextOp(); o["op"] != "exec" {
		t.Fatalf("specify → re-verify with the extra: %v", o)
	}
	vcode, vout, _ = d.cmd("verify", "--slug", "demo")
	cmds := vout["commands"].([]any)
	if vcode != 0 || vout["passed"] != true || len(cmds) != 1 || cmds[0] != "test -f a.go" {
		t.Fatalf("the extra command ran: %v", vout)
	}
	// proceed path on a fresh run:
	d2, bdir2 := finishRun(t, "--no-goals")
	driveToFinish(t, d2)
	patchVerify(t, bdir2, []string{})
	d2.cmd("verify", "--slug", "demo")
	d2.nextOp()
	if code, _, errb := d2.cmd(
		"answer",
		"--gate",
		"no_verification",
		"--choice",
		"proceed",
		"--slug",
		"demo",
	); code != 0 {
		t.Fatal(errb)
	}
	st, _ := bundle.LoadState(bdir2)
	if st.VerifiedSHA == nil {
		t.Fatal("proceed records verified_sha")
	}
}
