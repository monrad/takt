package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

// statusText runs `takt status` (text form, no --slug needed with a single
// active bundle) and returns stdout.
func statusText(t *testing.T, root string) string {
	t.Helper()
	var out strings.Builder
	cli.Main([]string{"status"}, &out, &out, func(k string) string {
		if k == "HOME" {
			return root + "/.home"
		}
		return ""
	}, root)
	return out.String()
}

// executeRun builds a bundle already in phase execute with a two-wave plan
// (task 1 bounded → sonnet, task 2 implement → opus, task 3 depends on 1).
func executeRun(t *testing.T) (string, string) {
	t.Helper()
	return executeRunWith(t)
}

// executeRunWith is executeRun with extra `takt init` flags — used by the
// task-4 addendum to build the same fixture under --autonomy step.
func executeRunWith(t *testing.T, initFlags ...string) (string, string) {
	t.Helper()
	root, bdir := setupRunWith(t, initFlags...)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	idx := `{"schema":1,"spec_hash":"x","tasks":[
 {"id":1,"title":"a","description":"create a.go with package a","files":["a.go"],"verify":["test -f a.go"],"depends_on":[],"goals":[],"class":"bounded"},
 {"id":2,"title":"b","description":"create b.go","files":["b.go"],"verify":["test -f b.go"],"depends_on":[],"goals":[],"class":"implement"},
 {"id":3,"title":"c","description":"create c.go","files":["c.go"],"verify":["test -f c.go"],"depends_on":[1],"goals":[],"class":"docs"}]}`
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", idx)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan\n")
	st, _ := bundle.LoadState(bdir)
	st.Phase = bundle.PhaseExecute
	st.Config.Review.Tasks = true
	st.Tasks = []bundle.Task{
		{ID: 1, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Class: "bounded"},
		{ID: 2, Wave: 0, Status: bundle.StatusPending, Files: []string{"b.go"}, Class: "implement"},
		{ID: 3, Wave: 1, Status: bundle.StatusPending, Files: []string{"c.go"}, Class: "docs"},
	}
	if err := bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "execute fixture")
	return root, bdir
}

// porcelainOutsideBundle is `git status --porcelain` with takt's own bundle
// tree filtered out. A wave commit records the sha of the commit that
// carries it, and that sha cannot exist until the commit does — so
// close.json and events.jsonl are modified for as long as it takes the next
// takt commit (the next slice, or the execute → finish transition) to sweep
// them up (review I1, spec §4.7). What must be clean the moment a wave
// commit lands is everything outside takt's own bookkeeping.
func porcelainOutsideBundle(t *testing.T, root string) string {
	t.Helper()
	var kept []string
	for line := range strings.SplitSeq(testutil.Git(t, root, "status", "--porcelain"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[len(fields)-1], "docs/takt/") {
			continue
		}
		kept = append(kept, strings.TrimSpace(line))
	}
	return strings.Join(kept, "\n")
}

func agentsOf(t *testing.T, o map[string]any) []map[string]any {
	t.Helper()
	raw, ok := o["agents"].([]any)
	if !ok {
		t.Fatalf("not a dispatch op: %v", o)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, a := range raw {
		out = append(out, a.(map[string]any))
	}
	return out
}

//nolint:unparam // status is the digest field under test; today every caller records done
func record(t *testing.T, root string, task, attempt int, status, summary string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "msg.txt")
	_ = os.WriteFile(f, []byte("did things\nSTATUS: "+status+"\nSUMMARY: "+summary+"\nBLOCKERS: none\n"), 0o600)
	code, o, errb := runIn(t, root, nil,
		"record", "--task", strconv.Itoa(task), "--attempt", strconv.Itoa(attempt), "--from", f, "--slug", "demo")
	if code != 0 || o["ignored"] == true {
		t.Fatalf("record %d: %d %v %s", task, code, o, errb)
	}
}

// candidateID matches the ids the verify brief quotes its candidates under
// ("c1", "c2", …) — the shape wave.MergeCandidates assigns them.
var candidateID = regexp.MustCompile(`\bc\d+\b`)

// drainReview is `next`, extended to answer the internal review layer's
// dispatches on the caller's behalf (two-layers design §3.2, §3.3): every
// execute-phase wave in this file's fixtures runs the default six lenses, so
// the first `next` after a wave's last "done" digest now dispatches the lens
// fan-out rather than falling through to whatever the caller was expecting
// next — `exec close-wave`, a gate, the next wave's dispatch. drainReview
// answers a lens dispatch with no findings, so the merged candidate list
// stays empty and the verifier is never dispatched
// (decide.InternalFacts.Done); on the rare path where the verifier is
// dispatched anyway, it answers every candidate false_positive, reading the
// ids straight out of the brief it was handed rather than assuming any. It
// then calls `next` again, and keeps doing so until the op is not a reviewer
// dispatch, which behaves exactly like a bare `next` call for every caller
// that never triggers the internal review at all (the very first dispatch of
// a wave, before any digest is recorded, or any call once one is complete).
func drainReview(t *testing.T, root string, env map[string]string, extra ...string) (int, map[string]any, string) {
	t.Helper()
	for {
		code, o, errb := next(t, root, env, extra...)
		if code != 0 || o["op"] != "dispatch" {
			return code, o, errb
		}
		agents := agentsOf(t, o)
		if len(agents) == 0 || agents[0]["agent"] != "reviewer" {
			return code, o, errb
		}
		attempt := strconv.Itoa(int(o["attempt"].(float64)))
		for _, ag := range agents {
			recordReviewerReply(t, root, env, ag, attempt)
		}
	}
}

// recordReviewerReply answers one lens or verify dispatch of the internal
// review layer with a canned reply and records it.
func recordReviewerReply(t *testing.T, root string, env map[string]string, ag map[string]any, attempt string) {
	t.Helper()
	mode, ok := ag["mode"].(string)
	if !ok {
		t.Fatalf("reviewer dispatch without a mode: %v", ag)
	}
	var body string
	if mode == "verify" {
		b, err := os.ReadFile(ag["brief"].(string))
		if err != nil {
			t.Fatal(err)
		}
		ids := slices.Compact(slices.Sorted(slices.Values(candidateID.FindAllString(string(b), -1))))
		verdicts := make([]string, 0, len(ids))
		for _, id := range ids {
			verdicts = append(verdicts, fmt.Sprintf(
				`{"id":%q,"verdict":"false_positive","evidence":"scripted: no defect found"}`, id))
		}
		body = "```json\n{\"mode\":\"verify\",\"verdicts\":[" + strings.Join(verdicts, ",") + "]}\n```\n"
	} else {
		body = fmt.Sprintf("```json\n{\"lens\":%q,\"findings\":[]}\n```\n", mode)
	}
	f := filepath.Join(t.TempDir(), "reviewer-msg.txt")
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errb := runIn(t, root, env, "record", "--agent", "reviewer", "--mode", mode,
		"--attempt", attempt, "--from", f, "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("record reviewer %s: %d %v %s", mode, code, out, errb)
	}
}

