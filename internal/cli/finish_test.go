package cli_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

const goalVerdictsJSON = "```json\n[{\"id\":\"G1\",\"verdict\":\"%s\"," +
	"\"evidence\":\"a.go and b.go exist\",\"citations\":[\"a.go:1\"]}]\n```\n"

func recordGoalVerdict(t *testing.T, d *driver, verdict string) (int, map[string]any, string) {
	t.Helper()
	msg := filepath.Join(t.TempDir(), "goals.txt")
	os.WriteFile(msg, []byte(fmt.Sprintf(goalVerdictsJSON, verdict)), 0o600)
	return d.cmd("record", "--agent", "goal-assessor", "--from", msg, "--slug", "demo")
}

func TestGoalAssessorDispatchRecordAndCheck(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t) // goals on
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	o := d.nextOp()
	ag := agentsOf(t, o)
	if o["op"] != "dispatch" || len(ag) != 1 || ag[0]["agent"] != "goal-assessor" || ag[0]["model"] != "sonnet" {
		t.Fatalf("%v", o)
	}
	brief, _ := os.ReadFile(ag[0]["brief"].(string))
	for _, want := range []string{"G1", "UNTRUSTED-ARTIFACT", "a.go", "go test", "achieved|partial|missed"} {
		if !strings.Contains(string(brief), want) {
			t.Fatalf("brief lacks %q:\n%s", want, brief)
		}
	}
	if code, got, errb := recordGoalVerdict(t, d, "achieved"); code != 0 || got["all_achieved"] != true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	st, _ := bundle.LoadState(bdir)
	if st.GoalsCheckedSHA == nil || *st.GoalsCheckedSHA != *st.VerifiedSHA {
		t.Fatalf("goals_checked_sha = %v", st.GoalsCheckedSHA)
	}
	if o = d.nextOp(); o["op"] != "run" || o["step"] != "retro" {
		t.Fatalf("all achieved → retro: %v", o)
	}
}

