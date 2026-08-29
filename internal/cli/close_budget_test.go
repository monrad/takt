//nolint:testpackage // tests an unexported helper
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/deadline"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

// budgetCfg is the config half of a close budget: verify_timeout at its
// shipped 10m and a copilot reviewer at the 15m A1 raised the default to.
func budgetCfg() config.Config {
	return config.Config{
		VerifyTimeout: config.Duration(10 * time.Minute),
		Backends: config.Backends{
			Reviewer: []string{"copilot"},
			Copilot:  config.Backend{Timeout: config.Duration(15 * time.Minute)},
		},
	}
}

// budgetState is a run whose active wave 0 holds n pending tasks, with
// review.tasks on and max_parallel 8 — the wave the worked example in the
// spec's A2.1 sizes.
func budgetState(n int) *bundle.State {
	st := &bundle.State{
		Slug: "demo", Phase: bundle.PhaseExecute,
		Config:     bundle.RunConfig{MaxParallel: 8, Review: bundle.ReviewConfig{Tasks: true}},
		ActiveWave: &bundle.ActiveWave{N: 0, Slice: 1, Attempt: 1},
	}
	for id := 1; id <= n; id++ {
		st.Tasks = append(st.Tasks, bundle.Task{ID: id, Wave: 0, Status: bundle.StatusPending, Attempt: 1})
		st.ActiveWave.Tasks = append(st.ActiveWave.Tasks, id)
	}
	return st
}

// budgetIndex gives every named task two verify commands and nothing else
// the budget reads.
func budgetIndex(ids ...int) plan.Index {
	idx := plan.Index{Schema: 1}
	for _, id := range ids {
		idx.Tasks = append(idx.Tasks, plan.Task{
			ID: id, Title: "t", Verify: []string{"go build ./...", "go test ./..."},
		})
	}
	return idx
}

// TestCloseBudgetCountsTheWave pins what the close's own deadline is derived
// from (spec A2.2): the active wave's pending tasks, their verify commands
// from the plan index, and the reviewer's timeout when review.tasks is on.
// The 8×2 wave is the worked example — 8 × 2 × 10m of serial verify plus a
// review round — and the assertion that it exceeds 30m is the whole point of
// deriving the number: 30m is the fixed constant it replaces, and this wave
// could spend nearly three hours in verify alone under it.
func TestCloseBudgetCountsTheWave(t *testing.T) {
	t.Parallel()
	cfg, st := budgetCfg(), budgetState(8)
	full := budgetIndex(1, 2, 3, 4, 5, 6, 7, 8)
	want := deadline.Budget{
		VerifyTimeout: 10 * time.Minute, VerifyCommands: 16,
		BackendTimeout: 15 * time.Minute, ReviewTasks: 8, MaxParallel: 8,
	}
	got := closeBudget(cfg, st, full)
	if got != want {
		t.Fatalf("closeBudget = %+v, want %+v", got, want)
	}
	if d := deadline.Close(got); d <= 30*time.Minute {
		t.Fatalf("the wave the old 30m constant could not hold budgets %s", d)
	}

	// review.tasks off makes no backend call, so no task is a reviewed one —
	// the verify commands are still every bit of work the close has to do.
	st.Config.Review.Tasks = false
	if b := closeBudget(cfg, st, full); b.ReviewTasks != 0 || b.VerifyCommands != 16 {
		t.Fatalf("review.tasks off: %+v", b)
	}
	st.Config.Review.Tasks = true

	// A task the index does not hold runs no verify command (closeWave cannot
	// run commands it cannot read), but it is still a task the wave grades.
	if b := closeBudget(cfg, st, budgetIndex(1, 2, 3, 4, 5, 6, 7)); b.VerifyCommands != 14 || b.ReviewTasks != 8 {
		t.Fatalf("a task absent from the index contributes no commands: %+v", b)
	}

	// Another wave's task is not this close's work, and a task that is no
	// longer pending is not graded by it — neither is counted.
	st.Tasks = append(st.Tasks,
		bundle.Task{ID: 9, Wave: 1, Status: bundle.StatusPending, Attempt: 1},
		bundle.Task{ID: 10, Wave: 0, Status: bundle.StatusDone, Attempt: 1},
		bundle.Task{ID: 11, Wave: 0, Status: bundle.StatusWaived, Attempt: 1},
	)
	if b := closeBudget(cfg, st, budgetIndex(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)); b != want {
		t.Fatalf("only the active wave's pending tasks count: %+v, want %+v", b, want)
	}
}

