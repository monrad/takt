//nolint:testpackage // drives the unexported gatherFacts against the unexported closeBudget
package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/deadline"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/testutil"
)

// These are integration tests over one real bundle on disk. The unit tests
// either side of this seam — close_budget_test.go for the binary's budget,
// internal/decide for the session's deadline — each prove their own half
// against numbers the test itself supplies. What neither can see is whether
// the two halves are fed the same numbers, and that is the whole containment
// claim of spec A2.2 and goal G4: deadline.Session's margin only contains
// anything if the budget it is applied to is the budget the binary applies to
// itself. So these drive the real gatherFacts over a real repository and
// compare its answer, field for field, with the real closeBudget.

// The fixture's own numbers, named because mnd would flag them and because
// each is a claim about what the bundle below holds.
const (
	// factsMaxParallel is the run's frozen max_parallel — deliberately not
	// the config's, since both sides must read it off state, not off config.
	factsMaxParallel = 5
	// factsCfgMaxParallel is the config's, which neither side may use.
	factsCfgMaxParallel = 3
	// factsVerifyTimeout is a non-default verify_timeout, so a hard-coded
	// 10m anywhere in the plumbing shows up as a mismatch.
	factsVerifyTimeout = 7 * time.Minute
	// factsClaudeTimeout and factsCopilotTimeout differ, and the reviewer
	// chain names "fake" first — which has no config key — so the budget's
	// backend timeout can only come from ReviewBudgetTimeout's answer.
	factsClaudeTimeout  = 21 * time.Minute
	factsCopilotTimeout = 9 * time.Minute
	// factsWaveCommands is 2+3+1 verify commands over the wave's four
	// pending tasks: task 4 is absent from the index and contributes none.
	factsWaveCommands = 6
	// factsWaveTasks is those four pending wave-0 tasks — including task 3,
	// which is pending in wave 0 but not in the dispatch's own list, the
	// shape a recovery leaves behind.
	factsWaveTasks = 4
)

// factsPlan is the plan the fixture bundle holds. Every task's verify list
// is distinct and of a distinct length, so a wave-share miscount cannot
// coincide with the whole union's count. Task 4 is deliberately absent.
func factsPlan() plan.Index {
	return plan.Index{Schema: 1, Tasks: []plan.Task{
		{ID: 1, Title: "one", Files: []string{"a.go"}, Verify: []string{"go build ./...", "go vet ./..."}},
		{
			ID: 2, Title: "two", Files: []string{"b.go"},
			Verify: []string{"go test ./x", "go test ./y", "go test ./z"},
		},
		{ID: 3, Title: "three", Files: []string{"c.go"}, Verify: []string{"gofmt -l ."}},
		{ID: 5, Title: "five", Files: []string{"e.go"}, Verify: []string{"go test ./done"}},
		{ID: 6, Title: "six", Files: []string{"f.go"}, Verify: []string{"go test ./wave1"}},
	}}
}

// factsCfg is a config that agrees with no default: a non-default
// verify_timeout, a max_parallel the run's frozen one contradicts, and a
// reviewer chain whose first entry has no config key at all.
func factsCfg() config.Config {
	return config.Config{
		MaxParallel:   factsCfgMaxParallel,
		VerifyTimeout: config.Duration(factsVerifyTimeout),
		Backends: config.Backends{
			Reviewer: []string{"fake", "claude"},
			Claude:   config.Backend{Timeout: config.Duration(factsClaudeTimeout)},
			Copilot:  config.Backend{Timeout: config.Duration(factsCopilotTimeout)},
		},
	}
}

// factsState is the run the fixture holds, in the phase the caller names.
// Its task set is the interesting part: tasks 1 and 2 are the dispatch's own
// list, 3 is pending in the same wave but outside it (what a recovery
// leaves), 4 is pending and missing from the plan index, 5 is done and 6
// belongs to another wave — the last two must not be counted by either side.
func factsState(phase string) *bundle.State {
	st := &bundle.State{
		Schema: 1, Slug: "demo", Topic: "demo", Phase: phase,
		Branch: "takt/demo", Base: "main",
		Config: bundle.RunConfig{
			Autonomy: "auto", MaxParallel: factsMaxParallel, MaxRework: 1,
			Review: bundle.ReviewConfig{Tasks: true},
		},
		Tasks: []bundle.Task{
			{ID: 1, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Attempt: 1},
			{ID: 2, Wave: 0, Status: bundle.StatusPending, Files: []string{"b.go"}, Attempt: 1},
			{ID: 3, Wave: 0, Status: bundle.StatusPending, Files: []string{"c.go"}, Attempt: 1},
			{ID: 4, Wave: 0, Status: bundle.StatusPending, Files: []string{"d.go"}, Attempt: 1},
			{ID: 5, Wave: 0, Status: bundle.StatusDone, Files: []string{"e.go"}, Attempt: 1},
			{ID: 6, Wave: 1, Status: bundle.StatusPending, Files: []string{"f.go"}, Attempt: 1},
		},
	}
	if phase == bundle.PhaseExecute {
		st.ActiveWave = &bundle.ActiveWave{
			N: 0, Slice: 1, Attempt: 1, StartedAt: time.Now().UTC(), SessionID: "S", Tasks: []int{1, 2},
		}
	}
	return st
}

