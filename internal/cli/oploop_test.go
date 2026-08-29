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
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
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
	// lensFinding, when set, is one finding the driver's scripted reply for
	// this lens reports on every dispatch of it — real coverage for the
	// verifier dispatch, which only fires once the merged candidate list is
	// non-empty (decide.InternalFacts.Done, two-layers design §4.2). Every
	// other lens, and every driver that leaves this nil, keeps reporting
	// none, so this is opt-in per test.
	lensFinding *lensFindingScript
}

// lensFindingScript is one finding playReviewer's scripted lens reply
// reports, so a test can force a real candidate — and so a real verifier
// dispatch — instead of the driver's default zero-findings reply, which
// leaves the merged candidate list empty and the verifier never dispatched.
type lensFindingScript struct {
	lens                          string
	severity, file, title, detail string
	line                          int
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

// driveToSpecGate drives the loop, executing every op via step, until `next`
// asks the gate_review gate for the spec gate, then returns that ask op
// without acting on it — Task 9's two tests each answer and edit spec.md
// differently (a non-blocking rework closes on the edit alone; a blocking
// one needs a second, scripted pass), so this stops at the one point their
// shapes diverge and leaves the rest to the caller.
func (d *driver) driveToSpecGate(maxSteps int) map[string]any {
	d.t.Helper()
	for range maxSteps {
		o := d.nextOp()
		if o["op"] == "ask" && o["gate"] == "gate_review" {
			return o
		}
		if reason, stopped := d.step(o); stopped {
			d.t.Fatalf("loop stopped (%s) before reaching the spec gate: %v", reason, d.ops)
		}
	}
	d.t.Fatalf("loop did not reach the spec gate in %d steps: %v", maxSteps, d.ops)
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
		case "reviewer":
			d.playReviewer(o, ag)
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
	// ok:true, not just exit 0: a malformed reply is reported as
	// {valid:false, problems} at exit 0 too, and a driver that only looked at
	// the exit code would drive the loop in a circle rather than fail here.
	if code, out, errb := d.cmd("record", "--agent", "alignment-auditor",
		"--mode", mode, "--from", msg, "--slug", "demo"); code != 0 || out["ok"] != true {
		d.t.Fatalf("record auditor %s: %d %v %s", mode, code, out, errb)
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
	final := "STATUS: done\nSUMMARY: implemented\nBLOCKERS: none\n"
	if d.implement == nil {
		for _, f := range declaredFiles(d.t, ag["brief"].(string)) {
			testutil.WriteFile(d.t, d.root, f, "package x // written by the scripted implementer\n")
		}
	} else {
		b, err := os.ReadFile(ag["brief"].(string))
		if err != nil {
			d.t.Fatal(err)
		}
		if final, err = d.implement(string(b), d.root, opText(ag["model"])); err != nil {
			d.t.Fatalf("implementer for task %v: %v", ag["task"], err)
		}
	}
	msg := d.message(final)
	task := strconv.Itoa(int(ag["task"].(float64)))
	attempt := strconv.Itoa(int(o["attempt"].(float64)))
	code, out, errb := d.cmd("record", "--task", task, "--attempt", attempt, "--from", msg, "--slug", "demo")
	if code != 0 || out["ignored"] == true {
		// The message is quoted, not just pointed at: it is what takt just
		// refused, and under a live hook the file it came from is in a
		// temporary directory the reader may no longer have.
		d.t.Fatalf("record task %s attempt %s: %d %v %s\nthe agent's final message was:\n%s",
			task, attempt, code, out, errb, final)
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

// playReviewer answers one dispatch of the internal review layer — a lens,
// or the verifier — the way the scripted walk exercises it: a lens reports
// no findings, so the merged candidate list stays empty and the verifier is
// never dispatched (decide.InternalFacts.Done, two-layers design §4.2); on
// the rare path where the verifier is dispatched anyway, every candidate the
// brief quotes comes back false_positive, its id read from the brief rather
// than assumed (candidateID is shared with execute_test.go's drainReview).
func (d *driver) playReviewer(o, ag map[string]any) {
	d.t.Helper()
	mode, ok := ag["mode"].(string)
	if !ok {
		d.t.Fatalf("reviewer dispatch without a mode: %v", ag)
	}
	var body string
	if mode == "verify" {
		b, err := os.ReadFile(ag["brief"].(string))
		if err != nil {
			d.t.Fatal(err)
		}
		ids := slices.Compact(slices.Sorted(slices.Values(candidateID.FindAllString(string(b), -1))))
		verdicts := make([]string, 0, len(ids))
		for _, id := range ids {
			verdicts = append(verdicts, fmt.Sprintf(
				`{"id":%q,"verdict":"false_positive","evidence":"scripted: no defect found"}`, id))
		}
		body = "```json\n{\"mode\":\"verify\",\"verdicts\":[" + strings.Join(verdicts, ",") + "]}\n```\n"
	} else {
		findings := ""
		if f := d.lensFinding; f != nil && f.lens == mode {
			findings = fmt.Sprintf(`{"severity":%q,"file":%q,"line":%d,"title":%q,"detail":%q}`,
				f.severity, f.file, f.line, f.title, f.detail)
		}
		body = fmt.Sprintf("```json\n{\"lens\":%q,\"findings\":[%s]}\n```\n", mode, findings)
	}
	msg := d.message(body)
	attempt := strconv.Itoa(int(o["attempt"].(float64)))
	code, out, errb := d.cmd("record", "--agent", "reviewer", "--mode", mode,
		"--attempt", attempt, "--from", msg, "--slug", "demo")
	if code != 0 || out["valid"] != true {
		d.t.Fatalf("record reviewer %s: %d %v %s", mode, code, out, errb)
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

// reviewCall is one line of the call log the fake reviewer appends to when
// TAKT_FAKE_REVIEW_CALLS names a file: the rubric a call was rendered from
// and the LogID takt minted for it.
type reviewCall struct{ rubric, logID string }

// reviewCalls parses the whole call log, in call order. Every line is parsed
// and none is skipped, so a test can assert how many backend calls a command
// spent in total — not only that the calls it expected are among them.
func reviewCalls(t *testing.T, path string) []reviewCall {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls []reviewCall
	for line := range strings.SplitSeq(string(b), "\n") {
		if line == "" {
			continue
		}
		rubric, logID, ok := strings.Cut(line, " ")
		if !ok || rubric == "" || logID == "" {
			t.Fatalf("malformed call log line %q in %s", line, b)
		}
		calls = append(calls, reviewCall{rubric: rubric, logID: logID})
	}
	return calls
}

// withRubric keeps the calls made with one rubric, in call order.
func withRubric(calls []reviewCall, rubric string) []reviewCall {
	var out []reviewCall
	for _, c := range calls {
		if c.rubric == rubric {
			out = append(out, c)
		}
	}
	return out
}

// callPrompt reads the prompt log the fake wrote for one recorded call,
// addressed by that call's own LogID. Scanning logs/ instead cannot tell two
// calls of the same rubric apart, and a newest-file heuristic would pick a
// different call on a slow machine.
func callPrompt(t *testing.T, bdir string, c reviewCall) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bdir, "logs", c.logID+".prompt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
	if st.Phase != bundle.PhaseArchived || st.ActiveWave != nil {
		t.Fatalf("phase=%s active=%+v", st.Phase, st.ActiveWave)
	}
	if sess, serr := bundle.ReadSession(bdir); serr != nil || sess != nil {
		t.Fatalf("an archived run holds no session: %+v %v", sess, serr)
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

// TestVerifierDispatchRunsThroughTheDriver covers a review finding: the
// verify op dispatchAgent prints for the reviewer used to carry no Wave or
// Attempt (op.Op's Attempt is omitempty, and only dispatchLenses set it), so
// any driver answering it the way playReviewer/drainReview do — reading
// o["attempt"].(float64) to build the `takt record` call — panicked on a nil
// type assertion instead of running. Every other driver-based test masks
// this: the scripted lens reply reports zero findings, so the merged
// candidate list stays empty and the verifier is never dispatched
// (decide.InternalFacts.Done). Scripting one real finding on wave 0's
// correctness lens, on task 1's own declared file (a.go), forces a genuine
// candidate — and with it a genuine verifier dispatch the driver must
// answer without panicking, all the way to an archived run.
func TestVerifierDispatchRunsThroughTheDriver(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	d := &driver{
		t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"},
		lensFinding: &lensFindingScript{
			lens: "correctness", severity: "major", file: "a.go", line: 1,
			title: "looks off", detail: "scripted finding for coverage",
		},
	}

	if reason := d.play(80); reason != stopArchived {
		t.Fatalf("the run must still end archived, stopped %q (ops: %v)", reason, d.ops)
	}
	if joined := strings.Join(d.ops, " "); !strings.Contains(joined, "dispatch/reviewer") {
		t.Fatalf("the reviewer must have been dispatched at all: %s", joined)
	}
	rec, err := wave.ReadInternalRecord(bdir, 0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("the scripted finding must have driven a real verifier dispatch and record")
	}
	if len(rec.Candidates) != 1 || rec.Candidates[0].ID != "c1" || rec.Candidates[0].File != "a.go" {
		t.Fatalf("candidates = %+v", rec.Candidates)
	}
	if len(rec.Verdicts) != 1 || rec.Verdicts[0].Verdict != wave.VerdictFalsePositive {
		t.Fatalf("verdicts = %+v", rec.Verdicts)
	}
}

// TestSpecGateSpendsOneReviewOnANonBlockingRework is the fixed point, end to
// end: a reviewer that asks for rework over two minors must cost exactly one
// backend call at the spec gate. Before the fixed-point change this looped —
// revise moved the hash, the hash re-armed the gate, and the next pass found
// new things in the new text.
func TestSpecGateSpendsOneReviewOnANonBlockingRework(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	const rework = `{"verdict":"rework","summary":"two minors","findings":[` +
		`{"severity":"minor","file":"spec.md","line":1,"title":"wording","detail":"ambiguous"},` +
		`{"severity":"nit","file":"spec.md","line":2,"title":"typo","detail":"polish"}]}`
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{
		"TAKT_SESSION": "S", "TAKT_FAKE_REVIEW": rework,
	}}

	// Drive until the spec gate is behind us: answer gate_review with revise
	// and edit spec.md, exactly as a session would.
	o := d.driveToSpecGate(20)
	d.answer(o) // "revise" is the first, recommended option (decide/questions.go)
	writeAt(t, filepath.Join(bdir, "spec.md"),
		"# spec\n\nWording tightened per review; typo fixed.\n\n"+
			"## Assumptions & Open Decisions\n| q | d | r | s |\n")
	// The edit is what closes a non-blocking spec gate (fixed-point design
	// §4): one more `next` re-hashes spec.md, sees the revise was accepted
	// at the old (now stale) hash, and is satisfied without a second
	// backend call.
	d.nextOp()

	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if n := gate.Rounds(events, gate.Spec); n != 1 {
		t.Fatalf("spec review calls = %d, want exactly 1", n)
	}
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase == bundle.PhaseBrainstorm {
		t.Fatal("the run must leave brainstorm: a non-blocking rework closes on the revise")
	}
}

// TestSpecGateSpendsASecondScopedReviewOnABlockingRework is the other half:
// a blocking finding does buy one more pass, and that pass is the scoped one.
func TestSpecGateSpendsASecondScopedReviewOnABlockingRework(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	const blocking = `{"verdict":"rework","summary":"one blocking","findings":[` +
		`{"severity":"blocking","file":"spec.md","line":1,"title":"wrong claim",` +
		`"detail":"executeRun does not set ActiveWave"}]}`
	callsFile := filepath.Join(t.TempDir(), "calls.log")
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{
		"TAKT_SESSION": "S", "TAKT_FAKE_REVIEW": blocking,
		"TAKT_FAKE_REVIEW_CALLS": callsFile,
	}}
	// Drive to the spec gate, answer revise, edit spec.md, then let the loop
	// take its second pass. Switch d.env["TAKT_FAKE_REVIEW"] to an approve
	// result before that second pass so the run can proceed.
	o := d.driveToSpecGate(20)
	d.answer(o) // "revise" is recommended even when the rework is blocking
	writeAt(t, filepath.Join(bdir, "spec.md"),
		"# spec\n\nexecuteRun now sets ActiveWave; see task 4 for the fix.\n\n"+
			"## Assumptions & Open Decisions\n| q | d | r | s |\n")
	// A blocking rework does not get the edit-closes-the-gate fast path:
	// acceptRevision (cmd_answer.go) writes nothing when the receipt it read
	// carried a blocking finding, so the edit only re-arms the gate. The next
	// pass is a real, scoped backend call (rendered from
	// review-spec-followup.md) — let it approve so the run can proceed.
	d.env["TAKT_FAKE_REVIEW"] = `{"verdict":"approve","summary":"blocking finding addressed"}`
	for range 5 {
		o2 := d.nextOp()
		if o2["op"] == "dispatch" {
			break // the spec gate is satisfied; the loop moved on to planning
		}
		if _, stopped := d.step(o2); stopped {
			t.Fatalf("loop stopped before the second pass closed the gate: %v", d.ops)
		}
	}

	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if n := gate.Rounds(events, gate.Spec); n != 2 {
		t.Fatalf("spec review calls = %d, want 2 (one blocking pass, one scoped confirmation)", n)
	}

	// The fake recorded every call it took, so the second spec pass's prompt
	// log is read by the LogID that call itself carries.
	calls := reviewCalls(t, callsFile)
	spec := withRubric(calls, gate.Spec)
	if len(spec) != 2 {
		t.Fatalf("spec review calls recorded = %d, want 2 (matching the round count): %+v", len(spec), calls)
	}
	if p := callPrompt(t, bdir, spec[1]); !strings.Contains(p, "Do NOT raise new findings") {
		t.Fatalf("the second pass must be rendered from the scoped rubric: %s", p)
	}
}

// TestSpecReviewFailsLoudlyWhenTheCallLogCannotBeWritten covers the recording
// hook the LogID lookup above rests on. When TAKT_FAKE_REVIEW_CALLS names
// something that cannot be appended to, the fake reports an error verdict
// carrying the reason rather than reviewing unrecorded: a call log that
// silently lost a line would leave the test above reading some other call's
// prompt — or none — and a swallowed write error would hide that.
func TestSpecReviewFailsLoudlyWhenTheCallLogCannotBeWritten(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")

	// A directory is the portable unwritable path: opening one for append
	// fails, and it fails before the reviewer has answered anything.
	unwritable := t.TempDir()
	env := map[string]string{"TAKT_FAKE_REVIEW_CALLS": unwritable}
	c, r, e := runIn(t, root, env, "review", "spec", "--slug", "demo")
	if c != 0 || r["verdict"] != "error" {
		t.Fatalf("an unwritable call log must surface as an error verdict: %d %v %s", c, r, e)
	}
	rc, err := gate.ReadReceipt(bdir, gate.Spec)
	if err != nil || rc == nil || rc.Verdict != gate.VerdictError {
		t.Fatalf("the failure must reach the receipt: %+v %v", rc, err)
	}
	if !strings.Contains(rc.Reason, unwritable) {
		t.Fatalf("the reason must name the call log that could not be written: %+v", rc)
	}
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