// closeFixture is the smallest bundle `takt close-wave` will act on: one
// pending task of an active wave 0, its file already in the tree, and no
// plan index at all. Every test below writes or withholds plan.index.json
// itself, because whether the close can read it is the question.
func closeFixture(t *testing.T) (string, string) {
	t.Helper()
	root := testutil.NewRepo(t)
	bdir := filepath.Join(root, "docs", "takt", "demo")
	testutil.WriteFile(t, root, "a.go", "package a\n")
	st := &bundle.State{
		Slug: "demo", Topic: "demo", Phase: bundle.PhaseExecute, Branch: "takt/demo",
		Config: bundle.RunConfig{MaxParallel: 8},
		Tasks: []bundle.Task{
			{ID: 1, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Attempt: 1},
		},
		ActiveWave: &bundle.ActiveWave{
			N: 0, Slice: 1, Attempt: 1, StartedAt: time.Now().UTC(), Tasks: []int{1},
		},
	}
	if err := bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "close fixture")
	return root, bdir
}

// runCloseWaveIn drives `takt close-wave --slug demo` in root and decodes
// whatever it printed.
func runCloseWaveIn(t *testing.T, root string) (int, map[string]any, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cmdCloseWave(Env{
		Args: []string{"--slug", "demo"}, Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			if k == "HOME" {
				return filepath.Join(root, ".home")
			}
			return ""
		},
		Cwd: root,
	})
	var got map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON: %q", out.String())
		}
	}
	return code, got, errb.String()
}

// TestCloseWaveRefusesWhenTheIndexCannotBeRead covers the working path of
// spec A2.2: the plan index is what the close's deadline is counted from, so
// a close that cannot read it fails and names the file instead of falling
// back to a zero budget — which would floor the deadline at deadline.Floor
// while the close ran real work, the exact containment the budget exists to
// establish. Nothing is graded and no commit is made.
func TestCloseWaveRefusesWhenTheIndexCannotBeRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, index string }{
		{"missing", ""},
		{"malformed", `{"schema":1,`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, bdir := closeFixture(t)
			if tc.index != "" {
				testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", tc.index)
			}
			head := testutil.Git(t, root, "rev-parse", "HEAD")
			code, out, errb := runCloseWaveIn(t, root)
			if code == 0 {
				t.Fatalf("a close that cannot read the plan it budgets from must fail: %v", out)
			}
			if !strings.Contains(errb, "plan.index.json") {
				t.Fatalf("the failure must name the file: %s", errb)
			}
			c, err := wave.ReadClose(bdir, 0, 1)
			if err != nil || c != nil {
				t.Fatalf("no close may be attempted: %+v %v", c, err)
			}
			if h := testutil.Git(t, root, "rev-parse", "HEAD"); h != head {
				t.Fatalf("the refused close moved HEAD: %s → %s", head, h)
			}
		})
	}
}

// TestLandedCloseReplaysWithoutTheIndex pins the fast path against the
// restructure A2.2 asks for: a close whose commit is already in HEAD is
// answered under deadline.Bootstrap from the record and git alone, so the
// replay the session may issue at any time stays a no-op even for a bundle
// whose plan.index.json has since gone missing (review I1, spec §5.4).
func TestLandedCloseReplaysWithoutTheIndex(t *testing.T) {
	t.Parallel()
	root, bdir := closeFixture(t)
	head := testutil.Git(t, root, "rev-parse", "HEAD")
	rec := wave.CloseResult{
		Wave: 0, Slice: 1, Attempt: 1, ClosedAt: time.Now().UTC(),
		Committed: true, CommitSHA: head,
		Tasks:  []wave.TaskResult{{Task: 1, Status: bundle.StatusDone}},
		Failed: []int{}, Blocked: []int{}, Rework: []int{}, ReviewErrors: []int{},
	}
	if err := wave.WriteClose(bdir, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexPath(bdir)); err == nil {
		t.Fatal("the fixture must not hold a plan index for this test to mean anything")
	}
	before, err := os.ReadFile(wave.ClosePath(bdir, 0, 1))
	if err != nil {
		t.Fatal(err)
	}

	code, out, errb := runCloseWaveIn(t, root)
	if code != 0 {
		t.Fatalf("a landed close must replay as a no-op: %d %s", code, errb)
	}
	if out["committed"] != true || out["commit"] != head || out["wave"] != float64(0) {
		t.Fatalf("the replay must reprint the landed record: %v", out)
	}
	after, err := os.ReadFile(wave.ClosePath(bdir, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the replay rewrote close.s1.json:\n%s\n%s", before, after)
	}
	if h := testutil.Git(t, root, "rev-parse", "HEAD"); h != head {
		t.Fatalf("the replay moved HEAD: %s → %s", head, h)
	}
}
