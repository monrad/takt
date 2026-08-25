package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/testutil"
)

// driver executes the ops `takt next` returns exactly as the command prompt
// will (spec §5.2): `run` → write the artifact and `takt done`; `exec` → run
// the takt command; `dispatch` → play the agent and `takt record`; `ask` →
// answer the recommended (first) option; `stop` → end the turn. Everything
// goes through cli.Main and the op JSON — nothing reaches into takt's
// internals — so what these two tests exercise is the whole loop as a
// session sees it (spec G1, G3).
type driver struct {
	t    *testing.T
	root string
	bdir string
	env  map[string]string
	ops  []string // op kinds seen, in order, for assertions
	// replay makes every `next` run twice and asserts spec §5.4's "every op
	// is safe to execute twice" — see assertReplay for the two shapes that
	// takes.
	replay bool
}

func (d *driver) cmd(args ...string) (int, map[string]any, string) {
	d.t.Helper()
	return runIn(d.t, d.root, d.env, args...)
}

// nextOp runs `takt next`, records the op kind, and — under replay — checks
// the call was safe to repeat.
func (d *driver) nextOp() map[string]any {
	d.t.Helper()
	code, o, errb := d.cmd("next", "--slug", "demo")
	if code != 0 {
		d.t.Fatalf("next: %d %s", code, errb)
	}
	if d.replay {
		d.assertReplay(o)
	}
	kind, ok := o["op"].(string)
	if !ok {
		d.t.Fatalf("next printed no op: %v", o)
	}
	d.ops = append(d.ops, kind)
	return o
}

// assertReplay re-runs `takt next` and checks the run is none the worse for
// it. Two shapes satisfy spec §5.4 here. After a wave dispatch the loop has
// launched real agents and written active_wave, so repeating the call must
// not re-launch them: spec §5.3 row 13 makes the second call wait, and the
// in-flight wave must come back untouched. Every other op — `run`, `exec`,
// `ask`, and the planner/auditor dispatch, which only renders a brief file —
// leaves nothing behind that could change the answer, so the two ops must be
// byte-identical.
func (d *driver) assertReplay(first map[string]any) {
	d.t.Helper()
	before := d.activeWave()
	code, second, errb := d.cmd("next", "--slug", "demo")
	if code != 0 {
		d.t.Fatalf("replayed next: %d %s", code, errb)
	}
	if isImplementerDispatch(d.t, first) {
		if second["op"] != "stop" || second["reason"] != "wave_in_flight" {
			d.t.Fatalf("a repeated next during a wave must wait, got %v", second)
		}
		if after := d.activeWave(); after != before {
			d.t.Fatalf("the repeated next disturbed the wave:\n%s\n%s", before, after)
		}
		return
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		d.t.Fatalf("next is not idempotent:\n%s\n%s", a, b)
	}
}

// activeWave is state.active_wave marshalled, so a repeated `next` can be
// checked for having left an in-flight wave exactly as it found it.
func (d *driver) activeWave() string {
	d.t.Helper()
	st, err := bundle.LoadState(d.bdir)
	if err != nil {
		d.t.Fatal(err)
	}
	b, err := json.Marshal(st.ActiveWave)
	if err != nil {
		d.t.Fatal(err)
	}
	return string(b)
}

// play runs the loop until a stop op; it returns the stop reason.
func (d *driver) play(maxSteps int) string {
	d.t.Helper()
	for range maxSteps {
		if reason, stopped := d.step(d.nextOp()); stopped {
			return reason
		}
	}
	d.t.Fatalf("loop did not stop in %d steps: %v", maxSteps, d.ops)
	return ""
}

// step performs one op. It reports the stop reason and true for a stop op.
func (d *driver) step(o map[string]any) (string, bool) {
	d.t.Helper()
	switch o["op"] {
	case "run":
		d.run(o)
	case "exec":
		args := strings.Fields(o["command"].(string))[1:]
		if code, _, errb := d.cmd(args...); code != 0 {
			d.t.Fatalf("exec %v: %s", args, errb)
		}
	case "dispatch":
		d.dispatch(o)
	case "ask":
		d.answer(o)
	case "stop":
		return o["reason"].(string), true
	default:
		d.t.Fatalf("unknown op %v", o["op"])
	}
	return "", false
}

