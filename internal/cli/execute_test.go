package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	root, bdir := setupRun(t)
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

//nolint:gocognit,gocyclo,cyclop // one scripted wave, end to end; splitting it would hide the sequence
func TestWaveLaunchCloseAndCommit(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	testutil.WriteFile(t, root, "notes.txt", "user dirt\n") // must survive untouched and uncommitted

	code, o, errb := next(t, root, nil)
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
	if _, o, _ = next(t, root, nil); o["op"] != "stop" || o["reason"] != "wave_in_flight" {
		t.Fatalf("%v", o)
	}
	// Agents work: task 1 in scope, task 2 in scope plus a stray file.
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	testutil.WriteFile(t, root, "stray.go", "package stray\n")
	record(t, root, 1, 1, "done", "wrote a.go")
	record(t, root, 2, 1, "done", "wrote b.go")
	if _, o, _ = next(t, root, nil); o["op"] != "exec" || !strings.HasPrefix(o["command"].(string), "takt close-wave") {
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
	c, _ := wave.ReadClose(bdir, 0)
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
	_, o, _ = next(t, root, nil)
	if o["op"] != "dispatch" || o["wave"] != float64(1) || agentsOf(t, o)[0]["model"] != "sonnet" {
		t.Fatalf("%v", o)
	}
}

func TestVerifyFailureThenRetryEscalates(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	next(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "claims b but wrote nothing")
	code, o, _ := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != false {
		t.Fatalf("%d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0)
	if len(c.Failed) != 1 || c.Failed[0] != 2 || c.Tasks[1].Reason != "no_changes" {
		t.Fatalf("%+v", c)
	}
	st, _ := bundle.LoadState(bdir)
	if st.Task(1).Status != bundle.StatusDone || st.Task(2).Status != bundle.StatusFailed {
		t.Fatalf("%+v", st.Tasks)
	}
	if _, o, _ = next(t, root, nil); o["op"] != "ask" || o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if rc, _, errb := runIn(t, root, nil,
		"answer", "--gate", "wave_failures", "--choice", "retry", "--slug", "demo"); rc != 0 {
		t.Fatal(errb)
	}
	_, o, _ = next(t, root, nil)
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
	next(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	rework := map[string]string{"TAKT_FAKE_REVIEW": `{"verdict":"rework","summary":"missing test",` +
		`"findings":[{"severity":"major","file":"b.go","line":1,"title":"no test","detail":"add b_test.go"}]}`}
	if code, o, _ := runIn(t, root, rework, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != false {
		t.Fatalf("%d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0)
	if len(c.Rework) != 2 {
		t.Fatalf("both reviewed tasks got rework: %+v", c)
	}
	_, o, _ := next(t, root, nil)
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
	next(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	broken := map[string]string{"TAKT_FAKE_REVIEW": `not json`}
	runIn(t, root, broken, "close-wave", "--slug", "demo")
	if _, o, _ := next(t, root, nil); o["gate"] != "review_error" {
		t.Fatalf("%v", o)
	}
	if code, _, errb := runIn(t, root, nil, "answer", "--gate", "review_error", "--choice", "skip",
		"--reason", "fake backend broken", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if _, o, _ := next(t, root, nil); o["op"] != "exec" {
		t.Fatalf("skip re-runs close-wave without review: %v", o)
	}
	if code, o, _ := runIn(t, root, broken, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != true {
		t.Fatalf("%d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0)
	if c.Tasks[0].Review != nil {
		t.Fatal("skipped tasks are not re-reviewed")
	}
}

func TestRecoveryResetsOnlyUnrecordedTasks(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	a := map[string]string{"TAKT_SESSION": "A"}
	next(t, root, a)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b half-done\n")
	record(t, root, 1, 1, "done", "a")
	// Session A dies. Session B takes over.
	b := map[string]string{"TAKT_SESSION": "B"}
	_, o, _ := next(t, root, b, "--force")
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
	next(t, root, nil)
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
	if _, o, _ := next(t, root, nil); o["gate"] != "wave_failures" {
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
	if _, o, _ := next(t, root, nil); o["op"] != "exec" ||
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
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
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
	root, _ := wideRun(t, 2)
	_, o, _ := next(t, root, nil)
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
	// No gate: the wave's remaining task goes out as the next slice.
	_, o, _ = next(t, root, nil)
	ags := agentsOf(t, o)
	if o["op"] != "dispatch" || o["wave"] != float64(0) || len(ags) != 1 || ags[0]["task"] != float64(3) {
		t.Fatalf("second slice: %v", o)
	}
	testutil.WriteFile(t, root, "c.go", "package c\n")
	record(t, root, 3, 1, "done", "c")
	if code, out, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || out["committed"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — tasks 3" {
		t.Fatalf("a slice commit names only the tasks it closed: %q", msg)
	}
	if status := porcelainOutsideBundle(t, root); status != "" {
		t.Fatalf("tree after the last slice: %q", status)
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
	next(t, root, a)
	f := filepath.Join(t.TempDir(), "m.txt")
	_ = os.WriteFile(f, []byte("STATUS: failed\nSUMMARY: could not\nBLOCKERS: none\n"), 0o600)
	runIn(t, root, nil, "record", "--task", "1", "--attempt", "1", "--from", f, "--slug", "demo")
	// Task 2 never reports; session B recovers it alone.
	b := map[string]string{"TAKT_SESSION": "B"}
	_, o, _ := next(t, root, b, "--force")
	if len(agentsOf(t, o)) != 1 || o["attempt"] != float64(2) {
		t.Fatalf("recovery re-dispatches only task 2: %v", o)
	}
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 2, 2, "done", "b")
	code, out, _ := runIn(t, root, b, "close-wave", "--slug", "demo")
	if code != 0 || out["committed"] != false {
		t.Fatalf("a task that ran and failed blocks the slice: %d %v", code, out)
	}
	c, _ := wave.ReadClose(bdir, 0)
	if len(c.Failed) != 1 || c.Failed[0] != 1 {
		t.Fatalf("%+v", c)
	}
	if _, gate, _ := next(t, root, b); gate["gate"] != "wave_failures" {
		t.Fatalf("%v", gate)
	}
}

// TestCloseWaveCommitFailureDropsTheRecord covers review finding C1: a close
// whose commit never happened must not leave a record claiming it did, or
// `next` clears the wave and the work is never committed.
func TestCloseWaveCommitFailureDropsTheRecord(t *testing.T) {
	t.Parallel()
	root, bdir := executeRun(t)
	next(t, root, nil)
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
	if c, _ := wave.ReadClose(bdir, 0); c != nil && c.Committed {
		t.Fatalf("the record must never outlive the commit it claims: %+v", c)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, o, _ := next(t, root, nil); o["op"] != "exec" ||
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
	next(t, root, nil)
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
	c, _ := wave.ReadClose(bdir, 0)
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
	next(t, root, nil)
	recordReport(t, root, 1, "STATUS: blocked\nSUMMARY: cannot\nBLOCKERS: needs schema\n")
	recordReport(t, root, 2, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	testutil.WriteFile(t, root, "c.go", "package c\n")
	record(t, root, 3, 1, "done", "c")
	runIn(t, root, nil, "close-wave", "--slug", "demo")
	if _, o, _ := next(t, root, nil); o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}

	waiveOne(t, root, 1, "schema lands later")
	if _, o, _ := next(t, root, nil); o["op"] != "exec" {
		t.Fatalf("a waived wave is re-closed: %v", o)
	}
	if code, o, _ := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != false {
		t.Fatalf("task 2 still holds the wave: %d %v", code, o)
	}
	c, _ := wave.ReadClose(bdir, 0)
	if len(c.Failed) != 1 || c.Failed[0] != 2 || len(c.Blocked) != 0 {
		t.Fatalf("the record must name the still-failed task and drop the waived one: %+v", c)
	}
	_, o, _ := next(t, root, nil)
	if o["gate"] != "wave_failures" {
		t.Fatalf("%v", o)
	}
	if got := fmt.Sprint(o["context"].(map[string]any)["failed"]); got != "[2]" {
		t.Fatalf("the returning gate must name task 2, got failed=%s in %v", got, o["context"])
	}

	// Waiving the last failure lets the wave commit the work that is there.
	waiveOne(t, root, 2, "out of scope")
	next(t, root, nil)
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
	next(t, root, nil)
	recordReport(t, root, 1, "STATUS: blocked\nSUMMARY: cannot\nBLOCKERS: needs schema\n")
	recordReport(t, root, 2, "STATUS: failed\nSUMMARY: gave up\nBLOCKERS: none\n")
	runIn(t, root, nil, "close-wave", "--slug", "demo")
	next(t, root, nil)
	waiveOne(t, root, 1, "schema lands later")
	next(t, root, nil)
	runIn(t, root, nil, "close-wave", "--slug", "demo")
	next(t, root, nil)
	waiveOne(t, root, 2, "out of scope")
	next(t, root, nil)
	if code, o, errb := runIn(t, root, nil, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(demo): wave 0 — waived 1, 2" {
		t.Fatalf("commit = %q", msg)
	}
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
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
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" {
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
	next(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	code, first, errb := runIn(t, root, nil, "close-wave", "--slug", "demo")
	if code != 0 || first["committed"] != true || first["commit"] == "" {
		t.Fatalf("%d %v %s", code, first, errb)
	}
	head := testutil.Git(t, root, "rev-parse", "HEAD")
	before, err := os.ReadFile(wave.ClosePath(bdir, 0))
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
	after, err := os.ReadFile(wave.ClosePath(bdir, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the replay rewrote close.json:\n%s\n%s", before, after)
	}
	c, _ := wave.ReadClose(bdir, 0)
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
	next(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 1, 1, "done", "a")
	record(t, root, 2, 1, "done", "b")
	// The state a process killed inside `git commit` leaves behind.
	if err := wave.WriteClose(bdir, wave.CloseResult{
		Wave: 0, Attempt: 1, ClosedAt: time.Now().UTC(), Committed: true,
		Failed: []int{}, Blocked: []int{}, Rework: []int{}, ReviewErrors: []int{},
	}); err != nil {
		t.Fatal(err)
	}

	_, o, _ := next(t, root, nil)
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
	if _, o, _ = next(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
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
	next(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "a")
	if code, o, errb := runIn(t, root, rework, "close-wave", "--slug", "demo"); code != 0 || o["committed"] != false {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	// max_rework is 1: one retry, then the gate.
	_, o, _ := next(t, root, nil)
	if o["op"] != "dispatch" || o["attempt"] != float64(2) {
		t.Fatalf("the first rework is retried: %v", o)
	}
	record(t, root, 1, 2, "done", "a again")
	if code, out, errb := runIn(t, root, rework, "close-wave", "--slug", "demo"); code != 0 {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, o, _ = next(t, root, nil); o["gate"] != "wave_failures" {
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
	if _, o, _ = next(t, root, nil); o["op"] != "exec" ||
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
	next(t, root, nil)
	testutil.WriteFile(t, root, "a.go", "package a\n")
	record(t, root, 1, 1, "done", "a")
	runIn(t, root, rework, "close-wave", "--slug", "demo")
	if code, _, errb := runIn(t, root, nil,
		"waive", "--task", "2", "--reason", "wrong wave", "--slug", "demo"); code == 0 {
		t.Fatalf("a task of a later wave must not be waivable: %s", errb)
	}
}
