package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

func TestStatusSingleBundle(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 0 {
		t.Fatal(errb)
	}
	testutil.WriteFile(
		t,
		root,
		"docs/takt/demo/goals.md",
		"# Goals — demo\n\n## Anchor\n```text\ntopic\n```\n\n## Goals\n- G1 — it works · signal: test · evidence: go test\n",
	)
	code, got, errb := runIn(t, root, nil, "status", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["slug"] != "demo" || got["phase"] != "brainstorm" || got["branch"] != "takt/demo" {
		t.Fatalf("out = %v", got)
	}
	tasks := got["tasks"].(map[string]any)
	if tasks["total"] != float64(0) {
		t.Fatalf("tasks = %v", tasks)
	}
	goals := got["goals"].([]any)
	if len(goals) != 1 || goals[0].(map[string]any)["id"] != "G1" {
		t.Fatalf("goals = %v", goals)
	}
	if _, ok := got["gates_live"]; !ok {
		t.Fatal("live gate status missing")
	}

	var out strings.Builder
	cli.Main([]string{"status"}, &out, &out, func(k string) string {
		if k == "HOME" {
			return root + "/.home"
		}
		return ""
	}, root)
	text := out.String()
	for _, want := range []string{"demo", "brainstorm", "takt/demo", "G1"} {
		if !strings.Contains(text, want) {
			t.Errorf("text status lacks %q:\n%s", want, text)
		}
	}
}

func TestStatusNeedsSlugWhenSeveral(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	// No checkout between the two inits: staying on takt/one means the
	// second init adopts it (spec D9) instead of cutting takt/two, so both
	// bundles' state.json land in the same working tree and are visible to
	// ListSlugs together — the precondition this test needs. Checking out
	// main in between (as an earlier draft of this test did) instead makes
	// the second init create its own branch; ordinary git checkout then
	// removes docs/takt/one from the working tree (it was only committed on
	// takt/one), so the "several active" precondition is never reached.
	runIn(t, root, nil, "init", "--slug", "one", "t")
	runIn(t, root, nil, "init", "--slug", "two", "t")
	code, _, errb := runIn(t, root, nil, "status", "--json")
	if code != 1 || !strings.Contains(errb, "one") || !strings.Contains(errb, "two") {
		t.Fatalf("several bundles must ask for --slug: %d %s", code, errb)
	}
	slugCode, got, _ := runIn(t, root, nil, "status", "--json", "--slug", "one")
	if slugCode != 0 || got["slug"] != "one" {
		t.Fatalf("--slug must select: %d %v", slugCode, got)
	}
}

func TestStatusNoRun(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "status", "--json"); code != 1 || !strings.Contains(errb, "no active run") {
		t.Fatalf("%d %s", code, errb)
	}
}