// run writes the artifact the step asks for — at the absolute path the op's
// own inputs name (spec §5.2) — and closes the step.
func (d *driver) run(o map[string]any) {
	d.t.Helper()
	in, ok := o["inputs"].(map[string]any)
	if !ok {
		d.t.Fatalf("run op without inputs: %v", o)
	}
	step, ok := o["step"].(string)
	if !ok {
		d.t.Fatalf("run op without a step: %v", o)
	}
	switch step {
	case "brainstorm":
		writeAt(d.t, in["spec_path"].(string), "# spec\n\n## Assumptions & Open Decisions\n| q | d | r | s |\n")
	case "goals":
		writeAt(d.t, in["goals_path"].(string), goalsMD)
	default:
		d.t.Fatalf("unknown run step %q", step)
	}
	if code, _, errb := d.cmd("done", "--step", step, "--slug", "demo"); code != 0 {
		d.t.Fatalf("done %s: %s", step, errb)
	}
}

// dispatch plays every agent of a dispatch op and records each result.
func (d *driver) dispatch(o map[string]any) {
	d.t.Helper()
	for _, ag := range agentsOf(d.t, o) {
		switch ag["agent"] {
		case "planner":
			d.playPlanner()
		case "alignment-auditor":
			d.playAuditor(ag)
		case "implementer":
			d.playImplementer(o, ag)
		default:
			d.t.Fatalf("unknown agent %v", ag["agent"])
		}
	}
}

// playPlanner writes the fixture plan a planner subagent would have written,
// then records it so takt validates the index itself.
func (d *driver) playPlanner() {
	d.t.Helper()
	testutil.WriteFile(d.t, d.root, "docs/takt/demo/plan.md", "# plan\n")
	specH := specHash(d.t, d.bdir)
	testutil.WriteFile(d.t, d.root, "docs/takt/demo/plan.index.json",
		strings.Replace(validIndex, "%s", specH, 1))
	code, out, errb := d.cmd("record", "--agent", "planner", "--from", d.message("wrote the plan\n"), "--slug", "demo")
	if code != 0 || out["valid"] != true {
		d.t.Fatalf("record planner: %d %v %s", code, out, errb)
	}
}

// playAuditor returns the fixture clause list or verdict list for the mode
// the op asked for, fenced the way an agent's final message carries JSON.
func (d *driver) playAuditor(ag map[string]any) {
	d.t.Helper()
	mode, ok := ag["mode"].(string)
	if !ok {
		d.t.Fatalf("auditor dispatch without a mode: %v", ag)
	}
	body := `{"mode":"clauses","clauses":[{"id":"A1","text":"add a greeting","span":"Add a greeting"}]}`
	if mode == "verdicts" {
		body = `{"mode":"verdicts","verdicts":[{"id":"A1","verdict":"covered","evidence":"tasks 1-2"}]}`
	}
	msg := d.message("here it is:\n```json\n" + body + "\n```\n")
	if code, _, errb := d.cmd("record", "--agent", "alignment-auditor",
		"--mode", mode, "--from", msg, "--slug", "demo"); code != 0 {
		d.t.Fatalf("record auditor %s: %s", mode, errb)
	}
}

// playImplementer creates exactly the files the brief declares and reports
// done at the attempt the dispatch op named.
func (d *driver) playImplementer(o, ag map[string]any) {
	d.t.Helper()
	for _, f := range declaredFiles(d.t, ag["brief"].(string)) {
		testutil.WriteFile(d.t, d.root, f, "package x // written by the scripted implementer\n")
	}
	msg := d.message("STATUS: done\nSUMMARY: implemented\nBLOCKERS: none\n")
	task := strconv.Itoa(int(ag["task"].(float64)))
	attempt := strconv.Itoa(int(o["attempt"].(float64)))
	code, out, errb := d.cmd("record", "--task", task, "--attempt", attempt, "--from", msg, "--slug", "demo")
	if code != 0 || out["ignored"] == true {
		d.t.Fatalf("record task %s attempt %s: %d %v %s", task, attempt, code, out, errb)
	}
}

// answer takes the first option, which every gate lists as the recommended
// one — "confirm" for alignment_confirm, "retry" for the wave gates
// (internal/decide/questions.go).
func (d *driver) answer(o map[string]any) {
	d.t.Helper()
	opts, ok := o["options"].([]any)
	if !ok || len(opts) == 0 {
		d.t.Fatalf("ask op without options: %v", o)
	}
	choice := opts[0].(map[string]any)["choice"].(string)
	gate := o["gate"].(string)
	if code, _, errb := d.cmd("answer", "--gate", gate, "--choice", choice, "--slug", "demo"); code != 0 {
		d.t.Fatalf("answer %s=%s: %s", gate, choice, errb)
	}
}

