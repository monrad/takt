package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/brief"
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

// keyReasonJSON is the event-data key takt records a takeover's reason under.
const keyReasonJSON = "reason"

// keyValidJSON and keyProblemsJSON are the fields `takt record --agent`
// reports a rejected agent message with (spec §5.1).
const (
	keyValidJSON    = "valid"
	keyProblemsJSON = "problems"
)

// goalsHash is the hash validateOpts binds a plan's spec_hash to.
func goalsHash(b []byte) string { return cli.GoalsHash(b) }

func setupRun(t *testing.T) (string, string) {
	t.Helper()
	return setupRunWith(t)
}

// setupRunWith is setupRun with extra `takt init` flags — the --no-review-*
// and --no-alignment switches a run freezes into its config (spec §12).
func setupRunWith(t *testing.T, initFlags ...string) (string, string) {
	t.Helper()
	return setupRunIn(t, testutil.NewRepo(t), initFlags...)
}

// setupRunIn is setupRunWith in a repository the caller built, so a test can
// have the run live somewhere particular.
func setupRunIn(t *testing.T, root string, initFlags ...string) (string, string) {
	t.Helper()
	testutil.WriteFile(t, root, ".takt.json", fakeCfg)
	testutil.Commit(t, root, "config")
	args := append([]string{"init", "--slug", "demo"}, initFlags...)
	if code, _, errb := runIn(t, root, nil, append(args, "Add a greeting")...); code != 0 {
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
	// A malformed auditor reply is the agent's mistake, not takt's, and is
	// reported the way the planner's and the assessor's are: {valid:false,
	// problems} at exit 0, an `alignment_invalid` event, and nothing written
	// — so the next call simply hands the same brief out again.
	for _, bad := range []string{"no json block at all\n", "```json\n{\"mode\":\"clauses\"}\n```\n"} {
		badFile := filepath.Join(t.TempDir(), "bad.txt")
		if werr := os.WriteFile(badFile, []byte(bad), 0o600); werr != nil {
			t.Fatal(werr)
		}
		c, r, e := runIn(t, root, nil, "record", "--agent", "alignment-auditor",
			"--mode", "clauses", "--from", badFile, "--slug", "demo")
		probs, _ := r[keyProblemsJSON].([]any)
		if c != 0 || r[keyValidJSON] != false || len(probs) == 0 {
			t.Fatalf("a malformed auditor reply must report {valid:false, problems} at exit 0: %d %v %s", c, r, e)
		}
	}
	evs, eerr := bundle.ReadEvents(bdir)
	if eerr != nil {
		t.Fatal(eerr)
	}
	if !hasEventType(evs, "alignment_invalid") {
		t.Fatalf("a rejected auditor reply must be on the event log: %+v", evs)
	}
	if _, o, _ = next(t, root, nil); o["op"] != "dispatch" ||
		o["agents"].([]any)[0].(map[string]any)["mode"] != "clauses" {
		t.Fatalf("a rejected reply must leave the auditor dispatch pending: %v", o)
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
	if q, _ := o["question"].(string); !strings.Contains(q, "A1..A1") {
		t.Fatalf("the gate must name the real clause count, got %q", q)
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

	// Load: next materialises the tasks, moves to execute, and — in the same
	// call — launches wave 0, because the load is a side effect the loop
	// decides past rather than an op of its own (spec §5.3 row 17).
	code, o, errb = next(t, root, nil)
	if code != 0 || o["op"] != "dispatch" || o["wave"] != float64(0) {
		t.Fatalf("load must fall through to the wave-0 dispatch: %d %v %s", code, o, errb)
	}
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

// planLoadFixture builds a bundle in phase plan with a valid two-task index,
// plan review and alignment gating both off, so a single `next` reaches the
// load transition (spec §7.3 Load) without walking every earlier gate the
// way TestNextWalksBrainstormAndPlan does. It writes plan.md as well as the
// index because that is what a planner produces (spec §13) — an index
// without it is an incomplete plan, and gatherIndexFacts says so.
func planLoadFixture(t *testing.T) (string, string) {
	t.Helper()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	specH := specHash(t, bdir)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan\n")
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", strings.Replace(validIndex, "%s", specH, 1))
	st, _ := bundle.LoadState(bdir)
	st.Phase = bundle.PhasePlan
	st.Config.Review.Plan = false
	st.Config.Alignment = false
	if err := bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "plan fixture")
	return root, bdir
}

// TestLoadCommitMessageCarriesAlignmentSummary covers fix round 2: spec §7.3
// says the contraction/creep summary belongs "in the load commit message
// and in status" — not status alone. loadCommitMessage must reuse
// statusAlignment/alignmentLine rather than re-deriving the bucketing, so
// this pins the wire format, not a second implementation of it.
func TestLoadCommitMessageCarriesAlignmentSummary(t *testing.T) {
	t.Parallel()
	root, _ := planLoadFixture(t)
	verdicts := filepath.Join(t.TempDir(), "verdicts.txt")
	body := "```json\n" +
		`{"mode":"verdicts","verdicts":[` +
		`{"id":"A1","verdict":"covered","evidence":"task 1"},` +
		`{"id":"A2","verdict":"narrowed","evidence":"scope cut"}]}` +
		"\n```\n"
	if err := os.WriteFile(verdicts, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := runIn(
		t,
		root,
		nil,
		"record",
		"--agent",
		"alignment-auditor",
		"--mode",
		"verdicts",
		"--from",
		verdicts,
		"--slug",
		"demo",
	); code != 0 {
		t.Fatal(errb)
	}
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("load must fall through to the wave-0 dispatch: %v", o)
	}
	log := testutil.Git(t, root, "log", "-1", "--format=%s%n%b")
	if !strings.Contains(log, "plan → execute") {
		t.Fatalf("subject must still say plan → execute (Task 9 asserts this substring): %q", log)
	}
	if !strings.Contains(log, "alignment: 1 covered, 1 narrowed (contraction: A2)") {
		t.Fatalf("commit message must carry the alignment summary: %q", log)
	}
}

// TestLoadCommitMessageOmitsAlignmentWhenAbsent covers the other half: no
// alignment.json at all (alignment disabled, skipped, or never run) must
// leave the load commit message exactly as before — no trailing " — ".
func TestLoadCommitMessageOmitsAlignmentWhenAbsent(t *testing.T) {
	t.Parallel()
	root, _ := planLoadFixture(t)
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("load must fall through to the wave-0 dispatch: %v", o)
	}
	log := testutil.Git(t, root, "log", "-1", "--format=%s%n%b")
	if !strings.Contains(log, "plan → execute") {
		t.Fatalf("subject must still say plan → execute: %q", log)
	}
	if strings.Contains(log, "alignment:") {
		t.Fatalf("no alignment.json — commit message must not mention alignment: %q", log)
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

func TestReviewIsIdempotentAtAHash(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"too vague","findings":[]}`}
	approve := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"approve","summary":"fine","findings":[]}`}
	if c, r, e := runIn(
		t,
		root,
		rework,
		"review",
		"spec",
		"--slug",
		"demo",
	); c != 0 || r["verdict"] != "rework" ||
		r["cached"] != nil {
		t.Fatalf("%d %v %s", c, r, e)
	}
	head := testutil.Git(t, root, "rev-parse", "HEAD")
	first, err := os.ReadFile(filepath.Join(bdir, "gates", "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Same hash, no --force: the receipt answers; the backend does not run
	// (it would approve now), nothing is written, nothing is committed.
	c, r, e := runIn(t, root, approve, "review", "spec", "--slug", "demo")
	if c != 0 || r["cached"] != true || r["verdict"] != "rework" || r["receipt"] != "gates/spec.json" {
		t.Fatalf("cached review: %d %v %s", c, r, e)
	}
	if testutil.Git(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("a cached review must not commit")
	}
	if again, _ := os.ReadFile(filepath.Join(bdir, "gates", "spec.json")); !bytes.Equal(first, again) {
		t.Fatal("a cached review must not rewrite the receipt")
	}
	// --force re-runs at the same hash and commits the new receipt.
	if c, r, e = runIn(
		t,
		root,
		approve,
		"review",
		"spec",
		"--force",
		"--slug",
		"demo",
	); c != 0 || r["cached"] != nil ||
		r["verdict"] != "approve" {
		t.Fatalf("forced review: %d %v %s", c, r, e)
	}
	if testutil.Git(t, root, "rev-parse", "HEAD") == head {
		t.Fatal("a forced review must commit its receipt")
	}
	// An edit changes the hash: the cache does not apply.
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec v2\n")
	if c, r, e = runIn(
		t,
		root,
		rework,
		"review",
		"spec",
		"--slug",
		"demo",
	); c != 0 || r["cached"] != nil ||
		r["verdict"] != "rework" {
		t.Fatalf("review after edit: %d %v %s", c, r, e)
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
	// The evidence is copied into the bundle and recorded by its
	// bundle-relative path, like `findings` (spec §4.5).
	copied, err := os.ReadFile(filepath.Join(bdir, "gates", "spec.evidence.txt"))
	if err != nil || !strings.Contains(string(copied), "connection refused") {
		t.Fatalf("evidence must be preserved in the bundle: %v %q", err, copied)
	}
	var rc struct {
		Skipped struct {
			EvidencePath string `json:"evidence_path"`
		} `json:"skipped"`
	}
	b, _ := os.ReadFile(filepath.Join(bdir, "gates", "spec.json"))
	if err = json.Unmarshal(b, &rc); err != nil {
		t.Fatal(err)
	}
	if rc.Skipped.EvidencePath != "gates/spec.evidence.txt" {
		t.Fatalf("evidence_path = %q, want a bundle-relative path", rc.Skipped.EvidencePath)
	}
}

// TestNextOwnerGateProtectsAnEnvNamedSession covers review finding 1: a
// session id supplied through TAKT_SESSION is a live session even when it
// looks like one takt generated, so a second session must be asked before
// taking the run over.
func TestNextOwnerGateProtectsAnEnvNamedSession(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	live := map[string]string{"TAKT_SESSION": "takt-deadbeefdeadbeef"}
	if code, o, errb := next(t, root, live); code != 0 || o["op"] != "run" {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	code, o, _ := next(t, root, map[string]string{"TAKT_SESSION": "B"})
	if code != 0 || o["op"] != "ask" || o["gate"] != "owner" {
		t.Fatalf("an env-named holder must raise the owner gate: %d %v", code, o)
	}
	// The generated holder init left behind is still taken over silently.
	root2, bdir2 := setupRun(t)
	if _, o2, _ := next(t, root2, live); o2["op"] != "run" {
		t.Fatalf("a generated holder must not block: %v", o2)
	}
	// Silently for the user, but not silently on the record: spec §4.6 has
	// takt record every takeover as an event, and the orphan one — the only
	// one that needs no confirmation — was leaving no trace at all
	// (review M7).
	events, err := bundle.ReadEvents(bdir2)
	if err != nil {
		t.Fatal(err)
	}
	taken := false
	for _, e := range events {
		if e.Type == "lock_taken" && e.Data[keyReasonJSON] == "orphaned" {
			taken = true
		}
	}
	if !taken {
		t.Fatalf("the orphan takeover must be recorded: %+v", events)
	}
}

// TestDoneLeavesUnrelatedStagedFilesAlone covers review finding 2: takt
// commits only the bundle, never whatever the user happened to stage
// (spec §4.7).
func TestDoneLeavesUnrelatedStagedFilesAlone(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	testutil.WriteFile(t, root, "user_wip.txt", "mine\n")
	testutil.Git(t, root, "add", "user_wip.txt")
	if code, _, errb := runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD"); strings.Contains(files, "user_wip") {
		t.Fatalf("takt committed the user's staged file: %q", files)
	}
	if st := testutil.Git(t, root, "status", "--porcelain"); st != "A  user_wip.txt" {
		t.Fatalf("the user's staged file must stay staged: %q", st)
	}
}

// TestNextLeavesTheTreeCleanWhenItOnlyHeartbeats covers the integration bug
// Task 9's end-to-end test found: the session heartbeat lives in state.json,
// which is tracked and committed, so stamping it on every `takt next` left a
// modified state.json behind after any call that decided nothing and
// committed nothing. A run whose last op was a `stop` therefore ended with a
// dirty worktree, and the plan's acceptance test — which replays every op —
// saw it. A call that changes nothing must now write nothing; the lock is
// still refreshed, but only once the holder's own heartbeat has aged past
// half the ttl (bundle.Acquire, LockKept).
func TestNextLeavesTheTreeCleanWhenItOnlyHeartbeats(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	env := map[string]string{"TAKT_SESSION": "S"}
	// The first next takes the lock over from the generated holder init left
	// behind — a real change — and `done` commits the bundle, so the run
	// starts this test with a clean tree.
	if code, o, errb := next(t, root, env); code != 0 || o["op"] != "run" {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	if code, _, errb := runIn(t, root, env, "done", "--step", "brainstorm", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("precondition: %q", status)
	}

	before, err := os.ReadFile(filepath.Join(bdir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if code, o, errb := next(t, root, env); code != 0 || o["step"] != "goals" {
			t.Fatalf("%d %v %s", code, o, errb)
		}
	}
	after, err := os.ReadFile(filepath.Join(bdir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a repeated next rewrote state.json:\n%s\n%s", before, after)
	}
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("repeating next dirtied the tree: %q", status)
	}
	// The lock is still held: keeping the heartbeat must not release the run.
	if _, o, _ := next(t, root, map[string]string{"TAKT_SESSION": "B"}); o["gate"] != "owner" {
		t.Fatalf("the kept lock must still hold the run: %v", o)
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

// TestGateReviewOutlivesTheGitDeadline covers review I5: the spec and plan
// reviews ran under commandContext, the deadline that bounds git (spec §13).
// A gate review is minutes of backend work, so takt was cutting healthy
// reviews off with its own "context deadline exceeded" — and the git work
// that follows the verdict has to be measured from when it starts, not from
// what the reviewer left of the budget.
func TestGateReviewOutlivesTheGitDeadline(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")

	env := map[string]string{"TAKT_GIT_TIMEOUT": "1s", "TAKT_FAKE_REVIEW_SLEEP": "2s"}
	code, out, errb := runIn(t, root, env, "review", "spec", "--slug", "demo")
	if code != 0 || out["verdict"] != "approve" {
		t.Fatalf("a reviewer slower than the git budget must still be heard: %d %v %s", code, out, errb)
	}
	if strings.Contains(errb, "context deadline exceeded") {
		t.Fatalf("stderr = %q", errb)
	}
	if _, err := os.Stat(filepath.Join(bdir, "gates", "spec.json")); err != nil {
		t.Fatal(err)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); !strings.Contains(msg, "spec reviewed: approve") {
		t.Fatalf("the receipt must still be committed: %q", msg)
	}
}

// TestDoneIsANoOpOnADoneStep covers spec §5.4 for `takt done`: replaying the
// call the session already made must not append a second receipt or take an
// empty commit, while editing the artifact and closing the step again is a
// real done, not a replay.
func TestDoneIsANoOpOnADoneStep(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	if code, _, errb := runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	before := testutil.Git(t, root, "rev-parse", "HEAD")
	code, got, _ := runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	if code != 0 || got["ignored"] != true || testutil.Git(t, root, "rev-parse", "HEAD") != before {
		t.Fatalf("%d %v", code, got)
	}
	// an edited artifact is a new done, not a replay
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec v2\n")
	if code, got, _ = runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo"); code != 0 ||
		got["ignored"] == true {
		t.Fatalf("%d %v", code, got)
	}
}

// TestNonTaskBriefsAreStableAcrossReplays covers the planner brief (spec
// §5.4): a replayed dispatch must leave briefs/planner.a1.md byte-identical,
// and an edited input must re-render it with the same delimiter token so the
// diff shows only the change.
func TestNonTaskBriefsAreStableAcrossReplays(t *testing.T) {
	t.Parallel()
	root, bdir := setupRunWith(t, "--no-review-spec", "--no-review-plan", "--no-alignment")
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")
	_, o, _ := next(t, root, nil)
	if o["op"] != "dispatch" {
		t.Fatalf("expected the planner dispatch, got %v", o)
	}
	p := filepath.Join(bdir, "briefs", "planner.a1.md")
	first, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	tok, ok := brief.TokenOf(string(first))
	if !ok {
		t.Fatalf("planner brief carries no delimiter token:\n%s", first)
	}
	if _, o, _ = next(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("replay: %v", o)
	}
	again, _ := os.ReadFile(p)
	if !bytes.Equal(first, again) {
		t.Fatal("a replayed dispatch must leave the brief byte-identical")
	}
	// A changed input re-renders the brief — with the same token, so the
	// diff is the change and nothing else.
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec v2\n")
	next(t, root, nil)
	third, _ := os.ReadFile(p)
	if bytes.Equal(third, again) {
		t.Fatal("an edited spec must re-render the planner brief")
	}
	if got, _ := brief.TokenOf(string(third)); got != tok {
		t.Fatalf("re-render must keep the token: %q != %q", got, tok)
	}
}

// hasEventType reports whether the log holds an event of this type.
func hasEventType(evs []bundle.Event, typ string) bool {
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// TestAuditorRepliesTaktCannotParseAreCappedAtThree covers spec §5.3 rows 10
// and 11 end to end: three rejected replies arm `agent_invalid`, *retry*
// resets the count through `alignment_attempts_reset` and hands the auditor
// a brief that quotes what was wrong with its last one, the cap re-arms, and
// *skip* ends the audit for good.
func TestAuditorRepliesTaktCannotParseAreCappedAtThree(t *testing.T) {
	t.Parallel()
	root, bdir := auditedFixture(t)
	garbage := unusableReply(t)
	for range 3 {
		rejectAuditor(t, root, "clauses", garbage)
	}
	_, o, _ := next(t, root, nil)
	if o["op"] != "ask" || o["gate"] != "agent_invalid" {
		t.Fatalf("three rejections must ask: %v", o)
	}
	ctx, _ := o["context"].(map[string]any)
	if ctx["agent"] != "alignment-auditor" || ctx["attempts"] != 3.0 {
		t.Fatalf("context: %v", ctx)
	}
	answerGate(t, root, "agent_invalid", "retry")
	_, o, _ = next(t, root, nil)
	if o["op"] != "dispatch" {
		t.Fatalf("retry must dispatch the auditor again: %v", o)
	}
	agents, _ := o["agents"].([]any)
	ag, _ := agents[0].(map[string]any)
	b, _ := os.ReadFile(ag["brief"].(string))
	if !strings.Contains(string(b), "Your previous reply was rejected") || !strings.Contains(string(b), "clauses") {
		t.Fatalf("the retried brief must carry the rejection reasons:\n%s", b)
	}
	for range 3 {
		rejectAuditor(t, root, "clauses", garbage)
	}
	if _, o, _ = next(t, root, nil); o["gate"] != "agent_invalid" {
		t.Fatalf("the cap re-arms after a retry: %v", o)
	}
	answerGate(t, root, "agent_invalid", "skip")
	_, o, _ = next(t, root, nil)
	if o["gate"] == "agent_invalid" {
		t.Fatalf("skip must end the audit: %v", o)
	}
	if agents, _ = o["agents"].([]any); len(agents) > 0 {
		if ag, _ = agents[0].(map[string]any); ag["agent"] == "alignment-auditor" {
			t.Fatalf("skip must not dispatch the auditor again: %v", o)
		}
	}
	// The events are the durable record (spec §4.4).
	if n := countEvents(t, bdir, "alignment_invalid"); n != 6 {
		t.Fatalf("six rejected replies, %d on the log", n)
	}
	if resets := eventsOfType(t, bdir, "alignment_attempts_reset"); len(resets) != 1 {
		t.Fatalf("only the retry resets this walk: %+v", resets)
	}
}

// validClauses is a usable `clauses` reply for the "Add a greeting" run the
// fixtures build, fenced the way an agent's final message carries JSON.
const validClauses = "here:\n```json\n" +
	`{"mode":"clauses","clauses":[{"id":"A1","text":"add a greeting","span":"Add a greeting"}]}` +
	"\n```\n"

// TestAValidRecordEndsTheAuditorsAttemptStreak covers the other half of the
// cap: the counter is one counter for both auditor modes, so a reply takt
// could use has to end the streak — otherwise two rejected `clauses`
// replies would arm the gate on the `verdicts` pass's first mistake, and
// the rejection of a reply that was since corrected would be quoted back
// into the next mode's brief.
func TestAValidRecordEndsTheAuditorsAttemptStreak(t *testing.T) {
	t.Parallel()
	root, bdir := auditedFixture(t)
	garbage := unusableReply(t)
	for range 2 {
		rejectAuditor(t, root, "clauses", garbage)
	}
	recordClauses(t, root)
	if _, o, _ := next(t, root, nil); o["gate"] != "alignment_confirm" {
		t.Fatalf("a recorded clause list must reach alignment_confirm: %v", o)
	}
	answerGate(t, root, "alignment_confirm", "confirm")
	if b := auditorBriefOf(t, root, "verdicts"); strings.Contains(b, "Your previous reply was rejected") {
		t.Fatalf("a recorded reply must not leave its rejections in the next mode's brief:\n%s", b)
	}
	for range 2 {
		rejectAuditor(t, root, "verdicts", garbage)
	}
	// Four rejected replies in the run, two since the record: the cap counts
	// the streak, not the run's history.
	_, o, _ := next(t, root, nil)
	if o["op"] != "dispatch" || o["gate"] == "agent_invalid" {
		t.Fatalf("rejections before a valid record must not count towards the cap: %v", o)
	}
	resets := eventsOfType(t, bdir, "alignment_attempts_reset")
	if len(resets) != 1 {
		t.Fatalf("one record ends one streak, got %d resets: %+v", len(resets), resets)
	}
	if resets[0].Data["reason"] != "recorded" || resets[0].Data["mode"] != "clauses" {
		t.Fatalf("the reset must say a record ended the streak, and in which mode: %+v", resets[0])
	}
}

// TestAValidRecordAfterARetryClearsTheQuotedRejection is the streak's other
// end. `agent_invalid`'s *retry* forwards the problems onto its own reset so
// that the retried brief can quote them — the count is back at zero while
// the reasons are not — so the record that answers them has to retire them
// too, or every later brief opens with a rejection that was already put
// right.
func TestAValidRecordAfterARetryClearsTheQuotedRejection(t *testing.T) {
	t.Parallel()
	root, bdir := auditedFixture(t)
	garbage := unusableReply(t)
	for range 3 {
		rejectAuditor(t, root, "clauses", garbage)
	}
	if _, o, _ := next(t, root, nil); o["gate"] != "agent_invalid" {
		t.Fatalf("three rejections must ask: %v", o)
	}
	answerGate(t, root, "agent_invalid", "retry")
	recordClauses(t, root)
	if _, o, _ := next(t, root, nil); o["gate"] != "alignment_confirm" {
		t.Fatalf("a recorded clause list must reach alignment_confirm: %v", o)
	}
	answerGate(t, root, "alignment_confirm", "confirm")
	if b := auditorBriefOf(t, root, "verdicts"); strings.Contains(b, "Your previous reply was rejected") {
		t.Fatalf("a record must retire the reasons the retry forwarded:\n%s", b)
	}
	resets := eventsOfType(t, bdir, "alignment_attempts_reset")
	if len(resets) != 2 {
		t.Fatalf("the retry resets, then the record does: %+v", resets)
	}
	if _, ok := resets[0].Data["problems"].([]any); !ok {
		t.Fatalf("the retry's reset carries the problems forward: %+v", resets[0])
	}
	if resets[1].Data["reason"] != "recorded" {
		t.Fatalf("the record's reset retires them: %+v", resets[1])
	}
}

// unusableReply is an auditor message with no JSON block in it at all —
// the shape `record --agent alignment-auditor` rejects.
func unusableReply(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reply.md")
	if err := os.WriteFile(p, []byte("I could not find anything.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// auditedFixture is planLoadFixture with the alignment audit switched back
// on: a run whose only outstanding work is the auditor's two dispatches.
func auditedFixture(t *testing.T) (string, string) {
	t.Helper()
	root, bdir := planLoadFixture(t)
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Config.Alignment = true
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	return root, bdir
}

// rejectAuditor answers the pending auditor dispatch with a reply takt
// cannot parse, and checks it was reported rather than failed on.
func rejectAuditor(t *testing.T, root, mode, from string) {
	t.Helper()
	_, o, _ := next(t, root, nil)
	if o["op"] != "dispatch" {
		t.Fatalf("expected the auditor dispatch, got %v", o)
	}
	c, r, e := runIn(t, root, nil, "record", "--agent", "alignment-auditor",
		"--mode", mode, "--from", from, "--slug", "demo")
	if c != 0 || r[keyValidJSON] != false {
		t.Fatalf("%d %v %s", c, r, e)
	}
}

// auditorBriefOf drives one `next`, checks it dispatched the auditor in mode
// and returns the brief it was given.
func auditorBriefOf(t *testing.T, root, mode string) string {
	t.Helper()
	_, o, _ := next(t, root, nil)
	agents, _ := o["agents"].([]any)
	if len(agents) == 0 {
		t.Fatalf("expected an auditor dispatch, got %v", o)
	}
	ag, _ := agents[0].(map[string]any)
	if ag["mode"] != mode {
		t.Fatalf("expected the %s dispatch, got %v", mode, o)
	}
	b, err := os.ReadFile(ag["brief"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// eventsOfType returns the run's events of type typ, in order.
func eventsOfType(t *testing.T, bdir, typ string) []bundle.Event {
	t.Helper()
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	var out []bundle.Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// recordClauses answers the pending clauses dispatch with a usable reply.
func recordClauses(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "clauses.md")
	if err := os.WriteFile(p, []byte(validClauses), 0o600); err != nil {
		t.Fatal(err)
	}
	c, r, e := runIn(t, root, nil, "record", "--agent", "alignment-auditor",
		"--mode", "clauses", "--from", p, "--slug", "demo")
	if c != 0 || r["ok"] != true {
		t.Fatalf("%d %v %s", c, r, e)
	}
}

// answerGate answers the run's pending gate with choice.
func answerGate(t *testing.T, root, gate, choice string) {
	t.Helper()
	if c, _, e := runIn(t, root, nil,
		"answer", "--gate", gate, "--choice", choice, "--slug", "demo"); c != 0 {
		t.Fatalf("answer %s %s: %s", gate, choice, e)
	}
}