// TestStatusAlignmentContradictedIsContraction covers fix round 1: spec
// §7.3 ("narrowed/dropped/contradicted are reported as contraction, widened
// as creep") named "contradicted" as a contraction verdict alongside
// narrowed/dropped; statusAlignment must bucket it there too, not drop it
// from both lists.
func TestStatusAlignmentContradictedIsContraction(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	verdicts := filepath.Join(t.TempDir(), "verdicts.txt")
	body := "```json\n" +
		`{"mode":"verdicts","verdicts":[` +
		`{"id":"A1","verdict":"covered","evidence":"task 1"},` +
		`{"id":"A2","verdict":"contradicted","evidence":"plan does the opposite of A2"}]}` +
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
	code, got, errb := runIn(t, root, nil, "status", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	alignment := got["alignment"].(map[string]any)
	counts := alignment["counts"].(map[string]any)
	if counts["contradicted"] != float64(1) {
		t.Fatalf("counts = %v", counts)
	}
	contraction := alignment["contraction"].([]any)
	if len(contraction) != 1 || contraction[0] != "A2" {
		t.Fatalf("contraction = %v, want just [A2]", contraction)
	}
	if creep, ok := alignment["creep"].([]any); ok && len(creep) != 0 {
		t.Fatalf("A2 must not also land in creep: %v", creep)
	}
}

// TestStatusRejectsInvalidSlug covers review finding 1: every command that
// takes --slug must reject a bad value before touching the filesystem, not
// just init.
func TestStatusRejectsInvalidSlug(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 0 {
		t.Fatal(errb)
	}
	code, _, errb := runIn(t, root, nil, "status", "--json", "--slug", "My Feature")
	if code != 2 || !strings.Contains(errb, "slug") {
		t.Fatalf("exit %d, want 2 with a slug error: %s", code, errb)
	}
}

// TestStatusShowsFinishBlock covers task 7: once a run reaches the finish
// phase, status carries a "finish" block built from the finish/ records and
// the disposition — and that block is absent before the run gets there.
func TestStatusShowsFinishBlock(t *testing.T) {
	t.Parallel()
	d, _, _ := finishedRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "keep", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o := d.nextOp(); o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	code, got, errb := d.cmd("status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	fin, ok := got["finish"].(map[string]any)
	if !ok {
		t.Fatalf("no finish block: %v", got)
	}
	if sha, _ := fin["verified_sha"].(string); sha == "" {
		t.Fatalf("verified_sha empty: %v", fin)
	}
	if fin["disposition"] != "keep" {
		t.Fatalf("disposition = %v", fin["disposition"])
	}
	if fin["applied"] != true {
		t.Fatalf("applied = %v", fin["applied"])
	}

	var out strings.Builder
	cli.Main([]string{"status", "--slug", "demo"}, &out, &out, func(k string) string {
		if k == "HOME" {
			return d.root + "/.home"
		}
		return ""
	}, d.root)
	text := out.String()
	for _, want := range []string{"verify: passed at ", "disposition: keep (applied)"} {
		if !strings.Contains(text, want) {
			t.Errorf("text status lacks %q:\n%s", want, text)
		}
	}

	// A run still in execute phase carries no finish block.
	root2, _ := setupRun(t)
	code2, got2, errb2 := runIn(t, root2, nil, "status", "--json")
	if code2 != 0 {
		t.Fatalf("exit %d: %s", code2, errb2)
	}
	if _, hasFinish := got2["finish"]; hasFinish {
		t.Fatalf("execute phase must have no finish key: %v", got2)
	}
}

// TestStatusShowsTheSessionHolder covers the status surface of the sidecar
// (spec §11): who holds the run and how long ago they last called, read from
// logs/session.json rather than from state.json, and `none` once the lock is
// released.
func TestStatusShowsTheSessionHolder(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	next(t, root, map[string]string{"TAKT_SESSION": "S"})
	c, r, e := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if c != 0 {
		t.Fatal(e)
	}
	sess, _ := r["session"].(map[string]any)
	if sess["id"] != "S" || sess["host"] == "" || sess["heartbeat"] == nil || sess["age"] == nil {
		t.Fatalf("session block: %v", r["session"])
	}
	if out := statusText(t, root); !strings.Contains(out, "session: S@") {
		t.Fatalf("text status must name the holder:\n%s", out)
	}
	runIn(t, root, nil, "unlock", "--slug", "demo")
	if _, r, _ = runIn(t, root, nil, "status", "--json", "--slug", "demo"); r["session"] != nil {
		t.Fatalf("a free run has session null: %v", r["session"])
	}
	if out := statusText(t, root); !strings.Contains(out, "session: none") {
		t.Fatalf("text status after unlock:\n%s", out)
	}
}

// TestStatusInternalReviewLine covers the internal review's status line
// through three of its four states — partial lenses, verify pending and
// verified counts (two-layers design §5.7) — on the same reviewer fixture
// record_reviewer_test.go's tests seed lens and internal records against.
func TestStatusInternalReviewLine(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)

	// No lens recorded yet for the active dispatch: 0/2.
	if out := statusText(t, root); !strings.Contains(out, "internal review: 0/2 lenses recorded") {
		t.Fatalf("no lenses yet:\n%s", out)
	}

	// One of the two frozen lenses recorded.
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	code, got, errb := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	ir, ok := got["internal_review"].(map[string]any)
	if !ok {
		t.Fatalf("no internal_review block: %v", got)
	}
	if ir["lenses_recorded"] != float64(1) || ir["lenses_total"] != float64(2) {
		t.Fatalf("internal_review = %v", ir)
	}
	if out := statusText(t, root); !strings.Contains(out, "internal review: 1/2 lenses recorded") {
		t.Fatalf("one lens recorded:\n%s", out)
	}

	// Both lenses recorded, no internal record yet: verify pending on the
	// merged candidates.
	writeLensRecord(t, bdir, "intent", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "minor", File: "b.go", Line: 1, Title: "t2"}, Task: 3},
	})
	if out := statusText(t, root); !strings.Contains(out, "internal review: verify pending (2 candidates)") {
		t.Fatalf("verify pending:\n%s", out)
	}

	// The internal record is written: verified candidates and confirmed
	// counts.
	internal := wave.InternalRecord{
		Wave: 0, Slice: 1, Attempt: 1, Model: "sonnet", RecordedAt: time.Now().UTC(),
		Lenses: []string{"correctness", "intent"},
		Candidates: []wave.Candidate{
			{ID: "c1", Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"},
				Task: 3, Lenses: []string{"correctness"}},
			{ID: "c2", Finding: backend.Finding{Severity: "minor", File: "b.go", Line: 1, Title: "t2"},
				Task: 3, Lenses: []string{"intent"}},
		},
		Verdicts: []wave.CandidateVerdict{
			{ID: "c1", Verdict: wave.VerdictConfirmed},
			{ID: "c2", Verdict: wave.VerdictFalsePositive},
		},
		Confirmed: []string{"c1"},
	}
	if err := wave.WriteInternalRecord(bdir, internal); err != nil {
		t.Fatal(err)
	}
	code, got, errb = runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	ir, _ = got["internal_review"].(map[string]any)
	if ir["candidates"] != float64(2) || ir["confirmed"] != float64(1) || ir["verify_pending"] != false {
		t.Fatalf("verified internal_review = %v", ir)
	}
	if out := statusText(t, root); !strings.Contains(out, "candidates, ") ||
		!strings.Contains(out, "internal review: 2 candidates, 1 confirmed") {
		t.Fatalf("verified counts:\n%s", out)
	}
}