//nolint:gocognit,gocyclo,cyclop // one scripted wave, end to end; splitting it would hide the sequence
func TestWaveLaunchCloseAndCommit(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	testutil.WriteFile(t, root, "notes.txt", "user dirt\n") // must survive untouched and uncommitted

	code, o, errb := drainReview(t, root, nil)
	if code != 0 || o["op"] != "dispatch" || o["wave"] != float64(0) {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	ags := agentsOf(t, o)
	if len(ags) != 2 || ags[0]["model"] != "sonnet" || ags[1]["model"] != "opus" || ags[0]["class"] != "bounded" {
		t.Fatalf("agents = %v", ags)
	}
	brief, _ := os.ReadFile(ags[0]["brief"].(string))
	if !strings.Contains(string(brief), "- a.go") || !strings.Contains(string(brief), "test -f a.go") {
		t.Fatalf("brief = %s", brief)
	}
	st, _ := bundle.LoadState(bdir)
	if st.ActiveWave == nil || len(st.ActiveWave.Tasks) != 2 || len(st.ActiveWave.Baseline) != 1 ||
		st.ActiveWave.Baseline[0].Path != "notes.txt" {
		t.Fatalf("active_wave = %+v", st.ActiveWave)
	}
	// Same session, nothing recorded yet → wait.
	if _, o, _ = drainReview(t, root, nil); o["op"] != "stop" || o["reason"] != "wave_in_flight" {
		t.Fatalf("%v", o)
	}
	// Agents work: task 1 in scope, task 2 in scope plus a stray file.
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	testutil.WriteFile(t, root, "stray.go", "package stray\n")
	record(t, root, 1, 1, "done", "wrote a.go")
	record(t, root, 2, 1, "done", "wrote b.go")
	if _, o, _ = drainReview(
		t,
		root,
		nil,
	); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt close-wave") {
		t.Fatalf("%v", o)
	}
	code, o, errb = runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if _, err := os.Stat(filepath.Join(root, "stray.go")); !os.IsNotExist(err) {
		t.Fatal("out-of-scope file must be reverted")
	}
	if status := porcelainOutsideBundle(t, root); status != "?? notes.txt" {
		t.Fatalf("tree after wave commit: %q", status)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — tasks 1, 2" {
		t.Fatalf("commit = %q", msg)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if c == nil || !c.Committed || len(c.Reverted) != 1 || c.Tasks[0].Review == nil ||
		c.Tasks[0].Review.Verdict != "approve" {
		t.Fatalf("close = %+v", c)
	}
	st, _ = bundle.LoadState(bdir)
	if st.Task(1).Status != bundle.StatusDone || st.Task(2).Status != bundle.StatusDone {
		t.Fatalf("statuses: %+v", st.Tasks)
	}
	// status must show task 1's attempt and the model its digest recorded.
	scode, sout, serrb := runIn(t, root, nil, "status", "--json")
	if scode != 0 {
		t.Fatalf("status: %d %s", scode, serrb)
	}
	items := sout["tasks"].(map[string]any)["items"].([]any)
	var task1 map[string]any
	for _, it := range items {
		if m := it.(map[string]any); m["id"] == float64(1) {
			task1 = m
		}
	}
	if task1 == nil || task1["attempt"] != float64(1) || task1["model"] != "sonnet" {
		t.Fatalf("status task 1 = %v (items %v)", task1, items)
	}
	// next clears the wave and launches wave 1 (task 3, docs → sonnet).
	_, o, _ = drainReview(t, root, nil)
	if o["op"] != "dispatch" || o["wave"] != float64(1) || agentsOf(t, o)[0]["model"] != "sonnet" {
		t.Fatalf("%v", o)
	}
}

func TestVerifyFailureThenRetryEscalates(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "claims b but wrote nothing")
	code, o, _ := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != false {
		t.Fatalf("%d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if len(c.Failed) != 1 || c.Failed[0] != 2 || c.Tasks[1].Reason != "no_changes" {
		t.Fatalf("%+v", c)
	}
	st, _ := bundle.LoadState(bdir)
	if st.Task(1).Status != bundle.StatusDone || st.Task(2).Status != bundle.StatusFailed {
		t.Fatalf("%+v", st.Tasks)
	}
	if _, o, _ = drainReview(t, root, nil); o["op"] != "ask" || o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if rc, _, errb := runIn(t, root, nil,
		"answer", "--gate", "wave_failures", "--choice", "retry", "--slug", "demo"); rc != 0 {
		t.Fatal(errb)
	}
	_, o, _ = drainReview(t, root, nil)
	ags := agentsOf(t, o)
	if len(ags) != 1 || ags[0]["task"] != float64(2) || ags[0]["model"] != "opus" || o["attempt"] != float64(2) {
		t.Fatalf("retry dispatch = %v", o)
	}
	b, _ := os.ReadFile(ags[0]["brief"].(string))
	if !strings.Contains(string(b), "attempt 2") || !strings.Contains(string(b), "no_changes") {
		t.Fatalf("retry brief lacks the failure context: %s", b)
	}
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 2, 2, "done", "b for real")
	if rc, out, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); rc != 0 || out["committed"] != true {
		t.Fatalf("%d %v", rc, out)
	}
	if files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "a.go") ||
		!strings.Contains(files, "b.go") {
		t.Fatalf("the wave commit must carry task 1's earlier work too: %q", files)
	}
}

