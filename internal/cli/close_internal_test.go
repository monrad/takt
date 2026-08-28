package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

// bumpTask3Attempt sets task 3's dispatched attempt to 1 in state, the way
// launchWave stamps it on a real dispatch. reviewerRun's fixture sets
// active_wave.Attempt but bypasses launchWave, leaving the task's own
// Attempt at its zero value — which makes sliceDone (cmd_close_wave.go)
// treat the task as never dispatched and the wave as vacuously done
// regardless of its graded status. The rework tests need the real
// accounting to see committed:false.
func bumpTask3Attempt(t *testing.T, bdir string) {
	t.Helper()
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	tk := st.Task(3)
	if tk == nil {
		t.Fatal("task 3 not found in state")
	}
	tk.Attempt = 1
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
}

// clearLensConfig disables the internal review layer in state. A test that
// writes its own internal record directly and then drives `next` afterward
// needs this: decideInternal (internal/decide) otherwise sees the frozen
// lens set's per-lens records missing for this attempt — this fixture wrote
// only the merged, verified record — and tries to redispatch the lens
// fan-out.
func clearLensConfig(t *testing.T, bdir string) {
	t.Helper()
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Config.Review.Lenses = nil
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
}

// writeInternalRecordForTask3 writes an internal record for wave 0 slice 1
// attempt 1 — the dispatch reviewerRun builds — with one candidate (id c1,
// lens correctness, file a.go:4) confirmed for task 3, at the given
// severity/title/detail.
func writeInternalRecordForTask3(t *testing.T, bdir, severity, title, detail string) {
	t.Helper()
	const id, lens, file, line = "c1", "correctness", "a.go", 4
	rec := wave.InternalRecord{
		Wave: 0, Slice: 1, Attempt: 1, Model: "sonnet", RecordedAt: time.Now().UTC(),
		Lenses: []string{lens},
		Candidates: []wave.Candidate{{
			Finding: backend.Finding{Severity: severity, File: file, Line: line, Title: title, Detail: detail},
			ID:      id, Task: 3, Lenses: []string{lens},
		}},
		Verdicts: []wave.CandidateVerdict{{
			ID: id, Verdict: wave.VerdictConfirmed, Evidence: "VERIFIER-EVIDENCE-MARKER: traced at " + file,
		}},
		Confirmed: []string{id},
	}
	if err := wave.WriteInternalRecord(bdir, rec); err != nil {
		t.Fatal(err)
	}
}

// followUps reads follow-ups.json, failing the test on any read error.
func followUps(t *testing.T, bdir string) []gate.FollowUp {
	t.Helper()
	f, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	return f.Items
}

// closeTask3 reads the close record for wave 0 slice 1 and returns task 3's
// result, failing the test if it is missing.
func closeTask3(t *testing.T, bdir string) wave.TaskResult {
	t.Helper()
	c, err := wave.ReadClose(bdir, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("no close record for wave 0 slice 1")
	}
	for _, tr := range c.Tasks {
		if tr.Task == 3 {
			return tr
		}
	}
	t.Fatalf("task 3 missing from close record: %+v", c.Tasks)
	return wave.TaskResult{}
}