// TestStatusInternalReviewSkipped covers the fourth state: skipped, which
// takes priority over an in-progress lens count (two-layers design §5.7).
func TestStatusInternalReviewSkipped(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	if err := bundle.AppendEvent(bdir, "internal_review_skipped", map[string]any{
		"wave": 0, "slice": 1, "attempt": 1, "reason": "agent_invalid",
	}); err != nil {
		t.Fatal(err)
	}
	code, got, errb := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	ir, ok := got["internal_review"].(map[string]any)
	if !ok || ir["skipped"] != true {
		t.Fatalf("internal_review = %v", ir)
	}
	if out := statusText(t, root); !strings.Contains(out, "internal review: skipped") {
		t.Fatalf("skipped:\n%s", out)
	}
}

// TestStatusInternalReviewZeroCandidatesNotVerifyPending covers the state
// where every lens is recorded but merged to zero candidates: no verifier
// will ever be dispatched for this attempt (recordVerify refuses "no
// candidates to verify"), so status must not claim one is pending.
func TestStatusInternalReviewZeroCandidatesNotVerifyPending(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", nil)
	writeLensRecord(t, bdir, "intent", nil)
	code, got, errb := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	ir, ok := got["internal_review"].(map[string]any)
	if !ok {
		t.Fatalf("no internal_review block: %v", got)
	}
	if ir["candidates"] != float64(0) || ir["verify_pending"] != false {
		t.Fatalf("internal_review = %v", ir)
	}
	if out := statusText(t, root); !strings.Contains(out, "internal review: 0 candidates, 0 confirmed") {
		t.Fatalf("zero candidates must not say verify pending:\n%s", out)
	}
}

// fourTaskIndex is a plan.index.json with four tasks, for a run that has
// planned but not yet materialised them. Its spec_hash never matters here:
// status reads the index for its task count only, and validates nothing.
const fourTaskIndex = `{"schema":1,"spec_hash":"sha256:deadbeef","tasks":[
 {"id":1,"title":"a","description":"add a","files":["a.go"],"verify":["true"],"depends_on":[],"goals":["G1"],"class":"bounded"},
 {"id":2,"title":"b","description":"add b","files":["b.go"],"verify":["true"],"depends_on":[1],"goals":["G1"],"class":"implement"},
 {"id":3,"title":"c","description":"add c","files":["c.go"],"verify":["true"],"depends_on":[1],"goals":["G1"],"class":"implement"},
 {"id":4,"title":"d","description":"add d","files":["d.go"],"verify":["true"],"depends_on":[2,3],"goals":["G1"],"class":"implement"}]}`

