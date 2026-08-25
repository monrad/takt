package cli_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

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
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "?? notes.txt" {
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
	if _, o, _ := next(t, root, nil); o["op"] != "dispatch" || o["wave"] != float64(1) {
		t.Fatalf("waived task 1 unblocks wave 1: %v", o)
	}
}
