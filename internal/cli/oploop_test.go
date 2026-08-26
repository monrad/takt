package cli_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	// takeover is replay across a session boundary: at every op boundary a
	// fresh named session takes the run over with --force and must be handed
	// the op its predecessor was looking at (spec §14, G1). See
	// assertTakeoverReplay; the live end-to-end test is what sets it.
	takeover bool
	// sessions counts the named sessions takeover has burned through, so
	// every takeover presents an id this run has never seen.
	sessions int
	// implement, when set, is what plays one implementer of a wave dispatch:
	// it is handed the brief the op named (its contents, not its path), the
	// repository root, and the model the op picked for that task, and
	// returns the agent's final message. The model travels with the brief so
	// that the agent which really runs is the one takt said it dispatched —
	// see TestImplementHookGetsTheOpsModel. The live end-to-end test wires a
	// real `claude -p` run in here. Nil keeps the scripted stand-in, which
	// writes the declared files itself.
	implement func(brief, repo, model string) (string, error)
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
	if d.takeover {
		d.assertTakeoverReplay(o)
	}
	if _, ok := o["op"].(string); !ok {
		d.t.Fatalf("next printed no op: %v", o)
	}
	d.ops = append(d.ops, opLabel(d.t, o))
	return o
}

// opLabel names an op the way the walk reads best: the kind, and the thing
// that kind is about — the step of a run, the agent of a dispatch, the gate
// of an ask, the reason of a stop. The kind is always the prefix, so a test
// asking whether a kind was ever seen still looks for the bare word.
func opLabel(t *testing.T, o map[string]any) string {
	t.Helper()
	kind, _ := o["op"].(string)
	switch kind {
	case "run":
		return kind + "/" + opText(o["step"])
	case "dispatch":
		ags := agentsOf(t, o)
		if len(ags) == 0 {
			t.Fatalf("dispatch op with no agents: %v", o)
		}
		return kind + "/" + opText(ags[0]["agent"])
	case "ask":
		return kind + "/" + opText(o["gate"])
	case "stop":
		return kind + "/" + opText(o["reason"])
	}
	return kind
}