// message writes an agent's final message to a scratch file and returns its
// path, which is what `takt record --from` consumes.
func (d *driver) message(body string) string {
	d.t.Helper()
	p := filepath.Join(d.t.TempDir(), "msg.txt")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		d.t.Fatal(err)
	}
	return p
}

// isImplementerDispatch separates a wave dispatch — which launched agents
// and wrote active_wave — from the planner/auditor dispatch, which renders a
// brief and mutates nothing.
func isImplementerDispatch(t *testing.T, o map[string]any) bool {
	t.Helper()
	if o["op"] != "dispatch" {
		return false
	}
	agents := agentsOf(t, o)
	if len(agents) == 0 {
		t.Fatalf("dispatch op with no agents: %v", o)
	}
	return agents[0]["agent"] == "implementer"
}

// declaredFiles reads the paths an implementer brief allows the agent to
// touch. templates/implementer.md renders them as a "- <path>" list under
// "## Files you may change (and only these)", and the section ends at the
// next "## " heading — the brief's Verify and Goals lists are "- " lines too,
// so the section boundaries, not the line shape, are what select them.
func declaredFiles(t *testing.T, briefPath string) []string {
	t.Helper()
	b, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	inSection := false
	for line := range strings.SplitSeq(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "## Files you may change"):
			inSection = true
		case inSection && strings.HasPrefix(line, "## "):
			inSection = false
		case inSection && strings.HasPrefix(line, "- "):
			files = append(files, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		}
	}
	if len(files) == 0 {
		t.Fatalf("no declared files in %s:\n%s", briefPath, b)
	}
	return files
}

// writeAt writes content to an absolute path an op named; op paths are
// absolute so the session may run from any cwd (spec §5.2).
func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if !filepath.IsAbs(path) {
		t.Fatalf("op paths must be absolute, got %q", path)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestOpLoopEndToEndWithFakeReviewer drives one whole run through cli.Main
// the way the command prompt will: init → brainstorm → goals → spec review →
// planner → plan review → alignment (clauses, confirm, verdicts) → load →
// wave 0 → wave 1 → finish. Every `next` is run twice (replay), so the run
// also proves spec §5.4's idempotency along the way.
func TestOpLoopEndToEndWithFakeReviewer(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"}, replay: true}

	if reason := d.play(60); reason != "finish_not_implemented" {
		t.Fatalf("stop reason = %s (ops: %v)", reason, d.ops)
	}

	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseFinish || st.ActiveWave != nil {
		t.Fatalf("phase=%s active=%+v", st.Phase, st.ActiveWave)
	}
	for _, tk := range st.Tasks {
		if tk.Status != bundle.StatusDone {
			t.Fatalf("task %d is %s", tk.ID, tk.Status)
		}
	}
	log := testutil.Git(t, root, "log", "--format=%s")
	for _, want := range []string{"wave 0 — tasks 1", "wave 1 — tasks 2", "plan → execute", "brainstorm → plan"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing commit %q in:\n%s", want, log)
		}
	}
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("tree not clean: %q", status)
	}
	// Review I4 / spec §13: reviewer logs quote repo content and are
	// gitignored. Every takt commit stages the bundle tree wholesale, so
	// this is the assertion that keeps them out of it — the ignore file
	// itself is the one thing under logs/ that is tracked, so that the rule
	// travels with the bundle instead of living only on the machine that ran
	// init.
	if logs := testutil.Git(t, root, "ls-files", "docs/takt/demo/logs"); logs != "docs/takt/demo/logs/.gitignore" {
		t.Fatalf("logs/ must hold exactly the tracked ignore rule, got %q", logs)
	}
	if _, serr := os.Stat(filepath.Join(bdir, "logs")); serr != nil {
		t.Fatalf("the walk must have written reviewer logs at all: %v", serr)
	}
	assertCloneIgnoresLogs(t, root)
	joined := strings.Join(d.ops, " ")
	for _, kind := range []string{"run", "exec", "dispatch", "ask"} {
		if !strings.Contains(joined, kind) {
			t.Errorf("op kind %s never seen: %s", kind, joined)
		}
	}
}