// taskFindingsMD reads reviews/wave-0/task-3.md, the human findings file
// reviewOne writes for task 3.
func taskFindingsMD(t *testing.T, bdir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bdir, "reviews", "wave-0", "task-3.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertApproveFollowUps checks that follow-ups.json holds exactly the
// confirmed internal finding (source internal) and the backend's own finding
// (source approve), both attributed to wave 0 task 3.
func assertApproveFollowUps(t *testing.T, bdir string, wantInternal, wantApprove gate.FollowUp) {
	t.Helper()
	items := followUps(t, bdir)
	var sawInternal, sawApprove bool
	for _, f := range items {
		if f.Wave == nil || *f.Wave != 0 || f.Task != 3 {
			t.Fatalf("follow-up missing wave/task: %+v", f)
		}
		switch f.Source {
		case gate.SourceInternal:
			if f.Severity != wantInternal.Severity || f.Title != wantInternal.Title {
				t.Fatalf("internal follow-up = %+v", f)
			}
			sawInternal = true
		case gate.SourceApprove:
			if f.Severity != wantApprove.Severity || f.Title != wantApprove.Title {
				t.Fatalf("approve follow-up = %+v", f)
			}
			sawApprove = true
		}
	}
	if !sawInternal || !sawApprove {
		t.Fatalf("follow-ups = %+v", items)
	}
}

// assertReviewScopedEvent checks that a review_scoped event for wave 0 task 3
// carries the given blind and final verdicts.
func assertReviewScopedEvent(t *testing.T, bdir, blindVerdict, verdict string) {
	t.Helper()
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type != "review_scoped" {
			continue
		}
		if e.Data["wave"] != 0.0 || e.Data["task"] != 3.0 ||
			e.Data["blind_verdict"] != blindVerdict || e.Data["verdict"] != verdict {
			t.Fatalf("review_scoped event = %+v", e.Data)
		}
		return
	}
	t.Fatal("no review_scoped event")
}

// reviewLogPrompts returns the contents of every *.prompt file under
// bdir/logs whose name matches the glob.
func reviewLogPrompts(t *testing.T, bdir, glob string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(bdir, "logs", glob))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		b, rerr := os.ReadFile(m)
		if rerr != nil {
			t.Fatal(rerr)
		}
		out = append(out, string(b))
	}
	return out
}