// TestAValidAssessmentEndsTheAssessorsAttemptStreak is the assessor's half
// of the streak reset (spec §5.3 row 21): the cap counts the rejections
// since the last reset, and a reply takt could use ends that run — with a
// reset that names the record as its reason and carries no problems, so
// nothing stale is quoted back into a later brief. The assessor has one
// mode, so its reset names none.
func TestAValidAssessmentEndsTheAssessorsAttemptStreak(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t)
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	if o := d.nextOp(); o["op"] != "dispatch" {
		t.Fatalf("expected the goal-assessor dispatch: %v", o)
	}
	bad := filepath.Join(t.TempDir(), "bad.txt")
	if err := os.WriteFile(bad, []byte("I had a look and it all seems fine.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, got, errb := d.cmd("record", "--agent", "goal-assessor", "--from", bad, "--slug", "demo")
	if code != 0 || got["valid"] != false {
		t.Fatalf("a rejected assessment is a document, not a failure: %d %v %s", code, got, errb)
	}
	if n := countEvents(t, bdir, "goals_invalid"); n != 1 {
		t.Fatalf("one rejection appends one goals_invalid, got %d", n)
	}
	if n := countEvents(t, bdir, "goals_attempts_reset"); n != 0 {
		t.Fatalf("a rejection ends nothing, got %d resets", n)
	}
	if code, got, errb = recordGoalVerdict(t, d, "achieved"); code != 0 || got["all_achieved"] != true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	resets := eventsOfType(t, bdir, "goals_attempts_reset")
	if len(resets) != 1 {
		t.Fatalf("a valid assessment ends the streak exactly once: %+v", resets)
	}
	if resets[0].Data["reason"] != "recorded" {
		t.Fatalf("the reset must name the record as its reason: %+v", resets[0])
	}
	if _, ok := resets[0].Data["mode"]; ok {
		t.Fatalf("the assessor has no mode to name: %+v", resets[0])
	}
}

func TestGoalsUnmetGateWaiveAndFix(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t)
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp()
	if code, got, _ := recordGoalVerdict(t, d, "missed"); code != 0 || got["all_achieved"] != false {
		t.Fatalf("%d %v", code, got)
	}
	o := d.nextOp()
	if o["op"] != "ask" || o["gate"] != "goals_unmet" {
		t.Fatalf("%v", o)
	}
	unmet := o["context"].(map[string]any)["unmet"].([]any)
	if len(unmet) != 1 || unmet[0].(map[string]any)["id"] != "G1" {
		t.Fatalf("%v", unmet)
	}
	// fix drops the record: the next call re-dispatches the assessor at the same HEAD.
	d.cmd("answer", "--gate", "goals_unmet", "--choice", "fix", "--slug", "demo")
	if o = d.nextOp(); o["op"] != "dispatch" {
		t.Fatalf("fix → re-assess: %v", o)
	}
	recordGoalVerdict(t, d, "missed")
	d.nextOp()
	if code, _, _ := d.cmd("answer", "--gate", "goals_unmet", "--choice", "waive", "--slug", "demo"); code == 0 {
		t.Fatal("waive needs --reason")
	}
	if code, _, errb := d.cmd("answer", "--gate", "goals_unmet", "--choice", "waive",
		"--reason", "docs later", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	st, _ := bundle.LoadState(bdir)
	if st.GoalsCheckedSHA == nil {
		t.Fatal("waive checks the goals at HEAD")
	}
	events, _ := bundle.ReadEvents(bdir)
	seen := false
	for _, e := range events {
		if e.Type == "goal_waived" && e.Data["goal"] == "G1" && e.Data["reason"] == "docs later" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("one goal_waived event per waived goal")
	}
	if o = d.nextOp(); o["op"] != "run" || o["step"] != "retro" {
		t.Fatalf("%v", o)
	}
}

// TestGoalAssessorRecordRejectsBadVerdicts covers the failure contract of
// `record --agent goal-assessor` (review M1): an assessment takt cannot use
// is reported the way the planner's index already is — {valid:false,
// problems}, exit 0 — not as a command failure. Nothing is written, so the
// dispatch is still pending and `takt next` hands the brief out again.
func TestGoalAssessorRecordRejectsBadVerdicts(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t)
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp()
	for name, body := range map[string]string{
		"unknown goal id": "```json\n[{\"id\":\"G9\",\"verdict\":\"achieved\",\"evidence\":\"x\"}]\n```\n",
		"no JSON block":   "I had a look and it all seems fine.\n",
	} {
		msg := filepath.Join(t.TempDir(), "bad.txt")
		if err := os.WriteFile(msg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		code, got, errb := d.cmd("record", "--agent", "goal-assessor", "--from", msg, "--slug", "demo")
		if code != 0 {
			t.Fatalf("%s: a rejected assessment is a document, not a failure: %d %s", name, code, errb)
		}
		if got["valid"] != false {
			t.Fatalf("%s: %v", name, got)
		}
		problems, ok := got["problems"].([]any)
		if !ok || len(problems) == 0 {
			t.Fatalf("%s: a rejection must say what is wrong: %v", name, got)
		}
	}
	if _, err := os.Stat(filepath.Join(bdir, "finish", "goals.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a rejected assessment must write no record: %v", err)
	}
	// The rejection itself is on the log, as the planner's is — audit only,
	// nothing counts it (spec §4.4).
	if n := countEvents(t, bdir, "goals_invalid"); n != 2 {
		t.Fatalf("each rejection appends one goals_invalid, got %d", n)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if problems, ok := last.Data["problems"].([]any); !ok || len(problems) == 0 {
		t.Fatalf("goals_invalid must name the problems: %+v", last)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil || st.GoalsCheckedSHA != nil {
		t.Fatalf("%v %+v", err, st.GoalsCheckedSHA)
	}
	if o := d.nextOp(); o["op"] != "dispatch" {
		t.Fatalf("a rejected record leaves the dispatch pending: %v", o)
	}
}

// TestWaiverSurvivesBundleCommitsButNotCodeCommits is the goals-side twin of
// TestVerifiedShaSurvivesBundleOnlyCommitsButNotCodeCommits. Waiving commits
// the bundle on its way out, so HEAD is always one takt commit past the
// record holding the waivers: a re-assessment has to carry them forward
// through takt's own commits (or the user is asked the same question again
// the moment the assessor is re-run) and drop them once real code moves.
func TestWaiverSurvivesBundleCommitsButNotCodeCommits(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t)
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp() // the assessor dispatch
	recordGoalVerdict(t, d, "missed")
	d.nextOp() // the goals_unmet ask
	if code, _, errb := d.cmd("answer", "--gate", "goals_unmet", "--choice", "waive",
		"--reason", "docs later", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	code, got, errb := recordGoalVerdict(t, d, "missed")
	if code != 0 || got["all_achieved"] != true {
		t.Fatalf("takt's own answer commit must not drop the waiver: %d %v %s", code, got, errb)
	}
	rec, _ := os.ReadFile(filepath.Join(bdir, "finish", "goals.json"))
	if !strings.Contains(string(rec), `"docs later"`) {
		t.Fatalf("the waiver is not in the re-written record: %s", rec)
	}
	// A code commit re-opens the question: the waiver was given against code
	// HEAD no longer holds.
	testutil.WriteFile(t, d.root, "z.go", "package z\n")
	testutil.Commit(t, d.root, "user fix")
	if code, got, _ = recordGoalVerdict(t, d, "missed"); code != 0 || got["all_achieved"] != false {
		t.Fatalf("a code commit must drop the waiver: %d %v", code, got)
	}
}

// TestRetroRunInputsAndDone covers row 22 end to end: `next` writes the
// retro inputs and hands the session their absolute path, `done --step
// retro` refuses without a retrospective and commits with one, a repeated
// `done` is ignored without a commit (spec §5.4), and the run then moves on
// to branch_finish.
func TestRetroRunInputsAndDone(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	o := d.nextOp()
	if o["op"] != "run" || o["step"] != "retro" {
		t.Fatalf("%v", o)
	}
	in, ok := o["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("run op without inputs: %v", o)
	}
	p, _ := in["inputs_path"].(string)
	if !filepath.IsAbs(p) || !fileExists(p) {
		t.Fatalf("inputs_path must be absolute and written: %q", p)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"tasks": 2`) || !strings.Contains(string(b), `"verify"`) {
		t.Fatalf("retro inputs: %s", b)
	}
	// The skeleton is derived and written beside the inputs by the same
	// call, so the op names it too and the instructions send the session to
	// it (spec §4, §6).
	sk, _ := in["skeleton_path"].(string)
	if sk != filepath.Join(bdir, "finish", "retro-skeleton.md") || !fileExists(sk) {
		t.Fatalf("skeleton_path must name the written skeleton: %q", sk)
	}
	if !strings.Contains(o["instructions"].(string), "retro.md") ||
		!strings.Contains(o["instructions"].(string), sk) ||
		!strings.Contains(o["done"].(string), "--step retro") {
		t.Fatalf("%v", o)
	}
	if code, _, _ := d.cmd("done", "--step", "retro", "--slug", "demo"); code == 0 {
		t.Fatal("done retro needs retro.md")
	}
	testutil.WriteFile(t, d.root, "docs/takt/demo/retro.md", "# Retro\n\nfine\n")
	if code, got, errb := d.cmd("done", "--step", "retro", "--slug", "demo"); code != 0 || got["ok"] != true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	if s := testutil.Git(t, d.root, "log", "-1", "--format=%s"); !strings.Contains(s, "retro done") {
		t.Fatalf("commit: %s", s)
	}
	// idempotent: a second done is ignored and commits nothing.
	before := testutil.Git(t, d.root, "rev-parse", "HEAD")
	if code, got, _ := d.cmd("done", "--step", "retro", "--slug", "demo"); code != 0 || got["ignored"] != true {
		t.Fatalf("%d %v", code, got)
	}
	if after := testutil.Git(t, d.root, "rev-parse", "HEAD"); after != before {
		t.Fatal("a no-op done must not commit")
	}
	if o = d.nextOp(); o["op"] != "ask" || o["gate"] != "branch_finish" {
		t.Fatalf("retro done → branch_finish: %v", o)
	}
}

// TestRetroArtifactsReplayByteIdentical pins G3 on the `next` side: the
// skeleton is written by the same code path as the inputs it renders, and
// every part of that path is a function of what is on disk, so a replayed
// `next` writes both files again byte for byte and hands back the same op.
// That is what makes re-emitting the retro op free (spec §4, design §5.4).
func TestRetroArtifactsReplayByteIdentical(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	first := d.nextOp()
	if first["op"] != "run" || first["step"] != "retro" {
		t.Fatalf("%v", first)
	}
	inputs, skeleton := retroArtifacts(t, bdir)
	// The skeleton is the document the template tells the session to copy:
	// it is titled for the run, it carries the rendered sections, and it
	// says the disposition is not yet chosen — which on a first pass it
	// never is, since row 22 emits this op and row 23 asks branch_finish.
	for _, want := range []string{"# Retro — demo", "## What shipped", "disposition: not yet chosen"} {
		if !strings.Contains(skeleton, want) {
			t.Fatalf("the skeleton is missing %q:\n%s", want, skeleton)
		}
	}
	second := d.nextOp()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("a replayed next changed the op:\n%v\n%v", first, second)
	}
	againInputs, againSkeleton := retroArtifacts(t, bdir)
	if againInputs != inputs {
		t.Fatalf("retro-inputs.json changed on replay:\n%s\n%s", inputs, againInputs)
	}
	if againSkeleton != skeleton {
		t.Fatalf("retro-skeleton.md changed on replay:\n%s\n%s", skeleton, againSkeleton)
	}
}

// retroArtifacts reads the pair the retro op derives: the inputs and the
// skeleton rendered from them.
func retroArtifacts(t *testing.T, bdir string) (string, string) {
	t.Helper()
	in, err := os.ReadFile(filepath.Join(bdir, "finish", "retro-inputs.json"))
	if err != nil {
		t.Fatal(err)
	}
	sk, err := os.ReadFile(filepath.Join(bdir, "finish", "retro-skeleton.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(in), string(sk)
}

// fileExists reports whether an op's path names something that is there.
func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// prURL is the pull request the scripted session reports having opened, and
// reopenedPRURL the one it reports after re-opening it.
const (
	prURL         = "https://git.invalid/monrad/takt/pull/1"
	reopenedPRURL = "https://git.invalid/monrad/takt/pull/2"
)

// atPushPROp drives a run to the push_pr op: verified, retro written, and
// the pr disposition chosen. Answering branch_finish is a later task, so the
// disposition is set through bundle.SaveState — the real API — rather than
// through `takt answer`. On the way it checks that push_pr is refused while
// no disposition names a pull request.
func atPushPROp(t *testing.T, d *driver, bdir string) map[string]any {
	t.Helper()
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp() // the retro run op
	testutil.WriteFile(t, d.root, "docs/takt/demo/retro.md", "# Retro\n\nfine\n")
	if code, _, errb := d.cmd("done", "--step", "retro", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if code, _, _ := d.cmd("done", "--step", "push_pr", "--url", prURL, "--slug", "demo"); code == 0 {
		t.Fatal("push_pr before the pr disposition must be refused")
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Disposition = &bundle.Disposition{Choice: "pr", At: time.Now().UTC()}
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	return d.nextOp()
}

// prSpec is the spec the push_pr fixture finishes with. Its H1 carries an
// apostrophe — the one character a single-quoted shell word cannot hold as
// itself — and its opening paragraph is two lines, so both the title's
// escaping and the paragraph's extent are pinned by what the op produces.
const prSpec = "# Add O'Brien's greeting\n\nFirst paragraph line one.\nline two.\n\n" +
	"## Assumptions & Open Decisions\n| q | d | r | s |\n"

// TestPushPRRunOp covers row 24's op: it names the branch to push and the
// base to open against, its done line asks for the URL, and it hands the
// session the pull request the run itself wrote — the title from the spec's
// H1, single-quoted, and a body file derived from the spec, the goals and
// the bundle rather than `--fill` (#36).
func TestPushPRRunOp(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	// The spec is rewritten in the finish phase, where no decision hashes it
	// again: written any earlier, the brainstorm step would replace it with
	// the driver's own fixture.
	driveToFinish(t, d)
	testutil.WriteFile(t, d.root, "docs/takt/demo/spec.md", prSpec)
	o := atPushPROp(t, d, bdir)
	if o["op"] != "run" || o["step"] != "push_pr" {
		t.Fatalf("%v", o)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	in, ok := o["inputs"].(map[string]any)
	if !ok || in["branch"] != st.Branch || in["base"] != st.Base {
		t.Fatalf("inputs must name the branch and base: %v", o["inputs"])
	}
	if in["pr_title"] != "Add O'Brien's greeting" {
		t.Fatalf("pr_title must be the spec's H1: %v", in["pr_title"])
	}
	body, ok := in["pr_body_path"].(string)
	if !ok || !filepath.IsAbs(body) || body != filepath.Join(bdir, "finish", "pr.md") || !fileExists(body) {
		t.Fatalf("pr_body_path must be the written finish/pr.md: %v", in["pr_body_path"])
	}
	instr, ok := o["instructions"].(string)
	if !ok || !strings.Contains(instr, "gh pr create") ||
		!strings.Contains(instr, `--title 'Add O'\''Brien'\''s greeting'`) ||
		!strings.Contains(instr, "--body-file "+body) || strings.Contains(instr, "--fill") {
		t.Fatalf("instructions:\n%s", instr)
	}
	if !strings.Contains(o["done"].(string), "--url") {
		t.Fatalf("%v", o)
	}
	raw, err := os.ReadFile(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"First paragraph line one.\nline two.", "## Run", "Bundle: docs/takt/demo/",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the body has no %q:\n%s", want, raw)
		}
	}
	// The fixture is --no-goals: a run with no goals must not carry an empty
	// goals section a reader would take for goals nobody met.
	if strings.Contains(string(raw), "## Goals") {
		t.Fatalf("body:\n%s", raw)
	}
}

// assertPROptionDescribesTheCommand pins the text the branch_finish question
// puts in front of the session when it offers to open a pull request. That
// description is the only place the session is told how: it must name the
// command the push_pr op then hands it, `--title` and `--body-file` with the
// two inputs that fill them, and never `--fill` — which would have the
// session title the pull request after a branch and fill its body with
// takt's own bookkeeping commits (#36).
func assertPROptionDescribesTheCommand(t *testing.T, o map[string]any) {
	t.Helper()
	opts, ok := o["options"].([]any)
	if !ok {
		t.Fatalf("branch_finish without options: %v", o)
	}
	for _, x := range opts {
		m, isMap := x.(map[string]any)
		if !isMap || m["choice"] != "pr" {
			continue
		}
		desc, _ := m["description"].(string)
		for _, want := range []string{
			"gh pr create --base <base>", "--title '<title>'", "--body-file <path>",
			"pr_title", "pr_body_path", "takt done --step push_pr",
		} {
			if !strings.Contains(desc, want) {
				t.Fatalf("the pr option must say %q: %q", want, desc)
			}
		}
		if strings.Contains(desc, "--fill") {
			t.Fatalf("the pr option must not send the session to --fill: %q", desc)
		}
		return
	}
	t.Fatalf("branch_finish offers no pr option: %v", opts)
}

// driveToPushPR plays the finish phase to the push_pr op. The driver answers
// every question with its first enabled option, which at branch_finish is
// `pr` — so the run reaches this op the way a session would, and the option's
// own description is checked on the way past.
func driveToPushPR(t *testing.T, d *driver) map[string]any {
	t.Helper()
	for range 20 {
		o := d.nextOp()
		if o["step"] == "push_pr" {
			return o
		}
		if o["gate"] == "branch_finish" {
			assertPROptionDescribesTheCommand(t, o)
		}
		d.step(o)
	}
	t.Fatalf("never reached push_pr: %v", d.ops)
	return nil
}

// assertNextFailsNaming pins that `takt next` refuses a broken bundle and
// says which file broke. No failure may advise deleting a file to get past
// it: a deleted finish/goals.json leaves goals_checked_sha behind, so the
// goals still count as checked, nothing is reassessed, and the run walks on
// to a pull request whose every goal reads "not assessed".
func assertNextFailsNaming(t *testing.T, d *driver, wants ...string) {
	t.Helper()
	code, _, errb := d.cmd("next", "--slug", "demo")
	if code != 1 {
		t.Fatalf("takt next must fail on a broken bundle: %d %s", code, errb)
	}
	for _, want := range wants {
		if !strings.Contains(errb, want) {
			t.Fatalf("the failure must say %q: %s", want, errb)
		}
	}
	if strings.Contains(errb, "delete it") {
		t.Fatalf("no recovery advice may send the user to a delete: %s", errb)
	}
}

// TestPushPRBodyListsGoalVerdicts covers the body's `## Goals` section on a
// run whose goals are on: every goal is listed with what the run decided
// about it, so the pull request carries the verdicts rather than making a
// reviewer open the bundle for them.
//
// The three failures after it are why that section is not best-effort. A
// goals-on run whose goals.md is gone or unparsable, or whose record cannot
// be decoded, is a broken bundle: the call stops, naming the file, instead
// of opening a pull request that says nothing about the goals — the reading
// a silently omitted section would invite.
func TestPushPRBodyListsGoalVerdicts(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t)
	driveToFinish(t, d)
	o := driveToPushPR(t, d)
	in, ok := o["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("push_pr without inputs: %v", o)
	}
	raw, err := os.ReadFile(in["pr_body_path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "## Goals") ||
		!strings.Contains(string(raw), "- G1 — greet works — achieved") {
		t.Fatalf("the body must list every goal with its verdict:\n%s", raw)
	}
	const goalsRel = "docs/takt/demo/goals.md"
	gp := filepath.Join(bdir, "goals.md")
	orig, err := os.ReadFile(gp)
	if err != nil {
		t.Fatal(err)
	}
	away := filepath.Join(t.TempDir(), "goals.md")
	if err = os.Rename(gp, away); err != nil {
		t.Fatal(err)
	}
	assertNextFailsNaming(t, d, "goals.md")
	if err = os.Rename(away, gp); err != nil {
		t.Fatal(err)
	}
	// A goals.md that is there but says nothing a parser recognises is the
	// same broken bundle as a missing one. The parser's own message opens
	// with "goals.md:", so the file is named without prGoals wrapping it a
	// second time.
	testutil.WriteFile(t, d.root, goalsRel, "these are not the goals you are looking for\n")
	assertNextFailsNaming(t, d, "goals.md")
	testutil.WriteFile(t, d.root, goalsRel, string(orig))
	// A record takt cannot decode is not a run whose goals were "not
	// assessed": the call stops. It is the finish facts that decode the
	// record on the way to this op, so they are what fails, and encoding/json
	// names no file in "unexpected end of JSON input" — the path and the one
	// recovery that works both come from the hint (factsHint).
	testutil.WriteFile(t, d.root, "docs/takt/demo/finish/goals.json", "{")
	assertNextFailsNaming(t, d,
		filepath.Join(bdir, "finish", "goals.json"), "cannot be read", "restore it from git")
}

// TestPushPRDoneRecordsTheURL covers `done --step push_pr`: the URL is
// required, it lands on the disposition, repeating the call with the same
// URL is ignored — and repeating it with a different one is a new done, not
// a replay, because the URL is what push_pr has instead of an artifact.
func TestPushPRDoneRecordsTheURL(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t, "--no-goals")
	atPushPROp(t, d, bdir)
	if code, _, _ := d.cmd("done", "--step", "push_pr", "--slug", "demo"); code == 0 {
		t.Fatal("done push_pr needs --url")
	}
	if code, got, errb := d.cmd("done", "--step", "push_pr", "--url", prURL,
		"--slug", "demo"); code != 0 || got["ok"] != true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil || st.Disposition.PRURL != prURL {
		t.Fatalf("%v %+v", err, st.Disposition)
	}
	// idempotent: the same pull request is already on the record.
	before := testutil.Git(t, d.root, "rev-parse", "HEAD")
	if code, got, _ := d.cmd("done", "--step", "push_pr", "--url", prURL,
		"--slug", "demo"); code != 0 || got["ignored"] != true {
		t.Fatalf("%d %v", code, got)
	}
	if after := testutil.Git(t, d.root, "rev-parse", "HEAD"); after != before {
		t.Fatal("a no-op done must not commit")
	}
	// a re-opened pull request is a new done: it replaces the recorded URL,
	// appends a second receipt, and takes exactly one commit.
	if code, got, errb := d.cmd("done", "--step", "push_pr", "--url", reopenedPRURL,
		"--slug", "demo"); code != 0 || got["ok"] != true || got["ignored"] == true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	if st, err = bundle.LoadState(bdir); err != nil || st.Disposition.PRURL != reopenedPRURL {
		t.Fatalf("a changed URL must replace the recorded one: %v %+v", err, st.Disposition)
	}
	if n := countEvents(t, bdir, "pr_pushed"); n != 2 {
		t.Fatalf("a changed URL must append a second pr_pushed, got %d", n)
	}
	if n := testutil.Git(t, d.root, "rev-list", "--count", before+"..HEAD"); n != "1" {
		t.Fatalf("a changed URL must take exactly one commit, got %s", n)
	}
	if msg := testutil.Git(t, d.root, "log", "-1", "--format=%s"); !strings.Contains(msg, "push_pr done") {
		t.Fatalf("commit: %s", msg)
	}
}

// countEvents is how many events of one type the run has logged.
func countEvents(t *testing.T, bdir, typ string) int {
	t.Helper()
	return len(eventsOfType(t, bdir, typ))
}

// forgetSha reproduces the window review I2 names: markVerified and
// markGoalsChecked write the record first and state.json second, so a kill
// between the two leaves a record that still covers HEAD with nothing in
// state pointing at it. The state write is undone through bundle.SaveState —
// the same API takt uses — rather than by editing the file.
func forgetSha(t *testing.T, bdir string, forget func(*bundle.State)) {
	t.Helper()
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	forget(st)
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
}

// healedEvent reports whether the run logged an event of this type that says
// it repaired an interrupted write.
func healedEvent(t *testing.T, bdir, typ string) bool {
	t.Helper()
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == typ && e.Data["healed"] == true {
			return true
		}
	}
	return false
}

// TestPassedVerifyRecordWithoutItsStateWriteIsHealed covers review I2's
// first shape: verify passed and the record says so, but the state write
// that follows it never landed. The run must not be asked
// verification_failed with nothing to show for it — the bookkeeping is
// finished from the record and the run moves on.
func TestPassedVerifyRecordWithoutItsStateWriteIsHealed(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t) // goals on
	driveToFinish(t, d)
	if code, got, errb := d.cmd("verify", "--slug", "demo"); code != 0 || got["passed"] != true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	head := testutil.Git(t, d.root, "rev-parse", "HEAD")
	forgetSha(t, bdir, func(st *bundle.State) { st.VerifiedSHA = nil })
	o := d.nextOp()
	if o["op"] != "dispatch" {
		t.Fatalf("a passed record must never come back as a gate: %v", o)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.VerifiedSHA == nil || *st.VerifiedSHA != head {
		t.Fatalf("verified_sha = %v, want HEAD %s", st.VerifiedSHA, head)
	}
	if st.PendingGate != nil {
		t.Fatalf("no gate may be persisted for a record that passed: %+v", st.PendingGate)
	}
	if !healedEvent(t, bdir, "verify") {
		t.Fatal("the repair must be on the event log")
	}
}

// TestAllAchievedGoalsRecordWithoutItsStateWriteIsHealed is the goals-side
// twin: every goal achieved, the record written, goals_checked_sha lost.
// "Unmet goals: []" is a question with no answer the user could give, so the
// bookkeeping is finished instead and the run reaches the retro.
func TestAllAchievedGoalsRecordWithoutItsStateWriteIsHealed(t *testing.T) {
	t.Parallel()
	d, bdir := finishRun(t)
	driveToFinish(t, d)
	d.cmd("verify", "--slug", "demo")
	d.nextOp() // the assessor dispatch
	if code, got, errb := recordGoalVerdict(t, d, "achieved"); code != 0 || got["all_achieved"] != true {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	head := testutil.Git(t, d.root, "rev-parse", "HEAD")
	forgetSha(t, bdir, func(st *bundle.State) { st.GoalsCheckedSHA = nil })
	o := d.nextOp()
	if o["op"] != "run" || o["step"] != "retro" {
		t.Fatalf("an all-achieved record must never come back as goals_unmet: %v", o)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.GoalsCheckedSHA == nil || *st.GoalsCheckedSHA != head {
		t.Fatalf("goals_checked_sha = %v, want HEAD %s", st.GoalsCheckedSHA, head)
	}
	if st.PendingGate != nil {
		t.Fatalf("no gate may be persisted for a record with nothing unmet: %+v", st.PendingGate)
	}
	if !healedEvent(t, bdir, "goal_check") {
		t.Fatal("the repair must be on the event log")
	}
}

// TestFinishCommandsRefuseOutsideTheFinishPhase covers review M2. The finish
// verbs are run by a session, not by takt, so a stale op or a hand-typed
// command can reach one while the run is still building — and all three
// refuse in the shape `takt verify` already used, so the session learns it
// once.
func TestFinishCommandsRefuseOutsideTheFinishPhase(t *testing.T) {
	t.Parallel()
	root, bdir := setupRunWith(t, "--no-goals")
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"}}
	reached := false
	for range 40 {
		o := d.nextOp()
		st, err := bundle.LoadState(bdir)
		if err != nil {
			t.Fatal(err)
		}
		if st.Phase == bundle.PhaseExecute {
			reached = true
			break
		}
		if reason, stopped := d.step(o); stopped {
			t.Fatalf("the run stopped (%s) before execute: %v", reason, d.ops)
		}
	}
	if !reached {
		t.Fatalf("never reached execute: %v", d.ops)
	}
	msg := d.message("```json\n[{\"id\":\"G1\",\"verdict\":\"achieved\",\"evidence\":\"x\"}]\n```\n")
	testutil.WriteFile(t, root, "docs/takt/demo/retro.md", "# Retro\n\ntoo early\n")
	for _, c := range []struct {
		what string
		args []string
	}{
		{"verify", []string{"verify", "--slug", "demo"}},
		{"record --agent goal-assessor", []string{"record", "--agent", "goal-assessor", "--from", msg, "--slug", "demo"}},
		{"done --step retro", []string{"done", "--step", "retro", "--slug", "demo"}},
	} {
		code, _, errb := d.cmd(c.args...)
		if code != 1 {
			t.Fatalf("%s must be refused in execute: exit %d", c.what, code)
		}
		if !strings.Contains(errb, c.what+" runs in the finish phase (now execute)") ||
			!strings.Contains(errb, "takt next") {
			t.Fatalf("%s: %s", c.what, errb)
		}
	}
	// Nothing of the refused work may have happened.
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.VerifiedSHA != nil || st.GoalsCheckedSHA != nil {
		t.Fatalf("%+v", st)
	}
	if countEvents(t, bdir, "retro") != 0 || countEvents(t, bdir, "verify") != 0 {
		t.Fatal("a refused command must leave no receipt")
	}
}