// fiveClauseAlignment is alignment.json with five clauses; verdicts is the
// body of the "verdicts" array, empty for the shape the audit has between
// decomposition and the auditor's ruling.
func fiveClauseAlignment(confirmed, skipped bool, verdicts string) string {
	clauses := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		clauses = append(clauses, fmt.Sprintf(`{"id":"A%d","text":"t%d","span":"s%d"}`, i, i, i))
	}
	return fmt.Sprintf(`{"anchor_hash":"x","clauses":[%s],"confirmed":%t,"skipped":%t,"verdicts":[%s]}`,
		strings.Join(clauses, ","), confirmed, skipped, verdicts)
}

// TestStatusPlanPhaseSaysPlannedAndConfirmedClauses pins the two lines a run
// in the plan phase used to get wrong (#33): tasks reach state.json only at
// the plan → execute transition, so a planned run reported "0 total" with a
// whole plan on disk; and an alignment.json with clauses but no verdicts
// rendered as a bare "alignment:" label.
func TestStatusPlanPhaseSaysPlannedAndConfirmedClauses(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", fourTaskIndex)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# Plan — demo\n")
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Phase = bundle.PhasePlan
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/alignment.json", fiveClauseAlignment(true, false, ""))

	code, got, errb := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	tasks, ok := got["tasks"].(map[string]any)
	if !ok || tasks["planned"] != float64(4) || tasks["total"] != float64(0) {
		t.Fatalf("tasks = %v", got["tasks"])
	}
	al, ok := got["alignment"].(map[string]any)
	if !ok || al["clauses"] != float64(5) || al["skipped"] != false || al["verdicts_present"] != false {
		t.Fatalf("alignment = %v", got["alignment"])
	}

	out := statusText(t, root)
	if !strings.Contains(out, "tasks: 4 planned (not yet materialised)") {
		t.Fatalf("planned tasks line:\n%s", out)
	}
	if strings.Contains(out, "0 total") {
		t.Fatalf("a planned run must not claim zero tasks:\n%s", out)
	}
	if !strings.Contains(out, "alignment: 5 clauses confirmed, verdicts pending") {
		t.Fatalf("confirmed-without-verdicts line:\n%s", out)
	}

	testutil.WriteFile(t, root, "docs/takt/demo/alignment.json", fiveClauseAlignment(false, false, ""))
	if out = statusText(t, root); !strings.Contains(out, "alignment: 5 clauses awaiting confirmation") {
		t.Fatalf("unconfirmed line:\n%s", out)
	}

	testutil.WriteFile(t, root, "docs/takt/demo/alignment.json", fiveClauseAlignment(false, true, ""))
	if out = statusText(t, root); !strings.Contains(out, "alignment: skipped") {
		t.Fatalf("skipped line:\n%s", out)
	}
}

// TestStatusAlignmentAwaitsConfirmationEvenWithVerdicts pins the one state
// precedence has to get right: `takt record --agent alignment-auditor --mode
// verdicts` stores verdicts whatever the confirmation flag says, so a bundle
// can hold verdicts against a clause set nobody has confirmed. Status names
// the confirmation that is still owed — a ruling on an ask the user never
// agreed to is not a settled reading of it. The plan → execute commit
// message is the other caller and keeps rendering the verdicts there; that
// half is TestLoadCommitMessageCarriesAlignmentSummary.
func TestStatusAlignmentAwaitsConfirmationEvenWithVerdicts(t *testing.T) {
	t.Parallel()
	root := setupAlignmentRun(t, fiveClauseAlignment(false, false,
		`{"id":"A1","verdict":"narrowed","evidence":"scope cut"}`))

	code, got, errb := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	al, ok := got["alignment"].(map[string]any)
	if !ok || al["confirmed"] != false || al["verdicts_present"] != true {
		t.Fatalf("alignment = %v", got["alignment"])
	}

	out := statusText(t, root)
	if !strings.Contains(out, "alignment: 5 clauses awaiting confirmation") {
		t.Fatalf("unconfirmed clauses must await confirmation whatever the verdicts say:\n%s", out)
	}
	if strings.Contains(out, "narrowed") {
		t.Fatalf("a verdict against unconfirmed clauses must not read as a ruling:\n%s", out)
	}
}

