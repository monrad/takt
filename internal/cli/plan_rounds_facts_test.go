//nolint:testpackage // drives the unexported gatherFacts over an unexported workspace
package cli

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/testutil"
)

// These are integration tests over one real bundle on disk, for the fill that
// feeds the plan gate's round cap (spec §3, goal G3). internal/decide proves
// its half against a PlanRounds it supplies itself; internal/gate proves that
// Rounds counts one gate's reviews since that gate's newest reset. What
// neither can see is whether the number Decide reads is the plan gate's count
// at all — a fill that counted the spec gate's events, or counted the whole
// log, or ran outside the guarded branch, passes both of those suites and
// fails here.

// The two counts the interleaved log below must produce. They are different
// on purpose: a fill that read the other gate's count cannot come out right
// by coincidence.
const (
	wantPlanRounds = 2
	wantSpecRounds = 1
)

// planRoundsSpec is the bundle's spec.md. The index binds to its hash, so the
// two move together.
const planRoundsSpec = "# spec\n\n## Assumptions & Open Decisions\n| q | d | r | s |\n"

// planRoundsGoals declares the one goal the index's tasks claim, in the shape
// the goals step writes.
const planRoundsGoals = "# Goals — demo\n\n## Anchor\n```text\nAdd a greeting\n```\n\n## Goals\n" +
	"- G1 — greet works · signal: test · evidence: go test ./...\n"

// planRoundsPlan is a non-empty plan.md — the guard's last conjunct.
const planRoundsPlan = "# plan\n"

// planRoundsIndexTmpl validates against the spec and goals above once its
// spec_hash is filled in, which is what makes IndexValid true.
const planRoundsIndexTmpl = `{"schema":1,"spec_hash":"%s","tasks":[
 {"id":1,"title":"a","description":"add a","files":["a.go"],"verify":["true"],"depends_on":[],"goals":["G1"],"class":"bounded"},
 {"id":2,"title":"b","description":"add b","files":["b.go"],"verify":["true"],"depends_on":[1],"goals":["G1"],"class":"implement"}]}`

// planRoundsFixture is one bundle on disk, spelled as the bytes that make the
// plan branch's guard hold or fail.
type planRoundsFixture struct {
	// reviewPlan is the run's frozen review.plan.
	reviewPlan bool
	// index is what plan.index.json holds; "" writes no file at all.
	index string
	// planMD is what plan.md holds; "" leaves it empty on disk.
	planMD string
}

// planRoundsGuardCase is a fixture that breaks the guard, with the assertion
// that says which conjunct it broke — so a zero PlanRounds is attributable to
// the guard rather than to a fixture that fell apart somewhere else.
type planRoundsGuardCase struct {
	name    string
	fixture planRoundsFixture
	reason  func(t *testing.T, f decide.Facts)
}

// guardedFixture is the bundle every conjunct of the guard holds for: plan
// review on, a valid index, a non-empty plan.md.
func guardedFixture() planRoundsFixture {
	return planRoundsFixture{reviewPlan: true, index: planRoundsValidIndex(), planMD: planRoundsPlan}
}

// planRoundsValidIndex is the index bound to this fixture's spec.
func planRoundsValidIndex() string {
	return fmt.Sprintf(planRoundsIndexTmpl, goals.Hash([]byte(planRoundsSpec)))
}

// planRoundsState is the run the fixture holds: a plan-phase run with both
// review gates on and no active wave, so the gate facts are the only ones
// under test.
func planRoundsState(reviewPlan bool) *bundle.State {
	return &bundle.State{
		Schema: 1, Slug: "demo", Topic: "demo", Phase: bundle.PhasePlan,
		Branch: "takt/demo", Base: "main",
		Config: bundle.RunConfig{
			Autonomy: "auto", MaxParallel: 2, MaxRework: 1,
			Review: bundle.ReviewConfig{Spec: true, Plan: reviewPlan},
		},
	}
}

// planRoundsEvents is the log every case runs: the two gates' reviews and
// resets interleaved, so neither count can be read off the other gate's
// events, off the whole log, or by a reset whose gate went unread. The spec
// gate takes two reviews, a reset and one more review (1); the plan gate
// three reviews, a reset and two more (2).
func planRoundsEvents() []bundle.Event {
	reviewed := func(g string) bundle.Event {
		return bundle.Event{Type: gate.EvReviewed, Data: map[string]any{keyGate: g}}
	}
	reset := func(g string) bundle.Event {
		return bundle.Event{Type: gate.EvRoundsReset, Data: map[string]any{keyGate: g}}
	}
	return []bundle.Event{
		reviewed(gate.Spec), reviewed(gate.Plan), reviewed(gate.Spec), reset(gate.Spec),
		reviewed(gate.Plan), reviewed(gate.Plan), reset(gate.Plan),
		reviewed(gate.Spec), reviewed(gate.Plan), reviewed(gate.Plan),
	}
}