// TestCloseAttachesInternalAndCarriesOnApprove covers the plain merge path
// (two-layers design §3.5, §3.7): a confirmed internal finding that is not
// blocking rides along with the blind pass's own verdict rather than buying a
// second review call, and on approve both the internal finding and the
// backend's own findings are carried to follow-ups.
func TestCloseAttachesInternalAndCarriesOnApprove(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	bumpTask3Attempt(t, bdir)
	writeInternalRecordForTask3(t, bdir, "major", "lens title", "lens detail")
	env := map[string]string{
		"TAKT_FAKE_REVIEW": `{"verdict":"approve","summary":"ok",` +
			`"findings":[{"severity":"minor","file":"a.go","line":1,"title":"nit title","detail":"nit detail"}]}`,
	}
	code, o, errb := runIn(t, root, env, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	tr := closeTask3(t, bdir)
	if len(tr.Internal) != 1 || tr.Internal[0].Title != "lens title" {
		t.Fatalf("internal findings not attached: %+v", tr.Internal)
	}
	if tr.BlindReview != nil {
		t.Fatalf("a non-blocking internal finding must not buy a scoped pass: %+v", tr.BlindReview)
	}
	if tr.Review == nil || tr.Review.Verdict != backend.VerdictApprove {
		t.Fatalf("review = %+v", tr.Review)
	}
	assertApproveFollowUps(t, bdir,
		gate.FollowUp{Severity: "major", Title: "lens title"}, gate.FollowUp{Severity: "minor", Title: "nit title"})
	md := taskFindingsMD(t, bdir)
	if !strings.Contains(md, "## Internal findings (confirmed)") ||
		!strings.Contains(md, "- [lens:correctness] major a.go:4 — lens title: lens detail") {
		t.Fatalf("findings.md lacks the internal findings section: %s", md)
	}
	if strings.Contains(md, "## Scoped pass") {
		t.Fatalf("no scoped pass ran, findings.md must not claim one: %s", md)
	}
}

// TestCloseRunsTheScopedPassOnBlockingDisagreement covers the scoped pass
// (two-layers design §3.5, D6): a confirmed blocking finding the blind pass
// missed buys exactly one more backend call, scoped to the confirmed claims
// alone — no lens names, no verifier evidence — and that pass's verdict, not
// the blind one, grades the task.
func TestCloseRunsTheScopedPassOnBlockingDisagreement(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	bumpTask3Attempt(t, bdir)
	writeInternalRecordForTask3(t, bdir, "blocking", "unchecked error", "swallowed err")
	followupFile := filepath.Join(t.TempDir(), "followup.json")
	followupBody := `{"verdict":"rework","summary":"still bad",` +
		`"findings":[{"severity":"blocking","file":"b.go","line":9,"title":"not fixed","detail":"do it"}]}`
	if err := os.WriteFile(followupFile, []byte(followupBody), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"TAKT_FAKE_REVIEW":                    `{"verdict":"approve","summary":"initial ok"}`,
		"TAKT_FAKE_REVIEW_FILE_TASK_FOLLOWUP": followupFile,
	}
	code, o, errb := runIn(t, root, env, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != false {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	tr := closeTask3(t, bdir)
	if tr.BlindReview == nil || tr.BlindReview.Verdict != backend.VerdictApprove {
		t.Fatalf("blind review must be preserved: %+v", tr.BlindReview)
	}
	if tr.Review == nil || tr.Review.Verdict != backend.VerdictRework {
		t.Fatalf("the scoped verdict must grade the task: %+v", tr.Review)
	}
	if tr.Status != "rework" {
		t.Fatalf("status = %q", tr.Status)
	}
	assertReviewScopedEvent(t, bdir, "approve", "rework")
	if items := followUps(t, bdir); len(items) != 0 {
		t.Fatalf("rework must carry nothing: %+v", items)
	}
	md := taskFindingsMD(t, bdir)
	if !strings.Contains(md, "# Review: demo task 3 — approve") {
		t.Fatalf("findings.md's # Review section must show the blind verdict: %s", md)
	}
	if !strings.Contains(md, "## Scoped pass") || !strings.Contains(md, "Verdict: rework — still bad") ||
		!strings.Contains(md, "blocking a.go:4 — unchecked error: swallowed err") {
		t.Fatalf("findings.md lacks the scoped pass section: %s", md)
	}
	if !strings.Contains(md, "## Internal findings (confirmed)") ||
		!strings.Contains(md, "- [lens:correctness] blocking a.go:4 — unchecked error: swallowed err") {
		t.Fatalf("findings.md lacks the internal findings section: %s", md)
	}
	prompts := reviewLogPrompts(t, bdir, "review-task-3-scoped-*.prompt")
	if len(prompts) != 1 {
		t.Fatalf("want exactly one scoped prompt log, got %d", len(prompts))
	}
	p := prompts[0]
	if !strings.Contains(p, "blocking a.go:4 — unchecked error: swallowed err") {
		t.Fatalf("scoped prompt lacks the distilled claim: %s", p)
	}
	if strings.Contains(p, "VERIFIER-EVIDENCE-MARKER") {
		t.Fatalf("scoped prompt must not leak the verifier's evidence: %s", p)
	}
	if strings.Contains(p, "correctness") {
		t.Fatalf("scoped prompt must not name the lens: %s", p)
	}
}

// TestCloseScopedPassErrorLeavesNoStaleCarry covers the two fix-round
// findings: a scoped pass that itself errors must not let the task's still
// approve-shaped tr.Review (the blind pass, deliberately left in place —
// see carryApprovedFindings) get carried into follow-ups.json — the task
// was never actually approved, only the blind pass was, and a later
// close-wave retry that does succeed would otherwise carry the same
// findings a second time. It must also still leave a findings.md: the blind
// pass genuinely completed, so the human resolving the review_error gets
// the full story even though the second call failed.
func TestCloseScopedPassErrorLeavesNoStaleCarry(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	bumpTask3Attempt(t, bdir)
	writeInternalRecordForTask3(t, bdir, "blocking", "unchecked error", "swallowed err")
	followupFile := filepath.Join(t.TempDir(), "followup.json")
	followupBody := `{"verdict":"error","summary":"review failed","reason":"boom"}`
	if err := os.WriteFile(followupFile, []byte(followupBody), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"TAKT_FAKE_REVIEW":                    `{"verdict":"approve","summary":"initial ok"}`,
		"TAKT_FAKE_REVIEW_FILE_TASK_FOLLOWUP": followupFile,
	}
	code, o, errb := runIn(t, root, env, "close-wave", "--slug", "demo")
	if code != 0 {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	tr := closeTask3(t, bdir)
	if tr.Status != "review_error" {
		t.Fatalf("status = %q, want review_error: %+v", tr.Status, tr)
	}
	if tr.Review == nil || tr.Review.Verdict != backend.VerdictApprove {
		t.Fatalf("the blind pass must still be recorded (informative in the close record): %+v", tr.Review)
	}
	if items := followUps(t, bdir); len(items) != 0 {
		t.Fatalf("an errored scoped pass must carry nothing — got: %+v", items)
	}
	md := taskFindingsMD(t, bdir)
	if !strings.Contains(md, "# Review: demo task 3 — approve") {
		t.Fatalf("findings.md must still show the blind verdict: %s", md)
	}
	if !strings.Contains(md, "## Internal findings (confirmed)") ||
		!strings.Contains(md, "- [lens:correctness] blocking a.go:4 — unchecked error: swallowed err") {
		t.Fatalf("findings.md lacks the internal findings section: %s", md)
	}
	if strings.Contains(md, "## Scoped pass") {
		t.Fatalf("no scoped verdict was ever reached, findings.md must not claim one: %s", md)
	}
}

// TestCloseWithoutInternalRecordIsTodaysBehaviour covers the "no internal
// record on disk" fallback (two-layers design §3.7): with nothing verified,
// close-wave behaves exactly as it did before this layer existed.
func TestCloseWithoutInternalRecordIsTodaysBehaviour(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	bumpTask3Attempt(t, bdir)
	code, o, errb := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	tr := closeTask3(t, bdir)
	if len(tr.Internal) != 0 {
		t.Fatalf("no internal record on disk must attach nothing: %+v", tr.Internal)
	}
	if tr.BlindReview != nil {
		t.Fatalf("no scoped pass must run: %+v", tr.BlindReview)
	}
	if items := followUps(t, bdir); len(items) != 0 {
		t.Fatalf("no follow-ups today: %+v", items)
	}
	prompts := reviewLogPrompts(t, bdir, "review-task-3-*.prompt")
	if len(prompts) != 1 {
		t.Fatalf("want exactly one review log for the task, got %d: %v", len(prompts), prompts)
	}
}

// TestRetryBriefCarriesLensLines covers the retry brief (spec §7.4, D11): a
// confirmed lens finding a task's rework did not act on is not lost — it
// rides along in the next attempt's brief beside the backend's own findings.
func TestRetryBriefCarriesLensLines(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	bumpTask3Attempt(t, bdir)
	// The internal review layer here is seeded straight to its verified
	// record, bypassing the lens fan-out reviewerRun's frozen lens set would
	// otherwise still expect per-lens records for — turning it off keeps
	// `next` from redispatching lenses this fixture never ran.
	clearLensConfig(t, bdir)
	writeInternalRecordForTask3(t, bdir, "major", "internal title", "internal detail")
	env := map[string]string{
		"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"needs fix",` +
			`"findings":[{"severity":"major","file":"a.go","line":2,"title":"backend title","detail":"backend detail"}]}`,
	}
	code, o, errb := runIn(t, root, env, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != false {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	tr := closeTask3(t, bdir)
	if len(tr.Internal) != 1 || tr.Internal[0].Title != "internal title" {
		t.Fatalf("internal findings must attach even off the approve path: %+v", tr.Internal)
	}
	_, redispatch, errb2 := drainReview(t, root, env)
	if redispatch["op"] != "dispatch" {
		t.Fatalf("retry dispatch: %v %s", redispatch, errb2)
	}
	ags := agentsOf(t, redispatch)
	if len(ags) != 1 || ags[0]["task"] != float64(3) {
		t.Fatalf("retry dispatch agents = %v", ags)
	}
	b, err := os.ReadFile(ags[0]["brief"].(string))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(b)
	if !strings.Contains(brief, "major a.go:2 — backend title: backend detail") {
		t.Fatalf("retry brief lacks the backend finding: %s", brief)
	}
	if !strings.Contains(brief, "[lens:correctness] major a.go:4 — internal title: internal detail") {
		t.Fatalf("retry brief lacks the lens finding: %s", brief)
	}
}

// twoTaskReviewerRun is reviewerRun (record_reviewer_test.go) extended to two
// tasks (3 and 4, each its own file) instead of one, both already dispatched
// and recorded done at attempt 1. The carry race the fix in
// carryApprovedFindings addresses only manifests with more than one task
// approving in the same close — reviewerRun's single task can never exercise
// it.
func twoTaskReviewerRun(t *testing.T) (string, string) {
	t.Helper()
	root, bdir := setupRunWith(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	idx := `{"schema":1,"spec_hash":"x","tasks":[
 {"id":3,"title":"c","description":"create a.go","files":["a.go"],"verify":["true"],"depends_on":[],"goals":[],"class":"docs"},
 {"id":4,"title":"d","description":"create d.go","files":["d.go"],"verify":["true"],"depends_on":[],"goals":[],"class":"docs"}]}`
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", idx)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan\n")
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Phase = bundle.PhaseExecute
	st.Tasks = []bundle.Task{
		{ID: 3, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Class: "docs", Attempt: 1},
		{ID: 4, Wave: 0, Status: bundle.StatusPending, Files: []string{"d.go"}, Class: "docs", Attempt: 1},
	}
	st.ActiveWave = &bundle.ActiveWave{N: 0, Slice: 1, Attempt: 1, Tasks: []int{3, 4}, StartedAt: time.Now().UTC()}
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "two-task reviewer fixture")
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "d.go", "package d\n")
	record(t, root, 3, 1, "done", "wrote a.go")
	record(t, root, 4, 1, "done", "wrote d.go")
	return root, bdir
}

// TestCloseCarriesFollowUpsForEveryApprovedTaskConcurrently is the assertion
// that would have caught the lost update: reviewTasks reviews multiple tasks
// concurrently (bounded by max_parallel, default 8 here), and
// gate.AppendFollowUps is an unsynchronized read-modify-write of
// follow-ups.json. Carrying the confirmed internal finding for both tasks
// must not let one task's carry silently drop the other's.
func TestCloseCarriesFollowUpsForEveryApprovedTaskConcurrently(t *testing.T) {
	t.Parallel()
	root, bdir := twoTaskReviewerRun(t)
	rec := wave.InternalRecord{
		Wave: 0, Slice: 1, Attempt: 1, Model: "sonnet", RecordedAt: time.Now().UTC(),
		Lenses: []string{"correctness"},
		Candidates: []wave.Candidate{
			{
				Finding: backend.Finding{
					Severity: "major",
					File:     "a.go",
					Line:     1,
					Title:    "t3 title",
					Detail:   "t3 detail",
				},
				ID:     "c1",
				Task:   3,
				Lenses: []string{"correctness"},
			},
			{
				Finding: backend.Finding{
					Severity: "major",
					File:     "d.go",
					Line:     1,
					Title:    "t4 title",
					Detail:   "t4 detail",
				},
				ID:     "c2",
				Task:   4,
				Lenses: []string{"correctness"},
			},
		},
		Verdicts: []wave.CandidateVerdict{
			{ID: "c1", Verdict: wave.VerdictConfirmed, Evidence: "e1"},
			{ID: "c2", Verdict: wave.VerdictConfirmed, Evidence: "e2"},
		},
		Confirmed: []string{"c1", "c2"},
	}
	if err := wave.WriteInternalRecord(bdir, rec); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"approve","summary":"ok"}`}
	code, o, errb := runIn(t, root, env, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	items := followUps(t, bdir)
	byTask := map[int]int{}
	for _, f := range items {
		if f.Source != gate.SourceInternal {
			t.Fatalf("unexpected follow-up source: %+v", f)
		}
		byTask[f.Task]++
	}
	if byTask[3] != 1 || byTask[4] != 1 {
		t.Fatalf("follow-ups must hold both tasks' internal findings — got %v from %+v", byTask, items)
	}
	if len(items) != 2 {
		t.Fatalf("want exactly 2 follow-ups, got %d: %+v", len(items), items)
	}
}