func TestReworkRedispatchesWithFindingsThenApproves(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"missing test",` +
		`"findings":[{"severity":"major","file":"b.go","line":1,"title":"no test","detail":"add b_test.go"}]}`}
	if code, o, _ := runIn(t, root, rework, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != false {
		t.Fatalf("%d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if len(c.Rework) != 2 {
		t.Fatalf("both reviewed tasks got rework: %+v", c)
	}
	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "dispatch" || o["attempt"] != float64(2) || len(agentsOf(t, o)) != 2 {
		t.Fatalf("%v", o)
	}
	b, _ := os.ReadFile(agentsOf(t, o)[1]["brief"].(string))
	if !strings.Contains(string(b), "add b_test.go") || !strings.Contains(string(b), "opus") {
		t.Fatalf("rework brief: %s", b)
	}
	record(t, root, 1, 2, "done", "a2")
	record(t, root, 2, 2, "done", "b2")
	if rc, out, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); rc != 0 || out["committed"] != true {
		t.Fatalf("%d %v", rc, out)
	}
	// A second rework at max_rework=1 must ask instead of looping.
}

func TestReviewErrorGateSkip(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	broken := map[string]string{"TAKT_FAKE_REVIEW": `not json`}
	runIn(t, root, broken, "close-wave", "--slug", "demo")
	if _, o, _ := drainReview(t, root, nil); o["gate"] != "review_error" {
		t.Fatalf("%v", o)
	}
	if code, _, errb := runIn(t, root, nil, "answer", "--gate", "review_error", "--choice", "skip",
		"--reason", "fake backend broken", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if _, o, _ := drainReview(t, root, nil); o["op"] != "exec" {
		t.Fatalf("skip re-runs close-wave without review: %v", o)
	}
	if code, o, _ := runIn(t, root, broken, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != true {
		t.Fatalf("%d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if c.Tasks[0].Review != nil {
		t.Fatal("skipped tasks are not re-reviewed")
	}
}

func TestRecoveryResetsOnlyUnrecordedTasks(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	a := map[string]string{"TAKT_SESSION": "A"}
	drainReview(t, root, a)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b half-done\n")
	record(t, root, 1, 1, "done", "a")
	// Session A dies. Session B takes over.
	b := map[string]string{"TAKT_SESSION": "B"}
	_, o, _ := drainReview(t, root, b, "--force")
	if o["op"] != "dispatch" || o["attempt"] != float64(2) || len(agentsOf(t, o)) != 1 ||
		agentsOf(t, o)[0]["task"] != float64(2) {
		t.Fatalf("recovery re-dispatches only task 2: %v", o)
	}
	if _, err := os.Stat(filepath.Join(root, "b.go")); !os.IsNotExist(err) {
		t.Fatal("the crashed task's file is reset")
	}
	if _, err := os.Stat(filepath.Join(root, "a.go")); err != nil {
		t.Fatal("the recorded task's work survives")
	}
	st, _ := bundle.LoadState(bdir)
	if st.ActiveWave.Attempt != 2 || len(st.ActiveWave.Tasks) != 1 {
		t.Fatalf("%+v", st.ActiveWave)
	}
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 2, 2, "done", "b")
	if rc, out, _ := runIn(t, root, b, "close-wave", "--slug", "demo"); rc != 0 || out["committed"] != true {
		t.Fatalf("close after recovery must include task 1's attempt-1 digest: %d %v", rc, out)
	}
}

func TestWaiveAndStaleDigest(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	f := filepath.Join(t.TempDir(), "m.txt")
	_ = os.WriteFile(f, []byte("STATUS: blocked\nSUMMARY: cannot\nBLOCKERS: needs schema\n"), 0o600)
	testutil.WriteFile(t, root, "a.go", "package a // as far as task 1 got\n")
	runIn(t, root, nil, "record", "--task", "1", "--attempt", "1", "--from", f, "--slug", "demo")
	if _, o, _ := runIn(t, root, nil,
		"record", "--task", "1", "--attempt", "7", "--from", f, "--slug", "demo"); o["ignored"] != true {
		t.Fatalf("stale attempt must be ignored: %v", o)
	}
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 2, 1, "done", "b")
	runIn(t, root, nil, "close-wave", "--slug", "demo")
	if _, o, _ := drainReview(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	runIn(t, root, nil, "answer", "--gate", "wave_failures", "--choice", "waive", "--slug", "demo")
	if code, _, errb := runIn(t, root, nil,
		"waive", "--task", "1", "--reason", "schema lands later", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	st, _ := bundle.LoadState(bdir)
	if st.Task(1).Status != bundle.StatusWaived {
		t.Fatalf("%+v", st.Tasks)
	}
	// The wave is re-closed, not abandoned: task 2's work is committed as it
	// stands now that the rest of the wave is accounted for (spec §7.4 step 5).
	if _, o, _ := drainReview(t, root, nil); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt close-wave") {
		t.Fatalf("a waived wave must be closed, not dropped: %v", o)
	}
	if code, o, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "b.go") {
		t.Fatalf("the done task's work must be committed: %q", files)
	}
	// Review I8 / spec §7.4 step 5: a waived task's files are committed as
	// they stand. Left in the tree they would be reverted as out of scope by
	// the next wave's scope check, silently undoing what the user accepted.
	if !strings.Contains(files, "a.go") {
		t.Fatalf("the waived task's partial work must be committed too: %q", files)
	}
	if status := porcelainOutsideBundle(t, root); status != "" {
		t.Fatalf("tree after the waived wave commit: %q", status)
	}
	if _, o, _ := drainReview(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
		t.Fatalf("waived task 1 unblocks wave 1: %v", o)
	}
}

// wideRun is executeRun with every task in wave 0, dispatched maxParallel at
// a time.
func wideRun(t *testing.T, maxParallel int) (string, string) {
	t.Helper()
	root, bdir := executeRun(t)
	st, _ := bundle.LoadState(bdir)
	st.Config.MaxParallel = maxParallel
	st.Tasks[2].Wave = 0
	if err := bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "wide fixture")
	return root, bdir
}

// TestWaveSlicesCommitPerSlice covers review finding I1: a wave larger than
// max_parallel commits once per slice, and the task of a later slice — never
// dispatched — neither blocks that commit nor raises a content-free gate.
func TestWaveSlicesCommitPerSlice(t *testing.T) {
	t.Parallel()
	root, bdir := wideRun(t, 2)
	_, o, _ := drainReview(t, root, nil)
	if len(agentsOf(t, o)) != 2 {
		t.Fatalf("a slice is max_parallel tasks: %v", o)
	}
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("the finished slice commits on its own: %d %v %s", code, out, errb)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — tasks 1, 2" {
		t.Fatalf("commit = %q", msg)
	}
	first := sliceRecord(t, bdir, 0, 1) // each slice keeps its own record, named by its number
	// No gate: the wave's remaining task goes out as the next slice.
	_, o, _ = drainReview(t, root, nil)
	ags := agentsOf(t, o)
	if o["op"] != "dispatch" || o["wave"] != float64(0) || len(ags) != 1 || ags[0]["task"] != float64(3) {
		t.Fatalf("second slice: %v", o)
	}
	if n, _ := o["narration"].(string); !strings.Contains(n, "slice 2") {
		t.Fatalf("the second slice must say so: %q", n)
	}
	testutil.WriteFile(t, root, "c.go", "package c\n")
	record(t, root, 3, 1, "done", "c")
	if code, out, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — tasks 3" {
		t.Fatalf("a slice commit names only the tasks it closed: %q", msg)
	}
	sliceRecord(t, bdir, 0, 2)
	if again := sliceRecord(t, bdir, 0, 1); string(again) != string(first) {
		t.Fatalf("slice 2 must not rewrite slice 1's record:\n%s\n%s", first, again)
	}
	if status := porcelainOutsideBundle(t, root); status != "" {
		t.Fatalf("tree after the last slice: %q", status)
	}
	drainReview(t, root, nil) // clears the finished wave
	if _, doc, errb := runIn(t, root, nil, "status", "--json", "--slug", "demo"); doc["active_wave"] != nil {
		t.Fatalf("the last slice's wave must be cleared: %v %s", doc["active_wave"], errb)
	}
	assertLogHas(t, root, "wave 0 — tasks 1, 2", "wave 0 — tasks 3")
}

// sliceRecord asserts that slice s of wave n closed and committed under its
// own number, and returns the record's bytes — so a later slice can be
// checked for having left it alone.
func sliceRecord(t *testing.T, bdir string, waveN, slice int) []byte {
	t.Helper()
	c, err := wave.ReadClose(bdir, waveN, slice)
	if err != nil || c == nil || c.Slice != slice || !c.Committed {
		t.Fatalf("wave %d slice %d record: %v %+v", waveN, slice, err, c)
	}
	b, err := os.ReadFile(wave.ClosePath(bdir, waveN, slice))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertLogHas checks every subject is somewhere in the branch's history.
func assertLogHas(t *testing.T, root string, subjects ...string) {
	t.Helper()
	log := testutil.Git(t, root, "log", "--format=%s")
	for _, want := range subjects {
		if !strings.Contains(log, want) {
			t.Fatalf("missing commit %q in:\n%s", want, log)
		}
	}
}

// TestOldActiveWaveWithoutASliceHeals covers the upgrade path into per-slice
// close records. A wave dispatched by a build from before them has
// active_wave.slice = 0 — the number the old waveBaseline returned — and no
// close record can be written under it, so `next` would keep asking for a
// close-wave that exits 1 with nothing on disk explaining why. takt heals it
// to slice 1, which is the right answer for a wave that has committed
// nothing yet, and `takt doctor` WARNs about the state it came from.
func TestOldActiveWaveWithoutASliceHeals(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	// What the older build left behind.
	st, _ := bundle.LoadState(bdir)
	st.ActiveWave.Slice = 0
	if err := bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}

	if _, o, _ := drainReview(t, root, nil); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt close-wave") {
		t.Fatalf("a fully recorded wave is closed: %v", o)
	}
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("a slice-less wave must still close: %d %v %s", code, out, errb)
	}
	if c, _ := wave.ReadClose(bdir, 0, 1); c == nil || c.Slice != 1 || !c.Committed {
		t.Fatalf("the healed close records itself as slice 1: %+v", c)
	}
	if _, o, _ := drainReview(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
		t.Fatalf("the healed wave clears and the run moves on: %v", o)
	}
	if st, _ = bundle.LoadState(bdir); st.ActiveWave == nil || st.ActiveWave.N != 1 {
		t.Fatalf("wave 0 must have been cleared: %+v", st.ActiveWave)
	}
}

// TestRetryKeepsTheSliceNumber is the other half of the slice counter: a
// slice that failed and is retried from the wave_failures gate comes back as
// the same slice, not as a new one. The counter follows what has committed,
// and an uncommitted slice has not — so its record stays close.s1.json and
// the retry overwrites it rather than leaving a half-graded s1 behind an s2.
func TestRetryKeepsTheSliceNumber(t *testing.T) {
	t.Parallel()
	root, bdir := wideRun(t, 2)
	drainReview(t, root, nil) // slice 1: tasks 1 and 2
	testutil.WriteFile(t, root, "b.go", "package b\n")
	recordReport(t, root, 1, "STATUS: failed\nSUMMARY: ran out of ideas\nBLOCKERS: none\n")
	record(t, root, 2, 1, "done", "b")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, o, _ := drainReview(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if code, _, errb := runIn(t, root, nil,
		"answer", "--gate", "wave_failures", "--choice", "retry", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}

	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "dispatch" || o["attempt"] != float64(2) {
		t.Fatalf("retry dispatch: %v", o)
	}
	// Slice 1 renders as the plain narration; "slice 2" would mean the retry
	// had been counted as a new slice.
	if n, _ := o["narration"].(string); strings.Contains(n, "slice") {
		t.Fatalf("a retry of an uncommitted slice keeps its number: %q", n)
	}
	st, _ := bundle.LoadState(bdir)
	if st.ActiveWave == nil || st.ActiveWave.Slice != 1 {
		t.Fatalf("active_wave = %+v", st.ActiveWave)
	}

	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "c.go", "package c\n")
	for _, id := range st.ActiveWave.Tasks {
		record(t, root, id, 2, "done", "retried")
	}
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if c, _ := wave.ReadClose(bdir, 0, 1); c == nil || c.Slice != 1 || !c.Committed {
		t.Fatalf("the retry's record is still slice 1: %+v", c)
	}
	if _, err := os.Stat(wave.ClosePath(bdir, 0, 2)); !os.IsNotExist(err) {
		t.Fatalf("a retry must not open a second slice: %v", err)
	}
	if _, err := os.Stat(wave.ClosePath(bdir, 0, 1) + ".prev"); !os.IsNotExist(err) {
		t.Fatalf("the retired copy must be gone once the slice closes: %v", err)
	}
}

// TestRecoveredWaveEarlierFailureBlocksTheCommit covers the other half of
// review finding I1: the slice predicate must not look at active_wave.tasks,
// which recovery narrows to the tasks it re-dispatched — a task that ran and
// failed in an earlier attempt is still graded by this close and still blocks.
func TestRecoveredWaveEarlierFailureBlocksTheCommit(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	a := map[string]string{"TAKT_SESSION": "A"}
	drainReview(t, root, a)
	f := filepath.Join(t.TempDir(), "m.txt")
	_ = os.WriteFile(f, []byte("STATUS: failed\nSUMMARY: could not\nBLOCKERS: none\n"), 0o600)
	runIn(t, root, nil, "record", "--task", "1", "--attempt", "1", "--from", f, "--slug", "demo")
	// Task 2 never reports; session B recovers it alone.
	b := map[string]string{"TAKT_SESSION": "B"}
	_, o, _ := drainReview(t, root, b, "--force")
	if len(agentsOf(t, o)) != 1 || o["attempt"] != float64(2) {
		t.Fatalf("recovery re-dispatches only task 2: %v", o)
	}
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 2, 2, "done", "b")
	code, out, _ := runIn(t, root, b, "close-wave", "--slug", "demo")
	if code != 0 || out["committed"] != false {
		t.Fatalf("a task that ran and failed blocks the slice: %d %v", code, out)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if len(c.Failed) != 1 || c.Failed[0] != 1 {
		t.Fatalf("%+v", c)
	}
	if _, gate, _ := drainReview(t, root, b); gate["gate"] != "wave_failures" {
		t.Fatalf("%v", gate)
	}
}

// TestCloseWaveCommitFailureDropsTheRecord covers review finding C1: a close
// whose commit never happened must not leave a record claiming it did, or
// `next` clears the wave and the work is never committed.
func TestCloseWaveCommitFailureDropsTheRecord(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); code == 0 {
		t.Fatal("a close that cannot stage or commit must fail")
	}
	if c, _ := wave.ReadClose(bdir, 0, 1); c != nil && c.Committed {
		t.Fatalf("the record must never outlive the commit it claims: %+v", c)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, o, _ := drainReview(t, root, nil); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt close-wave") {
		t.Fatalf("the wave must be closed again: %v", o)
	}
	if code, o, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "a.go") ||
		!strings.Contains(files, "b.go") {
		t.Fatalf("the retried close commits the work: %q", files)
	}
}

// TestReCloseCarriesEarlierResultsForward covers review finding M2: a close
// grades only the tasks that are still pending, so the record it writes must
// inherit the results of the ones an earlier round already judged.
func TestReCloseCarriesEarlierResultsForward(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); code == 0 {
		t.Fatal("a rejected commit must fail the close")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if code, o, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if c == nil || len(c.Tasks) != 2 {
		t.Fatalf("the re-close keeps both tasks' results: %+v", c)
	}
	for _, tr := range c.Tasks {
		if len(tr.FilesChanged) == 0 || len(tr.Verify) == 0 || tr.Review == nil {
			t.Fatalf("task %d lost its evidence: %+v", tr.Task, tr)
		}
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — tasks 1, 2" {
		t.Fatalf("commit = %q", msg)
	}
}

// recordReport records one raw agent report, whatever status it carries.
func recordReport(t *testing.T, root string, task int, body string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "r.txt")
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := runIn(t, root, nil,
		"record", "--task", strconv.Itoa(task), "--attempt", "1", "--from", f, "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
}

// waiveOne answers the wave_failures gate with waive and waives one task.
func waiveOne(t *testing.T, root string, task int, reason string) {
	t.Helper()
	if code, _, errb := runIn(t, root, nil,
		"answer", "--gate", "wave_failures", "--choice", "waive", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if code, _, errb := runIn(t, root, nil,
		"waive", "--task", strconv.Itoa(task), "--reason", reason, "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
}

// TestPartialWaiveGateNamesTheRemainingFailure covers review finding N1: a
// close grades only pending tasks, so after a partial waive it grades none of
// them — the record it writes must still name the task that is actually
// holding the wave, and must not name the one just waived.
func TestPartialWaiveGateNamesTheRemainingFailure(t *testing.T) {
	t.Parallel()
	root, bdir := wideRun(t, 8)
	drainReview(t, root, nil)
	recordReport(t, root, 1, "STATUS: blocked\nSUMMARY: cannot\nBLOCKERS: needs schema\n")
	recordReport(t, root, 2, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	testutil.WriteFile(t, root, "c.go", "package c\n")
	record(t, root, 3, 1, "done", "c")
	runIn(t, root, nil, "close-wave", "--slug", "demo")
	if _, o, _ := drainReview(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}

	waiveOne(t, root, 1, "schema lands later")
	if _, o, _ := drainReview(t, root, nil); o["op"] != "exec" {
		t.Fatalf("a waived wave is re-closed: %v", o)
	}
	if code, o, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != false {
		t.Fatalf("task 2 still holds the wave: %d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if len(c.Failed) != 1 || c.Failed[0] != 2 || len(c.Blocked) != 0 {
		t.Fatalf("the record must name the still-failed task and drop the waived one: %+v", c)
	}
	_, o, _ := drainReview(t, root, nil)
	if o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if got := fmt.Sprint(o["context"].(map[string]any)["failed"]); got != "[2]" {
		t.Fatalf("the returning gate must name task 2, got failed=%s in %v", got, o["context"])
	}

	// Waiving the last failure lets the wave commit the work that is there.
	waiveOne(t, root, 2, "out of scope")
	drainReview(t, root, nil)
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "c.go") {
		t.Fatalf("the done task's work must be committed: %q", files)
	}
	if status := porcelainOutsideBundle(t, root); status != "" {
		t.Fatalf("tree: %q", status)
	}
}

// TestAllWaivedWaveCommitSubject covers the ride-along minor: a wave whose
// dispatched work was all waived still commits its bundle, and its subject
// names the waivers rather than trailing off after "tasks".
func TestAllWaivedWaveCommitSubject(t *testing.T) {
	t.Parallel()
	root, _ := executeRun(t)
	drainReview(t, root, nil)
	recordReport(t, root, 1, "STATUS: blocked\nSUMMARY: cannot\nBLOCKERS: needs schema\n")
	recordReport(t, root, 2, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	runIn(t, root, nil, "close-wave", "--slug", "demo")
	drainReview(t, root, nil)
	waiveOne(t, root, 1, "schema lands later")
	drainReview(t, root, nil)
	runIn(t, root, nil, "close-wave", "--slug", "demo")
	drainReview(t, root, nil)
	waiveOne(t, root, 2, "out of scope")
	drainReview(t, root, nil)
	if code, o, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — waived 1, 2" {
		t.Fatalf("commit = %q", msg)
	}
	if _, o, _ := drainReview(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
		t.Fatalf("an all-waived wave still unblocks the next one: %v", o)
	}
}

// TestStatusTextOmitsModelUntilDigest covers fix round 1: Task.Attempt is
// set at dispatch, before any digest exists (launch.go's renderTaskBrief),
// so guarding the status text render on Attempt==0 rendered a
// dispatched-but-unrecorded task as "(bounded, attempt 1, )" — a trailing
// comma and an empty model. The guard must be on Model=="" instead.
func TestStatusTextOmitsModelUntilDigest(t *testing.T) {
	t.Parallel()
	root, _ := executeRun(t)
	if _, o, _ := drainReview(t, root, nil); o["op"] != "dispatch" {
		t.Fatalf("%v", o)
	}
	before := statusText(t, root)
	if strings.Contains(before, ", )") {
		t.Fatalf("dispatched-but-unrecorded task must not render an empty model: %s", before)
	}
	if !strings.Contains(before, "#1 wave 0 pending (bounded)\n") {
		t.Fatalf("task 1 before any digest = %s", before)
	}
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "wrote a.go")
	after := statusText(t, root)
	if !strings.Contains(after, "#1 wave 0 pending (bounded, attempt 1, sonnet)\n") {
		t.Fatalf("task 1 after its digest = %s", after)
	}
}

// TestCloseWaveTwiceIsANoOp covers review I1 (spec §5.4, "every op is safe to
// execute twice"): the session may replay an `exec takt close-wave`, so a
// second run after a successful close must grade nothing, write nothing and
// make no second commit — it reprints the record that already landed.
func TestCloseWaveTwiceIsANoOp(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	code, first, errb := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || first["committed"] != true || first["commit"] == "" {
		t.Fatalf("%d %v %s", code, first, errb)
	}
	head := testutil.Git(t, root, "rev-parse", "HEAD")
	before, err := os.ReadFile(wave.ClosePath(bdir, 0, 1))
	if err != nil {
		t.Fatal(err)
	}

	code, second, errb := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 {
		t.Fatalf("a replayed close must succeed: %d %s", code, errb)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatalf("a replayed close must reprint the same record:\n%s\n%s", a, b)
	}
	after, err := os.ReadFile(wave.ClosePath(bdir, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the replay rewrote close.s1.json:\n%s\n%s", before, after)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	if c == nil || len(c.Tasks) != 2 {
		t.Fatalf("the record must keep the tasks it graded: %+v", c)
	}
	if h := testutil.Git(t, root, "rev-parse", "HEAD"); h != head {
		t.Fatalf("the replay moved HEAD: %s → %s", head, h)
	}
	log := testutil.Git(t, root, "log", "--format=%s")
	if n := strings.Count(log, "wave 0 — tasks"); n != 1 {
		t.Fatalf("exactly one wave commit, got %d:\n%s", n, log)
	}
}

// TestCrashInsideWaveCommitIsReconciled covers review I2 (spec §5.4): a
// close record is written before `git commit` runs, so a crash inside the
// commit leaves committed:true with no sha. `next` must check that claim
// against git rather than believing it — clearing the wave on the record's
// word alone strands the work uncommitted with nothing pointing at it.
func TestCrashInsideWaveCommitIsReconciled(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	// The state a process killed inside `git commit` leaves behind.
	if err := wave.WriteClose(bdir, wave.CloseResult{
		Wave: 0, Slice: 1, Attempt: 1, ClosedAt: time.Now().UTC(), Committed: true,
		Failed: []int{}, Blocked: []int{}, Rework: []int{}, ReviewErrors: []int{},
	}); err != nil {
		t.Fatal(err)
	}

	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "exec" || !strings.HasPrefix(o["command"].(string), "takt close-wave") {
		t.Fatalf("a commit git cannot confirm must be re-closed, not cleared: %v", o)
	}
	st, _ := bundle.LoadState(bdir)
	if st.ActiveWave == nil {
		t.Fatal("the wave must stay active until its commit really lands")
	}
	code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "a.go") || !strings.Contains(files, "b.go") {
		t.Fatalf("the reconciled close commits the work: %q", files)
	}
	if status := porcelainOutsideBundle(t, root); status != "" {
		t.Fatalf("tree after the reconciled close: %q", status)
	}
	log := testutil.Git(t, root, "log", "--format=%s")
	if n := strings.Count(log, "wave 0 — tasks"); n != 1 {
		t.Fatalf("exactly one wave commit, got %d:\n%s", n, log)
	}
	if _, o, _ = drainReview(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
		t.Fatalf("the reconciled wave clears and the run moves on: %v", o)
	}
}

// soloWaveRun is executeRun with wave 0 narrowed to task 1, so a reviewer
// that always asks for rework exhausts exactly one task and the wave's fate
// rests on that one waiver.
func soloWaveRun(t *testing.T) (string, string) {
	t.Helper()
	root, bdir := executeRun(t)
	st, _ := bundle.LoadState(bdir)
	st.Tasks[1].Wave = 1
	if err := bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "solo fixture")
	return root, bdir
}

// TestWaiveAcceptsAReworkExhaustedTask covers review I3: a task the reviewer
// keeps sending back stays `pending` (spec §4.3), so once it runs out of
// rework attempts the wave_failures gate names it under `exhausted` and tells
// the user to waive it — and `waive` refused, leaving the run with no way
// out of the gate it had just raised.
func TestWaiveAcceptsAReworkExhaustedTask(t *testing.T) {
	t.Parallel()
	root, bdir := soloWaveRun(t)
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"needs a test",` +
		`"findings":[{"severity":"major","file":"a.go","line":1,"title":"no test","detail":"add a_test.go"}]}`}
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "a")
	if code, o, errb := runIn(t, root, rework, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != false {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	// max_rework is 1: one retry, then the gate.
	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "dispatch" || o["attempt"] != float64(2) {
		t.Fatalf("the first rework is retried: %v", o)
	}
	record(t, root, 1, 2, "done", "a again")
	if code, out, errb := runIn(t, root, rework, "close-wave", "--slug", "demo"); code != 0 {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, o, _ = drainReview(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if got := fmt.Sprint(o["context"].(map[string]any)["exhausted"]); got != "[1]" {
		t.Fatalf("the gate must name the exhausted task, got exhausted=%s in %v", got, o["context"])
	}
	st, _ := bundle.LoadState(bdir)
	if st.Task(1).Status != bundle.StatusPending {
		t.Fatalf("a rework-exhausted task is still pending: %+v", st.Tasks)
	}

	waiveOne(t, root, 1, "the missing test lands with task 3")
	if st, _ = bundle.LoadState(bdir); st.Task(1).Status != bundle.StatusWaived {
		t.Fatalf("waive must accept a rework-exhausted task: %+v", st.Tasks)
	}
	if _, o, _ = drainReview(t, root, nil); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt close-wave") {
		t.Fatalf("the waived wave is re-closed: %v", o)
	}
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "a.go") {
		t.Fatalf("the waived task's work is committed as it stands: %q", files)
	}
	if status := porcelainOutsideBundle(t, root); status != "" {
		t.Fatalf("tree: %q", status)
	}
}

// TestWaiveRefusesAPendingTaskThatHasNotRun keeps the other half of review
// I3 honest: `waive` widened to tasks the reviewer has actually sent back,
// not to every pending one. A task with no attempt behind it has produced
// nothing to accept, and a task of a later wave is not the user's decision
// to make yet.
func TestWaiveRefusesAPendingTaskThatHasNotRun(t *testing.T) {
	t.Parallel()
	root, _ := soloWaveRun(t)
	if code, _, errb := runIn(t, root, nil,
		"waive", "--task", "1", "--reason", "not yet", "--slug", "demo"); code == 0 {
		t.Fatalf("a task that has never run must not be waivable: %s", errb)
	}
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"needs a test"}`}
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "a")
	runIn(t, root, rework, "close-wave", "--slug", "demo")
	if code, _, errb := runIn(t, root, nil,
		"waive", "--task", "2", "--reason", "wrong wave", "--slug", "demo"); code == 0 {
		t.Fatalf("a task of a later wave must not be waivable: %s", errb)
	}
}

// TestRetryMeasuresAgainstTheWaveBaseline covers review M1: answering
// wave_failures with `retry` clears active_wave so the relaunch comes back
// as a fresh slice — and a baseline captured then would record the failed
// attempt's half-written files as pre-existing. The retry that finally gets
// the file right without rewriting it would then look like it changed
// nothing at all, fail as no_changes, and leave the work uncommitted.
func TestRetryMeasuresAgainstTheWaveBaseline(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	// Task 1 gives up, but leaves what it had written behind — which is, as
	// it happens, exactly right. Task 2 finishes.
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	recordReport(t, root, 1, "STATUS: failed\nSUMMARY: ran out of ideas\nBLOCKERS: none\n")
	record(t, root, 2, 1, "done", "b")
	if code, o, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != false {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if _, o, _ := drainReview(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if code, _, errb := runIn(t, root, nil,
		"answer", "--gate", "wave_failures", "--choice", "retry", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "dispatch" || o["attempt"] != float64(2) || len(agentsOf(t, o)) != 1 {
		t.Fatalf("retry dispatch: %v", o)
	}
	st, _ := bundle.LoadState(bdir)
	for _, e := range st.ActiveWave.Baseline {
		if e.Path == "a.go" {
			t.Fatalf("the retry must measure against the tree the wave started from: %+v", st.ActiveWave.Baseline)
		}
	}

	// The attempt-2 agent reads a.go, finds it correct, and touches nothing.
	record(t, root, 1, 2, "done", "a was already right")
	code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	c, _ := wave.ReadClose(bdir, 0, 1)
	i := slices.IndexFunc(c.Tasks, func(tr wave.TaskResult) bool { return tr.Task == 1 })
	if i < 0 || c.Tasks[i].Status != bundle.StatusDone {
		t.Fatalf("task 1 must be done, not no_changes: %+v", c.Tasks)
	}
	if files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(files, "a.go") {
		t.Fatalf("the retried task's file must be in the wave commit: %q", files)
	}
	if _, err := os.Stat(wave.BaselinePath(bdir, 0)); !os.IsNotExist(err) {
		t.Fatalf("the parked baseline must be dropped once the wave commits: %v", err)
	}
}

// TestRecordRejectsATaskTheWaveNeverDispatched covers review M3 and spec
// §13: a `record` for a task this run does not have, or for one the active
// wave never dispatched, is a mis-wired session, not a late report — it
// exits 1 rather than being swallowed as "ignored", which hid the mistake
// and let the wave close as though the task had never been asked for.
func TestRecordRejectsATaskTheWaveNeverDispatched(t *testing.T) {
	t.Parallel()
	root, _ := executeRun(t)
	drainReview(t, root, nil) // dispatches wave 0: tasks 1 and 2
	msg := filepath.Join(t.TempDir(), "m.txt")
	if err := os.WriteFile(msg, []byte("STATUS: done\nSUMMARY: s\nBLOCKERS: none\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, errb := runIn(t, root, nil, "record", "--task", "9", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 1 || !strings.Contains(errb, "no task 9") {
		t.Fatalf("an unknown task id must exit 1 with a JSON error: %d %s", code, errb)
	}
	code, _, errb = runIn(t, root, nil, "record", "--task", "3", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 1 || !strings.Contains(errb, "not in the active wave") {
		t.Fatalf("a task of a later wave must exit 1 with a JSON error: %d %s", code, errb)
	}
	// A stale attempt of a task the wave really did dispatch stays ignored.
	code, o, errb := runIn(t, root, nil, "record", "--task", "1", "--attempt", "7", "--from", msg, "--slug", "demo")
	if code != 0 || o["ignored"] != true {
		t.Fatalf("a late report from a replaced attempt is ignored, not an error: %d %v %s", code, o, errb)
	}
}

// TestPostCommitKillBackfillsTheSha covers the one window recordCloseOutcome
// documents but nothing closed: the wave commit lands and the process dies
// before the record learns its sha, leaving committed:true with no sha —
// which waveCommitLanded reads as "not landed". Re-closing from there grades
// nothing and stages nothing (the work is already in HEAD), so the run
// bounces between `exec close-wave` and a record git will not confirm.
// `next` reconciles instead: HEAD's subject is this close's own and the
// wave's files have nothing outstanding, so the sha is backfilled from HEAD,
// no second commit is made, and the wave clears.
func TestPostCommitKillBackfillsTheSha(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	_, o, _ := drainReview(t, root, nil)
	for _, ag := range agentsOf(t, o) {
		for _, f := range declaredFiles(t, ag["brief"].(string)) {
			testutil.WriteFile(t, root, f, "package x\n")
		}
		record(t, root, int(ag["task"].(float64)), 1, "done", "ok")
	}
	_, o, _ = drainReview(t, root, nil)
	if code, _, errb := runIn(t, root, nil, strings.Fields(o["command"].(string))[1:]...); code != 0 {
		t.Fatalf("close-wave: %d %s", code, errb)
	}
	// Forge the crash window: the commit landed, the record never learned
	// its sha.
	c, err := wave.LatestClose(bdir, 0)
	if err != nil || c == nil {
		t.Fatalf("%v %+v", err, c)
	}
	head := testutil.Git(t, root, "rev-parse", "HEAD")
	c.CommitSHA = ""
	if err = wave.WriteClose(bdir, *c); err != nil {
		t.Fatal(err)
	}
	before := testutil.Git(t, root, "rev-list", "--count", "HEAD")

	_, o, _ = drainReview(t, root, nil)
	if o["op"] != "dispatch" || o["wave"] != float64(1) {
		t.Fatalf("the reconciled wave clears and the run moves on: %v", o)
	}
	if after := testutil.Git(t, root, "rev-list", "--count", "HEAD"); after != before {
		t.Fatalf("backfill must not commit again: %s → %s", before, after)
	}
	if c, err = wave.LatestClose(bdir, 0); err != nil || c == nil || c.CommitSHA != head {
		t.Fatalf("commit_sha must be backfilled from HEAD (%s): %v %+v", head, err, c)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	backfilled := false
	for _, e := range events {
		if e.Type != "wave_committed" || e.Data["backfilled"] != true || e.Data["sha"] != head {
			continue
		}
		backfilled = true
		// The repair's event says what the commit it names carried, exactly
		// as the one recordCloseOutcome would have written does.
		if got := fmt.Sprint(e.Data["tasks"]); got != "[1 2]" {
			t.Fatalf("the backfilled event must name the tasks: %s in %+v", got, e.Data)
		}
	}
	if !backfilled {
		t.Fatalf("the repair must be in the log: %+v", events)
	}
}

// TestSliceReCloseSubjectNamesOnlyItsOwnSlice covers what a re-close falls
// back to when it grades nothing: "the wave's done tasks" is, by the time a
// second slice is closing, the answer the first slice already committed
// under — so the second slice's commit would carry a subject naming tasks it
// had nothing to do with, and a reader of the log would see the same wave
// commit twice. The fallback is this slice's own tasks.
func TestSliceReCloseSubjectNamesOnlyItsOwnSlice(t *testing.T) {
	t.Parallel()
	root, _ := wideRun(t, 2)
	drainReview(t, root, nil) // slice 1: tasks 1 and 2
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — tasks 1, 2" {
		t.Fatalf("slice 1's commit: %q", msg)
	}
	// Slice 2 goes out, fails, and is waived — so its re-close grades
	// nothing at all.
	drainReview(t, root, nil)
	recordReport(t, root, 3, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	if code, out, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != false {
		t.Fatalf("%d %v", code, out)
	}
	drainReview(t, root, nil) // raises wave_failures
	waiveOne(t, root, 3, "lands with the next run")
	drainReview(t, root, nil)
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	msg := testutil.Git(t, root, "log", "-1", "--format=%s")
	if msg == "takt(demo): wave 0 — tasks 1, 2" {
		t.Fatalf("slice 2 must not commit under slice 1's subject: %q", msg)
	}
	if !strings.Contains(msg, "3") {
		t.Fatalf("slice 2's subject must name its own task: %q", msg)
	}
}

// TestRetryCarriesTheEarlierRoundsResultsForward covers what answering
// wave_failures with `retry` used to throw away. The failed round had
// already verified and reviewed the tasks that passed, and a close grades
// only what is still pending — so the retry's own close wrote a record
// holding the retried task alone, and the evidence for everything it did not
// re-grade (verify output, review findings, files_changed) was gone from the
// wave's story. Retiring the record instead of leaving it to be overwritten
// is what lets the re-close carry those results forward.
func TestRetryCarriesTheEarlierRoundsResultsForward(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "a")
	recordReport(t, root, 2, "STATUS: failed\nSUMMARY: ran out of ideas\nBLOCKERS: none\n")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, o, _ := drainReview(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if code, _, errb := runIn(t, root, nil,
		"answer", "--gate", "wave_failures", "--choice", "retry", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "dispatch" || o["attempt"] != float64(2) || len(agentsOf(t, o)) != 1 {
		t.Fatalf("retry dispatch: %v", o)
	}
	// The retired record is the only copy of what judged task 2, and the
	// retry brief is written from it.
	b, err := os.ReadFile(agentsOf(t, o)[0]["brief"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ran out of ideas") {
		t.Fatalf("the retry brief must still quote the failure it is retrying: %s", b)
	}
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 2, 2, "done", "b")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	c, err := wave.ReadClose(bdir, 0, 1)
	if err != nil || c == nil {
		t.Fatalf("%v %+v", err, c)
	}
	i := slices.IndexFunc(c.Tasks, func(tr wave.TaskResult) bool { return tr.Task == 1 })
	if i < 0 || len(c.Tasks[i].Verify) == 0 || c.Tasks[i].Review == nil || len(c.Tasks[i].FilesChanged) == 0 {
		t.Fatalf("task 1's evidence from the first round must survive the retry: %+v", c.Tasks)
	}
}

// TestBackfillRetiresTheParkedBaseline covers the other half of what the
// interrupted recordCloseOutcome would have done: it deletes the baseline a
// retry parked, because the next slice must start from the tree the commit
// left. Repairing the record without that left the parked copy on disk, and
// the next launch of the wave prefers it — coming up as slice 1 again, with
// the tree the failed attempt started in, and closing over the record of the
// slice that had just committed.
func TestBackfillRetiresTheParkedBaseline(t *testing.T) {
	t.Parallel()
	root, bdir := wideRun(t, 2)
	parked := retryLandsSliceOne(t, root, bdir)
	// The crash window, in full: the commit landed, and neither the sha nor
	// the deletion of the parked baseline reached the disk.
	if werr := os.WriteFile(wave.BaselinePath(bdir, 0), parked, 0o600); werr != nil {
		t.Fatal(werr)
	}
	blankTheSha(t, bdir)

	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "dispatch" || len(agentsOf(t, o)) != 1 || agentsOf(t, o)[0]["task"] != float64(3) {
		t.Fatalf("the reconciled wave launches its next slice: %v", o)
	}
	st, _ := bundle.LoadState(bdir)
	if st.ActiveWave == nil || st.ActiveWave.Slice != 2 {
		t.Fatalf("the repaired slice has committed, so the next one is slice 2: %+v", st.ActiveWave)
	}
	if _, serr := os.Stat(wave.BaselinePath(bdir, 0)); !os.IsNotExist(serr) {
		t.Fatalf("the landed wave's parked baseline must be gone: %v", serr)
	}
	testutil.WriteFile(t, root, "c.go", "package c\n")
	record(t, root, 3, 1, "done", "c")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	first, err := wave.ReadClose(bdir, 0, 1)
	if err != nil || first == nil || len(first.Tasks) != 2 {
		t.Fatalf("slice 1's record must survive with both its tasks: %v %+v", err, first)
	}
	second, err := wave.ReadClose(bdir, 0, 2)
	if err != nil || second == nil || len(second.Tasks) != 1 || second.Tasks[0].Task != 3 {
		t.Fatalf("slice 2 must keep its own record: %v %+v", err, second)
	}
}

// retryLandsSliceOne drives slice 1 of a wideRun(2) through a failed round, a
// wave_failures retry and the close that finally commits it, and returns the
// baseline the retry parked on its way out — the copy recordCloseOutcome
// deletes once the wave commits, and the one a kill in that window leaves
// behind.
func retryLandsSliceOne(t *testing.T, root, bdir string) []byte {
	t.Helper()
	drainReview(t, root, nil) // slice 1: tasks 1 and 2
	recordReport(t, root, 1, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	recordReport(t, root, 2, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, o, _ := drainReview(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if code, _, errb := runIn(t, root, nil,
		"answer", "--gate", "wave_failures", "--choice", "retry", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	parked, err := os.ReadFile(wave.BaselinePath(bdir, 0))
	if err != nil {
		t.Fatalf("the retry must park a baseline: %v", err)
	}
	drainReview(t, root, nil) // the retry, at attempt 2
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 2, "done", "a")
	record(t, root, 2, 2, "done", "b")
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	return parked
}

// blankTheSha rewrites the wave's newest close record without its commit
// sha: the half of the crash window that makes the record claim a commit
// waveCommitLanded cannot confirm.
func blankTheSha(t *testing.T, bdir string) {
	t.Helper()
	c, err := wave.LatestClose(bdir, 0)
	if err != nil || c == nil {
		t.Fatalf("%v %+v", err, c)
	}
	c.CommitSHA = ""
	if err = wave.WriteClose(bdir, *c); err != nil {
		t.Fatal(err)
	}
}

// TestBackfillDeclinesASubjectThatNamesNoSlice covers the limit of the
// repair: it identifies a commit by the subject the close would have
// written, and a slice whose work was all waived writes the wave's waiver
// list — the same sentence every other slice of that wave would write, and
// "close" when there are no waivers either. A record whose task results
// never reached the disk can therefore be matched against a commit that is
// not its own, so the repair declines and the wave is re-closed the older
// way instead.
func TestBackfillDeclinesASubjectThatNamesNoSlice(t *testing.T) {
	t.Parallel()
	root, bdir := wideRun(t, 2)
	waiveSliceAway(t, root, 1, 2) // slice 1: tasks 1 and 2, waived and committed
	waiveSliceAway(t, root, 3)    // slice 2: task 3, likewise
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — waived 1, 2, 3" {
		t.Fatalf("the all-waived slice commits under the wave's waiver list: %q", msg)
	}
	// The crash window for a record that never got its task results written
	// either: what is left names no slice at all.
	c, err := wave.ReadClose(bdir, 0, 2)
	if err != nil || c == nil {
		t.Fatalf("%v %+v", err, c)
	}
	c.CommitSHA, c.Tasks = "", nil
	if err = wave.WriteClose(bdir, *c); err != nil {
		t.Fatal(err)
	}

	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "exec" || !strings.HasPrefix(o["command"].(string), "takt close-wave") {
		t.Fatalf("an unidentifiable subject must fall back to closing the wave again: %v", o)
	}
	if again, rerr := wave.ReadClose(bdir, 0, 2); rerr != nil || again != nil {
		t.Fatalf("the record must have been retired, not repaired: %v %+v", rerr, again)
	}
}

// waiveSliceAway launches the next slice, fails every task in it, waives
// them one by one through the wave_failures gate, and closes the wave —
// which commits the bundle under the wave's waiver list, since the slice has
// nothing of its own to show.
func waiveSliceAway(t *testing.T, root string, tasks ...int) {
	t.Helper()
	_, o, _ := drainReview(t, root, nil)
	if o["op"] != "dispatch" || len(agentsOf(t, o)) != len(tasks) {
		t.Fatalf("expected a dispatch of tasks %v: %v", tasks, o)
	}
	for _, id := range tasks {
		recordReport(t, root, id, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	}
	if code, out, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	for _, id := range tasks {
		drainReview(t, root, nil) // raises wave_failures
		waiveOne(t, root, id, "out of scope")
		drainReview(t, root, nil) // exec close-wave
		if code, _, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 {
			t.Fatalf("%d %s", code, errb)
		}
	}
}

// TestRecordFlagsBeatParsedTrailer covers the override half of the record
// contract, which no other test exercises: `record`'s helper always goes
// through --from. recordTask's `cmp.Or(in.status, s), ...` puts the explicit
// flags ahead of whatever parseReport pulled out of the trailer, so a report
// whose trailer says failed must still land as the digest the flags name.
func TestRecordFlagsBeatParsedTrailer(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	drainReview(t, root, nil) // dispatches wave 0 so task 1 attempt 1 is in the active wave
	f := filepath.Join(t.TempDir(), "m.txt")
	body := "STATUS: failed\nSUMMARY: parsed summary\nBLOCKERS: parsed blocker\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, o, errb := runIn(t, root, nil,
		"record", "--task", "1", "--attempt", "1", "--from", f,
		"--status", "done", "--summary", "s", "--blockers", "none", "--slug", "demo")
	if code != 0 || o["ignored"] == true {
		t.Fatalf("record: %d %v %s", code, o, errb)
	}
	st, _ := bundle.LoadState(bdir)
	var d struct {
		Status   string `json:"status"`
		Summary  string `json:"summary"`
		Blockers string `json:"blockers"`
	}
	if err := json.Unmarshal(st.Task(1).LastDigest, &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "done" || d.Summary != "s" || d.Blockers != "none" {
		t.Fatalf("--status/--summary/--blockers must beat the parsed trailer: %+v", d)
	}
}