// planRoundsBundle writes fx to a fresh repository, appends the interleaved
// log to it, and returns what gatherFacts takes.
func planRoundsBundle(t *testing.T, fx planRoundsFixture) (*workspace, string, *bundle.State) {
	t.Helper()
	root := testutil.NewRepo(t)
	repo, err := gitx.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := bundle.ResolveDir(repo.Root, filepath.Join(root, ".home"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace{Repo: repo, Cfg: config.Defaults(), Dir: dir, Home: filepath.Join(root, ".home")}
	bdir := ws.Dir.Bundle("demo")
	writePlanRoundsFile(t, bdir, "spec.md", planRoundsSpec)
	writePlanRoundsFile(t, bdir, "goals.md", planRoundsGoals)
	writePlanRoundsFile(t, bdir, "plan.md", fx.planMD)
	if fx.index != "" {
		writePlanRoundsFile(t, bdir, "plan.index.json", fx.index)
	}
	st := planRoundsState(fx.reviewPlan)
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	for _, e := range planRoundsEvents() {
		if err = bundle.AppendEvent(bdir, e.Type, e.Data); err != nil {
			t.Fatal(err)
		}
	}
	return ws, bdir, st
}

// writePlanRoundsFile writes one bundle artifact, empty content included.
func writePlanRoundsFile(t *testing.T, bdir, name, content string) {
	t.Helper()
	if err := bundle.WriteFileAtomic(filepath.Join(bdir, name), []byte(content)); err != nil {
		t.Fatal(err)
	}
}

// planRoundsGuardCases are the runs the plan branch must not be entered for,
// each breaking one conjunct of `Review.Plan && HasIndex && IndexValid &&
// plan.md non-empty` and leaving the rest of the fixture — the events log
// above all — exactly as the counting test has it.
func planRoundsGuardCases() []planRoundsGuardCase {
	off, noIndex, badIndex, emptyPlan := guardedFixture(), guardedFixture(), guardedFixture(), guardedFixture()
	off.reviewPlan = false
	noIndex.index = ""
	// indexGarbage (deadline_facts_test.go) is this package's bytes
	// plan.ParseIndex refuses; the two uses want the same bytes.
	badIndex.index = indexGarbage
	emptyPlan.planMD = ""
	return []planRoundsGuardCase{
		// Spec §9: "does the cap fire when plan review is disabled? No."
		// Everything else about this bundle is the counting fixture, so the
		// only thing that can zero the count is the first conjunct.
		{"review.plan off", off, func(t *testing.T, f decide.Facts) {
			t.Helper()
			if !f.HasIndex || !f.IndexValid {
				t.Fatalf("only review.plan may differ here: index %v, valid %v, problems %v",
					f.HasIndex, f.IndexValid, f.IndexProblems)
			}
		}},
		{"index absent", noIndex, func(t *testing.T, f decide.Facts) {
			t.Helper()
			if f.HasIndex || f.IndexValid {
				t.Fatalf("a plan nobody wrote has no index: %v, valid %v", f.HasIndex, f.IndexValid)
			}
		}},
		{"index malformed", badIndex, func(t *testing.T, f decide.Facts) {
			t.Helper()
			if !f.HasIndex || f.IndexValid {
				t.Fatalf("an index nobody can parse is present and invalid: %v, valid %v",
					f.HasIndex, f.IndexValid)
			}
		}},
		// This case does not isolate the guard's final conjunct and does not
		// claim to: gatherIndexFacts appends "plan.md is missing or empty" to
		// IndexProblems, so an empty plan.md already makes IndexValid false.
		// The two conjuncts fail together and gatherFacts cannot separate
		// them. It is here as the reachable end-to-end state, and asserts
		// only that.
		{"plan.md empty", emptyPlan, func(t *testing.T, f decide.Facts) {
			t.Helper()
			if f.IndexValid {
				t.Fatal("an empty plan.md is an index problem, so the index must read invalid")
			}
		}},
	}
}

// TestGatherFactsCountsPlanRoundsPerGate is the fill itself, over a log both
// gates have been reviewed and reset in: the plan gate's count is its own
// reviews since its own reset, and the spec gate's is unchanged beside it.
func TestGatherFactsCountsPlanRoundsPerGate(t *testing.T) {
	t.Parallel()
	ws, bdir, st := planRoundsBundle(t, guardedFixture())
	f, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has to reach the plan branch at all, or the counts below
	// would be judging a branch that never ran — and the guard test's zeros
	// would have nothing to contrast with.
	if !f.HasIndex || !f.IndexValid {
		t.Fatalf("the fixture must reach the plan branch: index %v, valid %v, problems %v",
			f.HasIndex, f.IndexValid, f.IndexProblems)
	}
	if f.PlanRounds != wantPlanRounds {
		t.Errorf("plan rounds = %d, want %d: the plan gate's reviews since the plan gate's reset",
			f.PlanRounds, wantPlanRounds)
	}
	if f.SpecRounds != wantSpecRounds {
		t.Errorf("spec rounds = %d, want %d: the same log, the other gate", f.SpecRounds, wantSpecRounds)
	}
}

// TestGatherFactsLeavesPlanRoundsZeroOutsideTheGuard pins the fill's
// placement, which the counting test cannot: its fixture satisfies every
// conjunct, so an unconditional assignment would produce the same 2 and pass.
// Each case here runs the same events log through a bundle that fails one
// conjunct, and a fill moved out of the branch turns every one of them red.
// The spec count stays 1 throughout, so the zero is the guard's doing and not
// an empty log.
func TestGatherFactsLeavesPlanRoundsZeroOutsideTheGuard(t *testing.T) {
	t.Parallel()
	for _, tc := range planRoundsGuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, bdir, st := planRoundsBundle(t, tc.fixture)
			// An unreadable plan is row 8's business, so the gather itself
			// must still succeed for `takt next` to reach it.
			f, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
			if err != nil {
				t.Fatal(err)
			}
			tc.reason(t, f)
			if f.PlanRounds != 0 {
				t.Errorf("a run the plan branch is never entered for counts no plan rounds: %d", f.PlanRounds)
			}
			if f.SpecRounds != wantSpecRounds {
				t.Errorf("the log is the counting test's: spec rounds = %d, want %d",
					f.SpecRounds, wantSpecRounds)
			}
		})
	}
}
