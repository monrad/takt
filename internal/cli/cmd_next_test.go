package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/testutil"
)

const fakeCfg = `{"backends":{"reviewer":["fake"]}}`

const goalsMD = "# Goals — demo\n\n## Anchor\n```text\nAdd a greeting\n```\n\n## Goals\n" +
	"- G1 — greet works · signal: test · evidence: go test ./...\n"

const validIndex = `{"schema":1,"spec_hash":"%s","tasks":[
 {"id":1,"title":"a","description":"add a","files":["a.go"],"verify":["true"],"depends_on":[],"goals":["G1"],"class":"bounded"},
 {"id":2,"title":"b","description":"add b","files":["b.go"],"verify":["true"],"depends_on":[1],"goals":["G1"],"class":"implement"}]}`

// goalsHash is the hash validateOpts binds a plan's spec_hash to.
func goalsHash(b []byte) string { return cli.GoalsHash(b) }

func setupRun(t *testing.T) (string, string) {
	t.Helper()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, ".takt.json", fakeCfg)
	testutil.Commit(t, root, "config")
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "Add a greeting"); code != 0 {
		t.Fatal(errb)
	}
	return root, filepath.Join(root, "docs", "takt", "demo")
}

func next(t *testing.T, root string, env map[string]string, extra ...string) (int, map[string]any, string) {
	t.Helper()
	return runIn(t, root, env, append([]string{"next", "--slug", "demo"}, extra...)...)
}

func specHash(t *testing.T, bdir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bdir, "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]string{"h": goalsHash(b)})
	var m map[string]string
	_ = json.Unmarshal(out, &m)
	return m["h"]
}