// TestStatusAlignmentNamesAVerdictItDoesNotRecognise pins the last way the
// "alignment:" label could still print bare (#33): `takt record --agent
// alignment-auditor --mode verdicts` checks the auditor's reply for shape,
// not for vocabulary, so a bundle takt itself wrote can hold a verdict word
// — or an empty one — outside the five status renders. They are counted
// under one "unrecognised" bucket, which is what makes "a verdict is
// recorded" and "the line says something" the same statement.
func TestStatusAlignmentNamesAVerdictItDoesNotRecognise(t *testing.T) {
	t.Parallel()
	root := setupAlignmentRun(t, fiveClauseAlignment(true, false,
		`{"id":"A1","verdict":"covered","evidence":"task 1"},`+
			`{"id":"A2","verdict":"maybe","evidence":"?"},`+
			`{"id":"A3","verdict":"","evidence":""}`))

	if out := statusText(t, root); !strings.Contains(out, "alignment: 1 covered, 2 unrecognised\n") {
		t.Fatalf("a verdict takt has no word for must still be counted:\n%s", out)
	}

	// And that state is one the recorder produces: the same reply that
	// reaches alignment.json here is accepted at exit 0 with ok:true, so a
	// status that rendered only the five known words would print a bare
	// label on a bundle takt had just written.
	report := filepath.Join(t.TempDir(), "verdicts.txt")
	if err := os.WriteFile(report, []byte("```json\n"+
		`{"mode":"verdicts","verdicts":[{"id":"A1","verdict":"maybe","evidence":"?"}]}`+
		"\n```\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, got, errb := runIn(t, root, nil, "record", "--agent", "alignment-auditor",
		"--mode", "verdicts", "--from", report, "--slug", "demo")
	if code != 0 || got["ok"] != true {
		t.Fatalf("the recorder takes the auditor's word as given: %d %v %s", code, got, errb)
	}
	stored, err := os.ReadFile(filepath.Join(root, "docs", "takt", "demo", "alignment.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), `"verdict": "maybe"`) {
		t.Fatalf("an unrecognised verdict must be on disk to be renderable: %s", stored)
	}
}

// setupAlignmentRun is a run in the plan phase whose alignment.json holds
// the given document, the state every alignment status line is read from.
func setupAlignmentRun(t *testing.T, alignment string) string {
	t.Helper()
	root, bdir := setupRun(t)
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Phase = bundle.PhasePlan
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/alignment.json", alignment)
	return root
}

// TestStatusHintsAtTheBranchHoldingTheBundle pins the three hints a bundle
// that cannot be read now carries (#8). The first is the one the dogfood run
// hit: `takt init` commits the bundle on takt/<slug>, so from any other
// branch every file of it is simply gone from the working tree.
func TestStatusHintsAtTheBranchHoldingTheBundle(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 0 {
		t.Fatal(errb)
	}
	testutil.Git(t, root, "checkout", "main")

	code, _, errb := runIn(t, root, nil, "status", "--slug", "demo")
	if code != 1 || !strings.Contains(errb, "takt/demo") ||
		!strings.Contains(errb, "check it out, or pass --dir") {
		t.Fatalf("branch hint: %d %s", code, errb)
	}

	code, _, errb = runIn(t, root, nil, "status", "--slug", "nope")
	if code != 1 || !strings.Contains(errb, "no run named nope under") ||
		!strings.Contains(errb, "check the slug or pass --dir") {
		t.Fatalf("unknown-slug hint: %d %s", code, errb)
	}

	testutil.Git(t, root, "checkout", "takt/demo")
	testutil.WriteFile(t, root, "docs/takt/demo/state.json", "{")
	code, _, errb = runIn(t, root, nil, "status", "--slug", "demo")
	if code != 1 || !strings.Contains(errb, "run takt doctor") {
		t.Fatalf("unreadable-state hint: %d %s", code, errb)
	}
}