// factsBundle writes the whole bundle to a fresh repository and commits it,
// so the git reads gatherFinishFacts makes have something to answer.
func factsBundle(t *testing.T, phase string) (*workspace, string, *bundle.State) {
	t.Helper()
	root := testutil.NewRepo(t)
	ws := factsWorkspace(t, root)
	bdir := ws.Dir.Bundle("demo")
	raw, err := json.Marshal(factsPlan())
	if err != nil {
		t.Fatal(err)
	}
	rel, err := ws.Dir.RelToRepo(bdir)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, filepath.Join(rel, "spec.md"), "# spec\n")
	testutil.WriteFile(t, root, filepath.Join(rel, "goals.md"), "# goals\n")
	testutil.WriteFile(t, root, filepath.Join(rel, "plan.md"), "# plan\n")
	testutil.WriteFile(t, root, filepath.Join(rel, "plan.index.json"), string(raw))
	st := factsState(phase)
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "facts fixture")
	return ws, bdir, st
}

// factsWorkspace opens root with the config the fixture wants, built
// directly rather than through openWorkspace: the point is a config that
// agrees with no default, which a loaded .takt.json would have to validate.
func factsWorkspace(t *testing.T, root string) *workspace {
	t.Helper()
	repo, err := gitx.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := bundle.ResolveDir(repo.Root, filepath.Join(root, ".home"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return &workspace{Repo: repo, Cfg: factsCfg(), Dir: dir, Home: filepath.Join(root, ".home")}
}

// TestGatheredFactsMatchTheBinaryBudget is the containment claim of goal G4
// stated where it can actually be checked: the facts gatherFacts hands
// internal/decide describe exactly the work closeBudget hands
// [deadline.Close]. Both count the active wave's pending tasks, both take
// max_parallel off the run's frozen state and not off config, and both take
// the backend timeout through ReviewBudgetTimeout — so the session's deadline
// for `exec takt close-wave` is Session of the very cap the binary applies to
// itself, and Session's margin is a real margin rather than a number computed
// from a different wave.
//
// The two counts are asserted non-zero first: a fill that silently stayed at
// its zero value would make every equality below trivially true while the
// session budgeted a floor for a wave with real work in it.
func TestGatheredFactsMatchTheBinaryBudget(t *testing.T) {
	t.Parallel()
	ws, bdir, st := factsBundle(t, bundle.PhaseExecute)
	f, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
	if err != nil {
		t.Fatal(err)
	}
	if f.Wave.VerifyCommands != factsWaveCommands || f.Wave.ReviewTasks != factsWaveTasks {
		t.Fatalf("the wave's work must be counted: %d commands, %d tasks",
			f.Wave.VerifyCommands, f.Wave.ReviewTasks)
	}
	if f.BackendTimeout != factsClaudeTimeout || f.VerifyTimeout != factsVerifyTimeout {
		t.Fatalf("per-unit caps: backend %s, verify %s", f.BackendTimeout, f.VerifyTimeout)
	}
	// The work is counted off the PARSED index, not the validated one. This
	// fixture's plan does not validate against its stub spec and goals, and
	// the count above is still the real one — which is the point: readIndex
	// gives close-wave the same parse-only view, so counting only a valid
	// index would floor the session's deadline while the binary budgeted
	// real work.
	if f.IndexValid {
		t.Fatal("the fixture's plan must not validate, or this claim is untested")
	}
	// The five fields internal/decide assembles for its `exec takt
	// close-wave` op, in the same order it assembles them.
	gathered := deadline.Budget{
		VerifyTimeout:  f.VerifyTimeout,
		VerifyCommands: f.Wave.VerifyCommands,
		BackendTimeout: f.BackendTimeout,
		ReviewTasks:    f.Wave.ReviewTasks,
		MaxParallel:    st.Config.MaxParallel,
	}
	idx, err := readIndex(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if binary := closeBudget(ws.Cfg, st, idx); gathered != binary {
		t.Fatalf("the session budgets %+v, the binary %+v", gathered, binary)
	}

	t.Run("review.tasks off drops the review term on both sides", func(t *testing.T) {
		t.Parallel()
		offWS, offBdir, offST := factsBundle(t, bundle.PhaseExecute)
		offST.Config.Review.Tasks = false
		offF, ferr := gatherFacts(t.Context(), offWS, offBdir, offST, false, false, time.Now().UTC(), "S")
		if ferr != nil {
			t.Fatal(ferr)
		}
		offIdx, ierr := readIndex(offBdir)
		if ierr != nil {
			t.Fatal(ierr)
		}
		binary := closeBudget(offWS.Cfg, offST, offIdx)
		if offF.Wave.ReviewTasks != 0 || binary.ReviewTasks != 0 {
			t.Fatalf("a run that makes no backend call reviews nothing: %d vs %d",
				offF.Wave.ReviewTasks, binary.ReviewTasks)
		}
		// The verify commands are still every bit of work the close has to
		// do, so dropping the reviews must not drop them too.
		if offF.Wave.VerifyCommands != factsWaveCommands || binary.VerifyCommands != factsWaveCommands {
			t.Fatalf("verify is unaffected by review.tasks: %d vs %d",
				offF.Wave.VerifyCommands, binary.VerifyCommands)
		}
	})
}

// TestGatheredFinishFactsCountTheVerifyUnion is the same claim for row 20:
// the deadline the session puts on `exec takt verify` is sized from the union
// `takt verify` will actually run — every task's commands plus the user's
// extras, deduplicated — and not from the wave's share of them, nor from a
// constant. The union is computed here through the very functions
// verifyAtHead assembles it from, so the two can only agree by reading the
// same disk.
func TestGatheredFinishFactsCountTheVerifyUnion(t *testing.T) {
	t.Parallel()
	ws, bdir, st := factsBundle(t, bundle.PhaseFinish)
	// A user-supplied command belongs to the union as much as a planned one,
	// and it is the half a count taken off the plan index alone would miss.
	if err := finish.AppendExtra(bdir, "make lint"); err != nil {
		t.Fatal(err)
	}
	f, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := readIndex(bdir)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := finish.ReadExtra(bdir)
	if err != nil {
		t.Fatal(err)
	}
	want := len(finish.UnionCommands(idx, extra))
	if want == 0 {
		t.Fatal("the fixture must hold a non-empty union for this test to mean anything")
	}
	if f.Finish.VerifyCommands != want {
		t.Fatalf("finish verify commands = %d, want the union's %d", f.Finish.VerifyCommands, want)
	}
	// The union is the whole plan's, not the active wave's share of it —
	// counting the wave would be the fail-open the derived deadline exists
	// to close.
	if f.Finish.VerifyCommands <= factsWaveCommands {
		t.Fatalf("the union must exceed one wave's share: %d", f.Finish.VerifyCommands)
	}
}

// brokenBundle is one way the fixture stops answering the questions the
// deadline facts ask of it: what the case is called, and what it does to the
// bundle on disk before the facts are gathered.
type brokenBundle struct {
	name    string
	corrupt func(t *testing.T, ws *workspace, bdir string)
}

// indexGarbage is bytes plan.ParseIndex refuses, as against bytes that
// parse into a plan with nothing in it — an empty plan is a readable index.
const indexGarbage = `{"schema":1,`

// unreadableIndexes is the two ways a plan index stops being one: the file
// is gone, or its bytes are not a plan. readIndex reports both, and both
// must reach the facts as "no index" rather than as an empty one.
func unreadableIndexes() []brokenBundle {
	return []brokenBundle{{"missing", removeIndex}, {"malformed", corruptIndex}}
}

// removeIndex deletes plan.index.json from the worktree every reader reads.
func removeIndex(t *testing.T, ws *workspace, bdir string) {
	t.Helper()
	testutil.Git(t, ws.Repo.Root, "rm", "-q", bundleFile(t, ws, bdir, "plan.index.json"))
}

// corruptIndex leaves the file in place holding bytes no reader can parse.
func corruptIndex(t *testing.T, ws *workspace, bdir string) {
	t.Helper()
	testutil.WriteFile(t, ws.Repo.Root, bundleFile(t, ws, bdir, "plan.index.json"), indexGarbage)
}

// corruptExtras does the same to the user's half of the verify union, which
// finish.ReadExtra reads and reports on exactly as readIndex does.
func corruptExtras(t *testing.T, ws *workspace, bdir string) {
	t.Helper()
	testutil.WriteFile(t, ws.Repo.Root, bundleFile(t, ws, bdir, "finish", "verify-extra.json"), "[")
}

// bundleFile is a path inside the fixture's bundle, relative to the repo
// root — which is what testutil's writers and git take.
func bundleFile(t *testing.T, ws *workspace, bdir string, parts ...string) string {
	t.Helper()
	rel, err := ws.Dir.RelToRepo(bdir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(append([]string{rel}, parts...)...)
}

// TestWaveFactsBudgetNothingForAnUnreadableIndex is the nil-index end of the
// same containment. closeWaveBudgeted fails on readIndex before it builds a
// budget or makes one backend call, so the work a close performs in this
// state is none — and the facts have to say so for both counts, not just for
// the verify one. Counting the wave's pending tasks as reviews would size
// the session's deadline for reviews that cannot happen, and would
// contradict what [decide.WaveFacts] says the fields mean.
//
// The same fixture counts 6 commands and 4 review tasks with its index
// intact (TestGatheredFactsMatchTheBinaryBudget), so a zero here is the
// unreadable index's doing and not an empty wave's.
func TestWaveFactsBudgetNothingForAnUnreadableIndex(t *testing.T) {
	t.Parallel()
	for _, tc := range unreadableIndexes() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, bdir, st := factsBundle(t, bundle.PhaseExecute)
			if !st.Config.Review.Tasks {
				t.Fatal("the fixture must have review.tasks on, or the claim is vacuous")
			}
			tc.corrupt(t, ws, bdir)
			// The execute phase reads the index softly — a plan nobody can
			// read is row 8's business, and `takt next` has to reach row 8 to
			// say so — so the gather itself must still succeed.
			f, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
			if err != nil {
				t.Fatal(err)
			}
			if f.Wave.VerifyCommands != 0 || f.Wave.ReviewTasks != 0 {
				t.Fatalf("an index nobody can read budgets no work: %d commands, %d tasks",
					f.Wave.VerifyCommands, f.Wave.ReviewTasks)
			}
			if _, err = readIndex(bdir); err == nil {
				t.Fatal("the binary must fail on this index, or the wave facts are wrong to floor")
			}
		})
	}
}

// TestGatherFinishFactsPropagatesAnIndexReadError is the error path of the
// finish seam. A verify union that cannot be counted is not a run with zero
// verify commands: mapping it to zero would emit an exec op whose timeout was
// computed from Verify(per, 0) while `takt verify` went on to run a real
// union — the fail-open containment break closeWaveBudgeted refuses on the
// binary side. The extras file is the other half of that union, so it fails
// the same way.
//
// The git-side finish facts are gathered either way: it is the count, and
// only the count, that an unreadable plan takes down.
func TestGatherFinishFactsPropagatesAnIndexReadError(t *testing.T) {
	t.Parallel()
	for _, tc := range append(unreadableIndexes(), brokenBundle{"malformed extras", corruptExtras}) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, bdir, st := factsBundle(t, bundle.PhaseFinish)
			tc.corrupt(t, ws, bdir)
			if _, err := gatherFinishFacts(t.Context(), ws, bdir, st); err != nil {
				t.Fatalf("the git-side finish facts read no plan: %v", err)
			}
			_, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
			if err == nil {
				t.Fatal("a union that cannot be counted must not be counted as zero commands")
			}
		})
	}
}

// TestVerifiedHEADNeedsNoCommandCount is the economy the count is gathered
// under. Row 20 is its only consumer, so once HEAD is verified nothing reads
// it — and a plan index that goes unreadable after verification must not fail
// the goals, retro, branch_finish, push_pr and archive rows, none of which
// ever read the plan. Counting it unconditionally would make every
// finish-phase `takt next` hard-fail on a file it has no use for.
func TestVerifiedHEADNeedsNoCommandCount(t *testing.T) {
	t.Parallel()
	for _, tc := range unreadableIndexes() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, bdir, st := factsBundle(t, bundle.PhaseFinish)
			head := testutil.Git(t, ws.Repo.Root, "rev-parse", "HEAD")
			st.VerifiedSHA = &head
			tc.corrupt(t, ws, bdir)
			f, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
			if err != nil {
				t.Fatalf("a verified run reads no plan index: %v", err)
			}
			if !f.Finish.Verified || f.Finish.VerifyCommands != 0 {
				t.Fatalf("verified %v, commands %d", f.Finish.Verified, f.Finish.VerifyCommands)
			}
		})
	}
}