// TestNextWalksBrainstormAndPlan drives one scripted run through the whole
// brainstorm→plan phase. Tasks 7 and 9 extend it in place, so it stays one
// sequence rather than being split into helpers.
//
//nolint:gocognit,gocyclo,cyclop // one long scripted walk, deliberately not split
func TestNextWalksBrainstormAndPlan(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)

	code, o, errb := next(t, root, nil)
	if code != 0 || o["op"] != "run" || o["step"] != "brainstorm" {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if !strings.Contains(o["instructions"].(string), "superpowers:brainstorming") ||
		!strings.HasPrefix(o["inputs"].(map[string]any)["spec_path"].(string), "/") {
		t.Fatalf("run op = %v", o)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md",
		"# spec\n\n## Assumptions & Open Decisions\n| q | d | r | s |\n")
	if c, _, e := runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}

	if _, o, _ = next(t, root, nil); o["step"] != "goals" {
		t.Fatalf("expected run goals, got %v", o)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	if c, _, e := runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}
	st, _ := bundle.LoadState(bdir)
	if st.GoalsHash == nil {
		t.Fatal("goals must be frozen")
	}

	if _, o, _ = next(
		t,
		root,
		nil,
	); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt review spec") {
		t.Fatalf("expected exec review spec, got %v", o)
	}
	if c, r, e := runIn(t, root, nil, "review", "spec", "--slug", "demo"); c != 0 || r["verdict"] != "approve" {
		t.Fatalf("%d %v %s", c, r, e)
	}
	if _, err := os.Stat(filepath.Join(bdir, "gates", "spec.json")); err != nil {
		t.Fatal("receipt missing")
	}

	if _, o, _ = next(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("expected dispatch planner, got %v", o)
	}
	agents := o["agents"].([]any)
	ag := agents[0].(map[string]any)
	if ag["agent"] != "planner" || ag["model"] != "fable" || !strings.HasPrefix(ag["brief"].(string), "/") {
		t.Fatalf("planner agent = %v", ag)
	}
	if b, err := os.ReadFile(ag["brief"].(string)); err != nil || !strings.Contains(string(b), "plan.index.json") {
		t.Fatalf("brief unreadable: %v", err)
	}
	st, _ = bundle.LoadState(bdir)
	if st.Phase != bundle.PhasePlan {
		t.Fatalf("phase = %s", st.Phase)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.Contains(msg, "brainstorm → plan") {
		t.Fatalf("transition must be committed: %q", msg)
	}

	// Planner writes the artifacts; record validates them.
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan\n")
	specH := specHash(t, bdir)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", strings.Replace(validIndex, "%s", specH, 1))
	if c, r, e := runIn(
		t,
		root,
		nil,
		"record",
		"--agent",
		"planner",
		"--from",
		"/dev/null",
		"--slug",
		"demo",
	); c != 0 ||
		r["valid"] != true {
		t.Fatalf("%d %v %s", c, r, e)
	}

	if _, o, _ = next(
		t,
		root,
		nil,
	); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt review plan") {
		t.Fatalf("expected exec review plan, got %v", o)
	}
	if c, _, e := runIn(t, root, nil, "review", "plan", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}

	_, o, _ = next(t, root, nil)
	ag = o["agents"].([]any)[0].(map[string]any)
	if ag["agent"] != "alignment-auditor" || ag["mode"] != "clauses" {
		t.Fatalf("expected auditor clauses, got %v", o)
	}
	out := filepath.Join(t.TempDir(), "clauses.txt")
	_ = os.WriteFile(out, []byte("here:\n```json\n"+
		`{"mode":"clauses","clauses":[{"id":"A1","text":"add a greeting","span":"Add a greeting"}]}`+
		"\n```\n"), 0o600)
	if c, _, e := runIn(t, root, nil,
		"record", "--agent", "alignment-auditor", "--mode", "clauses", "--from", out, "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}

	if _, o, _ = next(t, root, nil); o["op"] != "ask" || o["gate"] != "alignment_confirm" {
		t.Fatalf("expected ask alignment_confirm, got %v", o)
	}
	if _, o2, _ := next(t, root, nil); o2["gate"] != "alignment_confirm" {
		t.Fatal("a pending gate must be re-rendered identically")
	}
	if c, _, e := runIn(t, root, nil,
		"answer", "--gate", "alignment_confirm", "--choice", "confirm", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}
	_, o, _ = next(t, root, nil)
	if ag = o["agents"].([]any)[0].(map[string]any); ag["mode"] != "verdicts" {
		t.Fatalf("expected auditor verdicts, got %v", o)
	}
	_ = os.WriteFile(out, []byte("```json\n"+
		`{"mode":"verdicts","verdicts":[{"id":"A1","verdict":"covered","evidence":"task 1"}]}`+
		"\n```\n"), 0o600)
	if c, _, e := runIn(t, root, nil,
		"record", "--agent", "alignment-auditor", "--mode", "verdicts", "--from", out, "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}

	// Load: next materialises the tasks and moves to execute. (Until Task 7
	// wires the launch, the loop then fails loudly — the state is what we assert.)
	next(t, root, nil)
	st, _ = bundle.LoadState(bdir)
	if st.Phase != bundle.PhaseExecute || len(st.Tasks) != 2 || st.Tasks[1].Wave != 1 ||
		st.Tasks[0].Class != "bounded" {
		t.Fatalf("after load: phase=%s tasks=%+v", st.Phase, st.Tasks)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.Contains(msg, "plan → execute") {
		t.Fatalf("load must be committed: %q", msg)
	}
	idx, _ := os.ReadFile(filepath.Join(bdir, "plan.index.json"))
	if !strings.Contains(string(idx), `"wave": 1`) {
		t.Fatal("waves are written back into the index for display")
	}
}

func TestReviewReworkOpensGateAndOverrideClearsIt(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")
	env := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"too vague",` +
		`"findings":[{"severity":"major","file":"spec.md","line":1,"title":"vague","detail":"say more"}]}`}
	if c, r, _ := runIn(t, root, env, "review", "spec", "--slug", "demo"); c != 0 || r["verdict"] != "rework" {
		t.Fatalf("%d %v", c, r)
	}
	if b, _ := os.ReadFile(filepath.Join(bdir, "reviews", "spec.md")); !strings.Contains(string(b), "vague") {
		t.Fatalf("findings file: %q", b)
	}
	_, o, _ := next(t, root, nil)
	if o["op"] != "ask" || o["gate"] != "gate_review" {
		t.Fatalf("%v", o)
	}
	if c, _, e := runIn(t, root, nil,
		"answer", "--gate", "gate_review", "--choice", "accept", "--slug", "demo"); c == 0 {
		t.Fatal("accept without --reason must fail", e)
	}
	if c, _, e := runIn(t, root, nil,
		"answer", "--gate", "gate_review", "--choice", "accept", "--reason", "known gap", "--slug", "demo"); c != 0 {
		t.Fatal(e)
	}
	_, o, _ = next(t, root, nil)
	if o["op"] != "dispatch" {
		t.Fatalf("override must satisfy the gate and move to plan: %v", o)
	}
	// Editing the spec re-arms the gate despite the override.
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec v2\n")
	st, _ := bundle.LoadState(bdir)
	st.Phase = bundle.PhaseBrainstorm
	_ = bundle.SaveState(bdir, st)
	if _, o, _ = next(t, root, nil); o["op"] != "exec" {
		t.Fatalf("edited spec must re-arm the gate: %v", o)
	}
}

func TestReviewSkipNeedsEvidence(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	if c, _, _ := runIn(t, root, nil,
		"review", "spec", "--skip", "--reason", "copilot down", "--slug", "demo"); c == 0 {
		t.Fatal("skip without --evidence must fail")
	}
	ev := filepath.Join(t.TempDir(), "err.txt")
	_ = os.WriteFile(ev, []byte("copilot: connection refused\n"), 0o600)
	if c, r, e := runIn(t, root, nil,
		"review", "spec", "--skip", "--reason", "copilot down", "--evidence", ev, "--slug", "demo"); c != 0 ||
		r["verdict"] != "skipped" {
		t.Fatalf("%d %v %s", c, r, e)
	}
	if _, err := os.Stat(filepath.Join(bdir, "gates", "spec.json")); err != nil {
		t.Fatal(err)
	}
}

func TestNextSessionLock(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	a := map[string]string{"TAKT_SESSION": "A"}
	b := map[string]string{"TAKT_SESSION": "B"}
	if code, o, _ := next(t, root, a); code != 0 || o["op"] != "run" {
		t.Fatalf("%d %v", code, o)
	}
	if code, o, _ := next(t, root, b); code != 0 || o["op"] != "ask" || o["gate"] != "owner" {
		t.Fatalf("second session must be asked: %d %v", code, o)
	}
	if _, o, _ := next(t, root, b, "--force"); o["op"] != "run" {
		t.Fatalf("--force takes over: %v", o)
	}
	if _, o, _ := next(t, root, a); o["gate"] != "owner" {
		t.Fatal("the original session is now the outsider")
	}
	if code, _, errb := runIn(t, root, a, "unlock", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if _, o, _ := next(t, root, a); o["op"] != "run" {
		t.Fatalf("after unlock any session may drive: %v", o)
	}
}

func TestDoneGoalsRequiresVerbatimAnchor(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	testutil.WriteFile(
		t,
		root,
		"docs/takt/demo/goals.md",
		strings.Replace(goalsMD, "Add a greeting", "Add greeting", 1),
	)
	if code, _, errb := runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo"); code != 1 ||
		!strings.Contains(errb, "anchor") {
		t.Fatalf("%d %s", code, errb)
	}
}

func TestGoalsAmendRearmsSpecGate(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")
	runIn(t, root, nil, "review", "spec", "--slug", "demo")
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("%v", o)
	}
	st, _ := bundle.LoadState(bdir)
	st.Phase = bundle.PhaseBrainstorm
	_ = bundle.SaveState(bdir, st)
	testutil.WriteFile(
		t,
		root,
		"docs/takt/demo/goals.md",
		goalsMD+"- G2 — docs updated · signal: docs · evidence: README\n",
	)
	if _, o, _ := next(t, root, nil); o["step"] != "goals" {
		t.Fatalf("an edited goals.md is not frozen until amended: %v", o)
	}
	if code, _, errb := runIn(t, root, nil, "goals", "amend", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if _, o, _ := next(t, root, nil); o["op"] != "exec" {
		t.Fatalf("amended goals re-arm the spec gate: %v", o)
	}
}