// opText is a field of an op as text, so a malformed op labels itself rather
// than panicking on the way to the assertion that will report it.
func opText(v any) string {
	s, _ := v.(string)
	return s
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

// assertTakeoverReplay is spec §14's kill/resume at an op boundary: the
// session that just asked what to do next goes away, and a fresh one takes
// the run over with `--force` and must be handed the very op its predecessor
// was looking at. What is compared is the op JSON, not the bundle: a
// takeover appends a lock_taken event and stamps a new holder on
// state.json, so the bytes on disk are *expected* to differ — the point is
// that nothing about the answer came from the session that asked.
//
// The session that took over keeps driving: it holds the lock now, so the
// `done`, `record` and `answer` calls that execute the op have to come from
// it.
//
// A wave dispatch is the one op boundary this cannot be taken at. The wave
// is in flight the moment `next` printed the op, so a takeover there is a
// recovery re-dispatch rather than the same op again
// (TestOpLoopSurvivesACrashAfterDispatch covers that shape) — and against
// live agents it would run every implementer of the wave a second time.
func (d *driver) assertTakeoverReplay(first map[string]any) {
	d.t.Helper()
	if isImplementerDispatch(d.t, first) {
		return
	}
	d.sessions++
	d.env["TAKT_SESSION"] = fmt.Sprintf("takeover-%d", d.sessions)
	code, second, errb := d.cmd("next", "--slug", "demo", "--force")
	if code != 0 {
		d.t.Fatalf("takeover next: %d %s", code, errb)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		d.t.Fatalf("a fresh session must re-derive the same op:\n%s\n%s", a, b)
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

// playToFinish drives the loop, executing every op via step, until the run
// reaches finish (the exec op for `takt verify`), then returns that op
// without executing it — `takt verify` is a later plan-3 task, so driving
// `step` on it here would fail. Callers that only need "the run reached
// finish" use this.
func (d *driver) playToFinish(maxSteps int) map[string]any {
	d.t.Helper()
	for range maxSteps {
		o := d.nextOp()
		if o["op"] == "exec" && strings.Contains(o["command"].(string), "takt verify") {
			return o
		}
		if reason, stopped := d.step(o); stopped {
			d.t.Fatalf("loop stopped (%s) before reaching finish: %v", reason, d.ops)
		}
	}
	d.t.Fatalf("loop did not reach finish in %d steps: %v", maxSteps, d.ops)
	return nil
}

// play drives the loop, executing every op, until one of them is a stop, and
// returns that stop's reason.
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
	case "retro":
		writeAt(d.t, in["retro_path"].(string), d.retrospective(in))
	case "push_pr":
		// push_pr is the one run step whose done line is not the generic
		// one: the pull request URL is what it has instead of an artifact,
		// so it closes itself and the shared tail below is skipped.
		if code, _, errb := d.cmd("done", "--step", "push_pr", "--url", fixturePRURL, "--slug", "demo"); code != 0 {
			d.t.Fatalf("done push_pr: %s", errb)
		}
		return
	default:
		d.t.Fatalf("unknown run step %q", step)
	}
	if code, _, errb := d.cmd("done", "--step", step, "--slug", "demo"); code != 0 {
		d.t.Fatalf("done %s: %s", step, errb)
	}
}

// fixturePRURL is the pull request the scripted session reports opening. It
// is never reached over the network — `takt done --step push_pr` only records
// the string the session hands it.
const fixturePRURL = "https://example.invalid/pr/1"

// stopArchived is the reason a finished run stops with (spec §5.3 row 25).
const stopArchived = "archived"

// retrospective is the retro.md a scripted session writes: a heading and one
// line per fact the run op handed it, read back from inputs_path so the walk
// proves the file `next` wrote is really there and really JSON.
func (d *driver) retrospective(in map[string]any) string {
	d.t.Helper()
	p, ok := in["inputs_path"].(string)
	if !ok {
		d.t.Fatalf("retro op without inputs_path: %v", in)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		d.t.Fatal(err)
	}
	var inputs map[string]any
	if err = json.Unmarshal(raw, &inputs); err != nil {
		d.t.Fatalf("retro inputs are not JSON: %v\n%s", err, raw)
	}
	if len(inputs) == 0 {
		d.t.Fatalf("retro inputs say nothing about the run: %s", raw)
	}
	var b strings.Builder
	b.WriteString("# Retro — demo\n\n")
	for _, k := range slices.Sorted(maps.Keys(inputs)) {
		fmt.Fprintf(&b, "- %s: noted\n", k)
	}
	return b.String()
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
		case "goal-assessor":
			d.playAssessor(ag)
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

// playImplementer plays one implementer of the wave and records what it
// reported at the attempt the dispatch op named. The scripted stand-in
// creates exactly the files the brief declares and reports done; a driver
// carrying an implement hook hands the brief to that instead and records
// whatever final message comes back, touching no files of its own — the
// agent's own edits are the ones under test.
func (d *driver) playImplementer(o, ag map[string]any) {
	d.t.Helper()
	var msg string
	if d.implement == nil {
		for _, f := range declaredFiles(d.t, ag["brief"].(string)) {
			testutil.WriteFile(d.t, d.root, f, "package x // written by the scripted implementer\n")
		}
		msg = d.message("STATUS: done\nSUMMARY: implemented\nBLOCKERS: none\n")
	} else {
		b, err := os.ReadFile(ag["brief"].(string))
		if err != nil {
			d.t.Fatal(err)
		}
		final, err := d.implement(string(b), d.root, opText(ag["model"]))
		if err != nil {
			d.t.Fatalf("implementer for task %v: %v", ag["task"], err)
		}
		msg = d.message(final)
	}
	task := strconv.Itoa(int(ag["task"].(float64)))
	attempt := strconv.Itoa(int(o["attempt"].(float64)))
	code, out, errb := d.cmd("record", "--task", task, "--attempt", attempt, "--from", msg, "--slug", "demo")
	if code != 0 || out["ignored"] == true {
		d.t.Fatalf("record task %s attempt %s: %d %v %s", task, attempt, code, out, errb)
	}
}

// goalID matches the ids the assessor brief asks about — both in the quoted
// goals.md and in the template's own "one entry per goal id (G1 …)" line.
var goalID = regexp.MustCompile(`\bG\d+\b`)

// playAssessor answers the goal-assessor dispatch the way the agent would:
// one verdict per goal id the brief names, fenced as JSON in a final message.
// The ids come from the brief rather than from the fixture, so a run whose
// goals.md grows a goal is assessed on all of them without the driver
// knowing anything about it.
func (d *driver) playAssessor(ag map[string]any) {
	d.t.Helper()
	brief, err := os.ReadFile(ag["brief"].(string))
	if err != nil {
		d.t.Fatal(err)
	}
	ids := slices.Compact(slices.Sorted(slices.Values(goalID.FindAllString(string(brief), -1))))
	if len(ids) == 0 {
		d.t.Fatalf("the assessor brief names no goal: %s", brief)
	}
	verdicts := make([]string, 0, len(ids))
	for _, id := range ids {
		verdicts = append(verdicts, fmt.Sprintf(
			`{"id":%q,"verdict":"achieved","evidence":"a.go and b.go exist","citations":["a.go:1"]}`, id))
	}
	msg := d.message("```json\n[" + strings.Join(verdicts, ",") + "]\n```\n")
	code, out, errb := d.cmd("record", "--agent", "goal-assessor", "--from", msg, "--slug", "demo")
	if code != 0 {
		d.t.Fatalf("record goal-assessor: %d %v %s", code, out, errb)
	}
	// An assessment takt cannot use comes back as {valid:false, problems}
	// at exit 0, exactly as a rejected plan index does (review M1) — so the
	// walk reads the document, not the exit code. The fixture's verdicts
	// are well-formed, so a rejection here is the driver's own bug.
	if out["valid"] == false {
		d.t.Fatalf("the scripted assessment must validate: %v", out)
	}
	if out["all_achieved"] != true {
		d.t.Fatalf("record goal-assessor: %v", out)
	}
}

// answer takes the first option a user could actually pick, which every gate
// lists as the recommended one — "confirm" for alignment_confirm, "retry" for
// the wave gates (internal/decide/questions.go). An option carrying a
// `disabled` reason is skipped rather than chosen: branch_finish leads with
// merge, and in a plain checkout — the primary worktree holding the run
// branch — merge is exactly the one takt cannot do, so the walk lands on pr.
func (d *driver) answer(o map[string]any) {
	d.t.Helper()
	opts, ok := o["options"].([]any)
	if !ok || len(opts) == 0 {
		d.t.Fatalf("ask op without options: %v", o)
	}
	choice := ""
	for _, x := range opts {
		m, isMap := x.(map[string]any)
		if !isMap {
			d.t.Fatalf("option is not an object: %v", x)
		}
		if reason, _ := m["disabled"].(string); reason == "" {
			choice = m["choice"].(string)
			break
		}
	}
	if choice == "" {
		d.t.Fatalf("every option of %v is disabled: %v", o["gate"], opts)
	}
	gate := o["gate"].(string)
	args := []string{"answer", "--gate", gate, "--choice", choice, "--slug", "demo"}
	// The destructive choice takes the slug back as a confirmation. The
	// fixture never reaches it — merge is disabled and pr comes first — but
	// a driver that answered it wrongly would report a takt bug that was its
	// own.
	if gate == "branch_finish" && choice == "discard" {
		args = append(args, "--confirm", "demo")
	}
	if code, _, errb := d.cmd(args...); code != 0 {
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

// recordedModel is the model a task's last digest says ran. `takt status`
// reads the same field back out of the same bytes.
func recordedModel(t *testing.T, tk bundle.Task) string {
	t.Helper()
	var d struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(tk.LastDigest, &d); err != nil {
		t.Fatalf("task %d has no readable digest: %v (%s)", tk.ID, err, tk.LastDigest)
	}
	return d.Model
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
// wave 0 → wave 1 → finish → verify → goal assessment → retro →
// branch_finish → push_pr → archive. Every `next` is run twice (replay), so
// the run also proves spec §5.4's idempotency along the way — including
// through the finish phase, where a repeated call re-derives the retro
// inputs, re-renders the stored gate and re-reads git for the disposition
// rather than replaying a record.
//
//nolint:gocognit // one scripted run, end to end; splitting it would hide the sequence
func TestOpLoopEndToEndWithFakeReviewer(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"}, replay: true}

	if reason := d.play(80); reason != stopArchived {
		t.Fatalf("the run must end archived, stopped %q (ops: %v)", reason, d.ops)
	}

	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseArchived || st.ActiveWave != nil || st.Session != nil {
		t.Fatalf("phase=%s active=%+v session=%+v", st.Phase, st.ActiveWave, st.Session)
	}
	if st.VerifiedSHA == nil || st.GoalsCheckedSHA == nil {
		t.Fatalf("the run must be verified and its goals checked: %v %v", st.VerifiedSHA, st.GoalsCheckedSHA)
	}
	if st.Disposition == nil || st.Disposition.Choice != "pr" ||
		st.Disposition.PRURL == "" || !st.Disposition.Applied {
		t.Fatalf("merge is disabled in a plain checkout, so the walk takes pr: %+v", st.Disposition)
	}
	for _, tk := range st.Tasks {
		if tk.Status != bundle.StatusDone {
			t.Fatalf("task %d is %s", tk.ID, tk.Status)
		}
	}
	log := testutil.Git(t, root, "log", "--format=%s")
	for _, want := range []string{
		"wave 0 — tasks 1", "wave 1 — tasks 2", "plan → execute", "brainstorm → plan",
		"execute → finish", "retro done", "push_pr done", "takt(demo): archive",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("missing commit %q in:\n%s", want, log)
		}
	}
	// The finish records are the run's receipts; the archive commit is what
	// puts them on the branch for whoever reads it later.
	tracked := testutil.Git(t, root, "ls-files")
	for _, want := range []string{"docs/takt/demo/finish/verify.json", "docs/takt/demo/finish/goals.json"} {
		if !strings.Contains(tracked, want) {
			t.Errorf("%s must be committed:\n%s", want, tracked)
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
	for _, want := range []string{
		"run", "exec", "dispatch", "ask", "stop",
		"run/retro", "run/push_pr", "dispatch/goal-assessor", "ask/branch_finish", "stop/archived",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("op %s never seen: %s", want, joined)
		}
	}
	t.Logf("ops: %s", joined)
}

// TestOpLoopFinishSurvivesRestart is spec §5.4 inside the finish phase: the
// session that got the run as far as the retrospective goes away without
// writing one, and a new one picks the run up from the records on disk. It is
// asked before it takes the lock (the run is still held), `--force` hands it
// the very same op the first session was looking at, and it drives the rest
// of the finish phase — retro, disposition, pull request, archive — to the
// end.
func TestOpLoopFinishSurvivesRestart(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	a := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "A"}}

	// Session A drives to the retro run op, then "crashes" without writing
	// the retrospective or calling done.
	var last map[string]any
	for range 60 {
		o := a.nextOp()
		if o["op"] == "run" && o["step"] == "retro" {
			last = o
			break
		}
		if reason, stopped := a.step(o); stopped {
			t.Fatalf("session A stopped (%s) before the retro: %v", reason, a.ops)
		}
	}
	if last == nil {
		t.Fatalf("session A never reached the retro: %v", a.ops)
	}

	b := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "B"}}
	if code, o, errb := b.cmd("next", "--slug", "demo"); code != 0 || o["gate"] != "owner" {
		t.Fatalf("outsider must be asked: %d %v %s", code, o, errb)
	}
	code, o, errb := b.cmd("next", "--slug", "demo", "--force")
	if code != 0 || o["op"] != "run" || o["step"] != "retro" {
		t.Fatalf("finish re-derives the same op from disk: %d %v %s", code, o, errb)
	}
	// Not merely the same step: the same op, to the byte. The finish phase
	// remembers nothing about the session that asked, so the run op a
	// takeover is handed has to be the one its predecessor was looking at.
	was, _ := json.Marshal(last)
	is, _ := json.Marshal(o)
	if string(was) != string(is) {
		t.Fatalf("the taken-over op differs:\n%s\n%s", was, is)
	}
	if reason := b.play(40); reason != stopArchived {
		t.Fatalf("the taken-over run must still end archived, stopped %q (ops: %v)", reason, b.ops)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseArchived || st.VerifiedSHA == nil {
		t.Fatalf("%+v", st)
	}
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("tree not clean: %q", status)
	}
	t.Logf("session A ops: %s\nsession B ops: %s", strings.Join(a.ops, " "), strings.Join(b.ops, " "))
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

	if fo := b.playToFinish(40); fo["op"] != "exec" || !strings.Contains(fo["command"].(string), "takt verify") {
		t.Fatalf("op at finish = %v (ops: %v)", fo, b.ops)
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
	if o := d.playToFinish(60); o["op"] != "exec" || !strings.Contains(o["command"].(string), "takt verify") {
		t.Fatalf("op at finish = %v (ops: %v)", o, d.ops)
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

// byClassCfg pins one task class to a model that is neither the shipped
// default for that class (sonnet) nor the implementer default (opus), so a
// model seen downstream can only have come from the by_class map (spec D22).
const byClassCfg = `{"backends":{"reviewer":["fake"]},` +
	`"agents":{"implementer":{"by_class":{"bounded":"haiku"}}}}`

// TestImplementHookGetsTheOpsModel is the seam the live end-to-end test
// hangs its implementers on: the model `takt next` picked for a task has to
// reach the agent that really runs, and the digest has to record that same
// model. Without the model travelling with the brief, a hook could run on
// anything at all while the digest went on claiming what the config said —
// so `takt status` would name a model that never saw the task.
//
// The config pins class `bounded` to haiku, which validIndex's task 1 is, so
// the model asserted here cannot have come from a default anywhere.
func TestImplementHookGetsTheOpsModel(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, ".takt.json", byClassCfg)
	testutil.Commit(t, root, "config")
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "Add a greeting"); code != 0 {
		t.Fatal(errb)
	}
	bdir := filepath.Join(root, "docs", "takt", "demo")

	var got []string
	d := &driver{
		t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"},
		implement: func(_, _, model string) (string, error) {
			got = append(got, model)
			return "STATUS: done\nSUMMARY: implemented\nBLOCKERS: none\n", nil
		},
	}

	var disp map[string]any
	for range 40 {
		o := d.nextOp()
		if isImplementerDispatch(t, o) {
			disp = o
			break
		}
		if reason, stopped := d.step(o); stopped {
			t.Fatalf("the run stopped (%s) before dispatching a wave: %v", reason, d.ops)
		}
	}
	if disp == nil {
		t.Fatalf("never reached a wave dispatch: %v", d.ops)
	}
	if ag := agentsOf(t, disp)[0]; ag["model"] != "haiku" {
		t.Fatalf("the by_class override must reach the dispatch op: %v", ag)
	}

	d.dispatch(disp)
	if !slices.Equal(got, []string{"haiku"}) {
		t.Fatalf("the hook was handed %v, want the op's own model", got)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	tk := st.Task(1)
	if tk == nil {
		t.Fatal("task 1 is not in the run")
	}
	if m := recordedModel(t, *tk); m != "haiku" {
		t.Fatalf("the digest recorded %q, want the model the agent ran on", m)
	}
}