// TestOpLoopSurvivesACrashAfterDispatch kills the session between a wave
// dispatch and the digests it was supposed to record, and checks a fresh
// session can pick the run up: it is asked before taking the lock, `--force`
// takes it over and recovers the wave at the next attempt, and the run then
// finishes normally with the recovery on the record (spec §5.4).
func TestOpLoopSurvivesACrashAfterDispatch(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	a := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "A"}}

	// Session A drives until the first implementer dispatch, then "crashes"
	// without recording anything.
	crashed := false
	for range 40 {
		o := a.nextOp()
		if isImplementerDispatch(t, o) {
			crashed = true
			break
		}
		if _, stopped := a.step(o); stopped {
			t.Fatalf("session A stopped before dispatching a wave: %v", a.ops)
		}
	}
	if !crashed {
		t.Fatalf("session A never reached a wave dispatch: %v", a.ops)
	}

	// Session B is an outsider: it must be asked before it takes the run over.
	b := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "B"}}
	if code, o, errb := b.cmd("next", "--slug", "demo"); code != 0 || o["gate"] != "owner" {
		t.Fatalf("outsider must be asked: %d %v %s", code, o, errb)
	}
	code, o, errb := b.cmd("next", "--slug", "demo", "--force")
	if code != 0 || o["op"] != "dispatch" || o["attempt"] != float64(2) {
		t.Fatalf("recovery re-dispatch: %d %v %s", code, o, errb)
	}
	b.ops = append(b.ops, "dispatch")
	b.dispatch(o)

	if reason := b.play(40); reason != "finish_not_implemented" {
		t.Fatalf("stop reason = %s (ops: %v)", reason, b.ops)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseFinish {
		t.Fatalf("phase = %s", st.Phase)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, e := range events {
		if e.Type == "recovered" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("recovery must be recorded")
	}
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("tree not clean after a recovered run: %q", status)
	}
}

// TestDisabledReviewsRecordDisabledGates covers review I6: state.gates was
// stamped "ok" by the phase transition itself, so a run started with
// --no-review-spec / --no-review-plan claimed two reviews that never
// happened — and `takt doctor` then reported the missing receipts as ERRORs,
// telling the user to run the reviews they had switched off. The gate value
// now comes from the receipt, and `disabled` is what a switched-off review
// records.
func TestDisabledReviewsRecordDisabledGates(t *testing.T) {
	t.Parallel()
	root, bdir := setupRunWith(t, "--no-review-spec", "--no-review-plan")
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"}}
	if reason := d.play(60); reason != "finish_not_implemented" {
		t.Fatalf("stop reason = %s (ops: %v)", reason, d.ops)
	}
	for _, g := range []string{"spec", "plan"} {
		if _, err := os.Stat(filepath.Join(bdir, "gates", g+".json")); err == nil {
			t.Fatalf("a switched-off review must never take a receipt: gates/%s.json exists", g)
		}
	}
	code, out, errb := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("status: %d %s", code, errb)
	}
	gates, ok := out["gates"].(map[string]any)
	if !ok || gates["spec"] != "disabled" || gates["plan"] != "disabled" {
		t.Fatalf("a run with the reviews off must record them as disabled, got %v", out["gates"])
	}
	dcode, findings, derrb := runIn(t, root, nil, "doctor", "--json")
	if dcode != 0 {
		t.Fatalf("doctor must not ask for a review this run switched off: %d %v %s", dcode, findings, derrb)
	}
}

// assertCloneIgnoresLogs is the other half of the tracked-ignore rule: clone
// the repo somewhere fresh, drop a reviewer log into the bundle's logs
// directory, and the clone must still consider its tree clean. An ignore
// file that ignored itself would never have been committed, so the clone
// would have no rule at all and the next review there would commit its logs.
func assertCloneIgnoresLogs(t *testing.T, root string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, root, "clone", "--quiet", root, clone)
	rule, err := os.ReadFile(filepath.Join(clone, "docs", "takt", "demo", "logs", ".gitignore"))
	if err != nil {
		t.Fatalf("the ignore rule must travel with the bundle: %v", err)
	}
	if string(rule) != "*\n!.gitignore\n" {
		t.Fatalf("logs/.gitignore = %q", rule)
	}
	testutil.WriteFile(t, clone, "docs/takt/demo/logs/x.stdout", "reviewer output\n")
	if status := testutil.Git(t, clone, "status", "--porcelain"); status != "" {
		t.Fatalf("a reviewer log in a fresh clone must be ignored, got %q", status)
	}
}
