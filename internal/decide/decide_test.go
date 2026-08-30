package decide_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/deadline"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/op"
)

var t0 = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

func state(phase string) *bundle.State {
	return &bundle.State{
		Schema: 1,
		Slug:   "demo",
		Topic:  "t",
		Phase:  phase,
		Branch: "takt/demo",
		Base:   "main",
		Config: bundle.RunConfig{
			Autonomy:    "auto",
			Review:      bundle.ReviewConfig{Spec: true, Plan: true, Tasks: true},
			Goals:       true,
			Alignment:   true,
			MaxParallel: 8,
			MaxRework:   1,
		},
		Gates: map[string]string{"spec": "pending", "plan": "pending"},
	}
}

// The deadline terms every fixture carries (spec A2.2): the shipped backend
// timeout, the shipped verify_timeout, and the work of the 8-task × 2-command
// wave the spec sizes — state()'s max_parallel is 8, so the wave is one
// review round.
const (
	factBackendTimeout = 15 * time.Minute
	factVerifyTimeout  = 10 * time.Minute
	factVerifyCommands = 16
	factReviewTasks    = 8
	factMaxParallel    = 8
)

func facts() decide.Facts {
	return decide.Facts{Now: t0, SessionID: "S", LockTTL: 10 * time.Minute, WaveStaleAfter: 30 * time.Minute,
		BackendTimeout: factBackendTimeout, VerifyTimeout: factVerifyTimeout,
		Wave: decide.WaveFacts{
			Recorded: map[int]bool{}, VerifyCommands: factVerifyCommands, ReviewTasks: factReviewTasks,
		}}
}

// closeCap is the cap the binary applies to itself for the close of the wave
// facts() describes — the same [deadline.Budget] internal/cli's closeBudget
// builds from the same run.
func closeCap() time.Duration {
	return deadline.Close(deadline.Budget{
		VerifyTimeout:  factVerifyTimeout,
		VerifyCommands: factVerifyCommands,
		BackendTimeout: factBackendTimeout,
		ReviewTasks:    factReviewTasks,
		MaxParallel:    factMaxParallel,
	})
}

// specGateFacts is a brainstorm run whose spec gate is the only thing left,
// so row 7 emits `exec takt review spec`.
func specGateFacts() decide.Facts {
	f := facts()
	f.HasSpec, f.HasGoals, f.GoalsFrozen = true, true, true
	return f
}

// planGateFacts is a plan run whose plan gate is the only thing left, so
// row 9 emits `exec takt review plan`.
func planGateFacts() decide.Facts {
	f := facts()
	f.HasIndex, f.IndexValid = true, true
	return f
}

// closeWaveState is the wave whose every task has reported, so row 15 emits
// `exec takt close-wave`.
func closeWaveState() *bundle.State {
	st := execState()
	st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
	return st
}

// TestExecTimeoutsStrictlyContainTheBinaryCaps is the decide half of spec
// A2.2 and goal G2: an `exec` op's timeout_s is no longer a fixed constant
// but [deadline.Session] of the very cap the binary will apply to the same
// work, computed from the same facts the binary computes its own from.
//
// Both halves of that are asserted, because either alone is satisfiable by
// an accident. The equality pins *which* cap each site wraps — a close op
// carrying GateReview's number would pass a bare inequality — and the strict
// inequality is the containment itself: the session must outlast the binary,
// so a command that hits its own deadline reports the timeout as a result
// instead of being killed mid-write. reviewTimeoutS (900s = exactly the new
// 15m backend timeout) and closeTimeoutS (1800s, under the 30m cap it wrapped)
// both failed that, which is why they are gone.
func TestExecTimeoutsStrictlyContainTheBinaryCaps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		st      *bundle.State
		f       decide.Facts
		command string
		inner   time.Duration
	}{
		{
			"spec review", state(bundle.PhaseBrainstorm), specGateFacts(),
			"takt review spec --slug demo", deadline.GateReview(factBackendTimeout),
		},
		{
			"plan review", state(bundle.PhasePlan), planGateFacts(),
			"takt review plan --slug demo", deadline.GateReview(factBackendTimeout),
		},
		{
			"close wave", closeWaveState(), func() decide.Facts {
				f := facts()
				f.Wave.Recorded = map[int]bool{1: true, 2: true}
				return f
			}(),
			"takt close-wave --slug demo", closeCap(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := mustDecide(t, c.st, c.f)
			if d.Action != decide.ActExec || d.Op.Command != c.command {
				t.Fatalf("%+v", d)
			}
			assertSessionContains(t, d.Op.TimeoutS, c.inner)
		})
	}
}

// assertSessionContains is the pair of claims every exec op above makes:
// its timeout_s is Session of the named cap, and it strictly exceeds that
// cap so the binary always outlives its own deadline.
func assertSessionContains(t *testing.T, timeoutS int, inner time.Duration) {
	t.Helper()
	if want := int(deadline.Session(inner).Seconds()); timeoutS != want {
		t.Fatalf("timeout_s = %d, want Session(%s) = %d", timeoutS, inner, want)
	}
	if capS := int(inner.Seconds()); timeoutS <= capS {
		t.Fatalf("timeout_s %d must strictly exceed the binary's own cap %s (%ds)", timeoutS, inner, capS)
	}
}

func execState() *bundle.State {
	st := state(bundle.PhaseExecute)
	st.Tasks = []bundle.Task{
		{ID: 1, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Class: "implement"},
		{ID: 2, Wave: 0, Status: bundle.StatusPending, Files: []string{"b.go"}, Class: "bounded"},
		{ID: 3, Wave: 1, Status: bundle.StatusPending, Files: []string{"c.go"}, Class: "implement"},
	}
	return st
}

func mustDecide(t *testing.T, st *bundle.State, f decide.Facts) decide.Decision {
	t.Helper()
	d, err := decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPendingGateIsRerendered(t *testing.T) {
	t.Parallel()
	st := state(bundle.PhaseBrainstorm)
	payload, _ := json.Marshal(op.Op{Op: op.Ask, Narration: "n", Gate: "gate_review", Question: "q"})
	st.PendingGate = &bundle.PendingGate{ID: "gate_review", OpenedAt: t0, Payload: payload}
	d := mustDecide(t, st, facts())
	if d.Action != decide.ActAsk || d.Op.Gate != "gate_review" || d.Op.Question != "q" {
		t.Fatalf("%+v", d)
	}
}

func TestBrainstormRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		f      func(*decide.Facts)
		action decide.Action
		check  func(*testing.T, decide.Decision)
	}{
		{"no spec → run brainstorm", func(*decide.Facts) {}, decide.ActRun, func(t *testing.T, d decide.Decision) {
			if d.Op.Step != "brainstorm" {
				t.Fatal(d.Op.Step)
			}
		}},
		{
			"spec, goals not frozen → run goals",
			func(f *decide.Facts) { f.HasSpec = true },
			decide.ActRun,
			func(t *testing.T, d decide.Decision) {
				if d.Op.Step != "goals" {
					t.Fatal(d.Op.Step)
				}
			},
		},
		{
			"goals frozen, spec gate pending → exec review spec",
			func(f *decide.Facts) { f.HasSpec, f.HasGoals, f.GoalsFrozen = true, true, true },
			decide.ActExec,
			func(t *testing.T, d decide.Decision) {
				if d.Op.Command != "takt review spec --slug demo" {
					t.Fatal(d.Op.Command)
				}
			},
		},
		{"spec gate rework → ask gate_review", func(f *decide.Facts) {
			f.HasSpec, f.HasGoals, f.GoalsFrozen = true, true, true
			f.SpecGate = decide.GateStatus{Verdict: "rework"}
		}, decide.ActAsk, func(t *testing.T, d decide.Decision) {
			if d.Op.Gate != "gate_review" || d.Op.Context["gate"] != "spec" {
				t.Fatalf("%+v", d.Op)
			}
		}},
		{"everything satisfied → transition plan", func(f *decide.Facts) {
			f.HasSpec, f.HasGoals, f.GoalsFrozen = true, true, true
			f.SpecGate = decide.GateStatus{Satisfied: true, Verdict: "approve"}
		}, decide.ActTransition, func(t *testing.T, d decide.Decision) {
			if d.Phase != bundle.PhasePlan {
				t.Fatal(d.Phase)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := facts()
			c.f(&f)
			d := mustDecide(t, state(bundle.PhaseBrainstorm), f)
			if d.Action != c.action {
				t.Fatalf("action = %s, want %s (%+v)", d.Action, c.action, d.Op)
			}
			c.check(t, d)
		})
	}
}

func TestBrainstormWithGoalsAndReviewOff(t *testing.T) {
	t.Parallel()
	st := state(bundle.PhaseBrainstorm)
	st.Config.Goals, st.Config.Review.Spec = false, false
	f := facts()
	f.HasSpec = true
	if d := mustDecide(t, st, f); d.Action != decide.ActTransition || d.Phase != bundle.PhasePlan {
		t.Fatalf("%+v", d)
	}
}

// complexity is the table's, not a function's.
//
//nolint:gocognit // one precedence table for spec §5.3's plan rows; the
func TestPlanRows(t *testing.T) {
	t.Parallel()
	base := func(f *decide.Facts) {
		f.HasSpec, f.HasGoals, f.GoalsFrozen = true, true, true
		f.SpecGate = decide.GateStatus{Satisfied: true}
	}
	cases := []struct {
		name   string
		f      func(*decide.Facts)
		action decide.Action
		check  func(*testing.T, decide.Decision)
	}{
		{
			"no index → dispatch planner",
			func(*decide.Facts) {},
			decide.ActDispatch,
			func(t *testing.T, d decide.Decision) {
				if d.Agent == nil || d.Agent.Agent != "planner" {
					t.Fatalf("%+v", d)
				}
			},
		},
		{"invalid index, 3 attempts → ask plan_invalid", func(f *decide.Facts) {
			f.HasIndex, f.IndexValid, f.PlanAttempts = true, false, 3
			f.IndexProblems = []string{"task 1 files: empty"}
		}, decide.ActAsk, func(t *testing.T, d decide.Decision) {
			if d.Op.Gate != "plan_invalid" {
				t.Fatal(d.Op.Gate)
			}
		}},
		{
			"valid index, plan gate pending → exec review plan",
			func(f *decide.Facts) { f.HasIndex, f.IndexValid = true, true },
			decide.ActExec,
			func(t *testing.T, d decide.Decision) {
				if d.Op.Command != "takt review plan --slug demo" {
					t.Fatal(d.Op.Command)
				}
			},
		},
		{"plan gate ok, no clauses → dispatch auditor clauses", func(f *decide.Facts) {
			f.HasIndex, f.IndexValid = true, true
			f.PlanGate = decide.GateStatus{Satisfied: true}
		}, decide.ActDispatch, func(t *testing.T, d decide.Decision) {
			if d.Agent.Agent != "alignment-auditor" || d.Agent.Mode != "clauses" {
				t.Fatalf("%+v", d.Agent)
			}
		}},
		{"clauses present, unconfirmed → ask alignment_confirm", func(f *decide.Facts) {
			f.HasIndex, f.IndexValid = true, true
			f.PlanGate = decide.GateStatus{Satisfied: true}
			f.Alignment.ClausesPresent = true
			f.Alignment.ClauseCount = 3
		}, decide.ActAsk, func(t *testing.T, d decide.Decision) {
			if d.Op.Gate != "alignment_confirm" {
				t.Fatal(d.Op.Gate)
			}
			// Review finding 4: the question interpolates the count, and the
			// op is persisted as the gate payload, so a missing count is a
			// durable "A1..A<nil>".
			if d.Op.Context["count"] != 3 {
				t.Fatalf("count = %v, want 3", d.Op.Context["count"])
			}
			if !strings.Contains(d.Op.Question, "A1..A3") {
				t.Fatalf("question = %q", d.Op.Question)
			}
		}},
		{"confirmed, no verdicts → dispatch auditor verdicts", func(f *decide.Facts) {
			f.HasIndex, f.IndexValid = true, true
			f.PlanGate = decide.GateStatus{Satisfied: true}
			f.Alignment = decide.AlignmentFacts{ClausesPresent: true, ClausesConfirmed: true}
		}, decide.ActDispatch, func(t *testing.T, d decide.Decision) {
			if d.Agent.Mode != "verdicts" {
				t.Fatal(d.Agent.Mode)
			}
		}},
		{"all satisfied → load plan", func(f *decide.Facts) {
			f.HasIndex, f.IndexValid = true, true
			f.PlanGate = decide.GateStatus{Satisfied: true}
			f.Alignment = decide.AlignmentFacts{ClausesPresent: true, ClausesConfirmed: true, VerdictsPresent: true}
		}, decide.ActLoadPlan, func(*testing.T, decide.Decision) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := facts()
			base(&f)
			c.f(&f)
			d := mustDecide(t, state(bundle.PhasePlan), f)
			if d.Action != c.action {
				t.Fatalf("action = %s, want %s (%+v)", d.Action, c.action, d.Op)
			}
			c.check(t, d)
		})
	}
}

// TestExecuteLaunchAndRecover, TestExecuteCloseWave and TestExecuteCompletion
// are one logical table (the §5.3 execute-phase rows) split into three
// top-level funcs so no single func's cognitive complexity trips gocognit;
// the subtests themselves are unchanged.

func TestExecuteLaunchAndRecover(t *testing.T) {
	t.Parallel()
	t.Run("no active wave, pending tasks → launch lowest wave", func(t *testing.T) {
		t.Parallel()
		d := mustDecide(t, execState(), facts())
		if d.Action != decide.ActLaunch || d.Wave != 0 || len(d.Tasks) != 2 || d.Attempt != 1 {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("active wave, unrecorded, same session, fresh → stop wave_in_flight", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{
			N:         0,
			Attempt:   1,
			StartedAt: t0.Add(-time.Minute),
			SessionID: "S",
			Tasks:     []int{1, 2},
		}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true}
		d := mustDecide(t, st, f)
		if d.Action != decide.ActStop || d.Op.Reason != "wave_in_flight" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("active wave, unrecorded, other session → recover those tasks", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{
			N:         0,
			Attempt:   1,
			StartedAt: t0.Add(-time.Minute),
			SessionID: "OTHER",
			Tasks:     []int{1, 2},
		}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true}
		d := mustDecide(t, st, f)
		if d.Action != decide.ActRecover || len(d.Tasks) != 1 || d.Tasks[0] != 2 || d.Attempt != 2 {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("active wave, unrecorded, stale → recover", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{
			N:         0,
			Attempt:   1,
			StartedAt: t0.Add(-time.Hour),
			SessionID: "S",
			Tasks:     []int{1, 2},
		}
		if d := mustDecide(t, st, facts()); d.Action != decide.ActRecover {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("--recover forces recovery", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
		f := facts()
		f.Recover = true
		if d := mustDecide(t, st, f); d.Action != decide.ActRecover {
			t.Fatalf("%+v", d)
		}
	})
}

func TestExecuteCloseWave(t *testing.T) {
	t.Parallel()
	t.Run("all recorded, no close → exec close-wave", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true, 2: true}
		d := mustDecide(t, st, f)
		if d.Action != decide.ActExec || d.Op.Command != "takt close-wave --slug demo" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("close committed → clear wave", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true, 2: true}
		f.Wave.Close = &decide.CloseFacts{Committed: true}
		if d := mustDecide(t, st, f); d.Action != decide.ActClearWave {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("close rework under max_rework → launch rework tasks attempt+1", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.Tasks[0].Attempt = 1
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true, 2: true}
		f.Wave.Close = &decide.CloseFacts{Rework: []int{1}}
		d := mustDecide(t, st, f)
		if d.Action != decide.ActLaunch || d.Wave != 0 || len(d.Tasks) != 1 || d.Tasks[0] != 1 || d.Attempt != 2 {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("close rework exhausted → ask wave_failures", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.Tasks[0].Attempt = 2 // already retried once with max_rework 1
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 2, StartedAt: t0, SessionID: "S", Tasks: []int{1}}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true}
		f.Wave.Close = &decide.CloseFacts{Rework: []int{1}}
		d := mustDecide(t, st, f)
		if d.Action != decide.ActAsk || d.Op.Gate != "wave_failures" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("close review error → ask review_error", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true, 2: true}
		f.Wave.Close = &decide.CloseFacts{ReviewErrors: []int{2}}
		if d := mustDecide(t, st, f); d.Action != decide.ActAsk || d.Op.Gate != "review_error" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("close failed → ask wave_failures", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true, 2: true}
		f.Wave.Close = &decide.CloseFacts{Failed: []int{2}}
		if d := mustDecide(t, st, f); d.Action != decide.ActAsk || d.Op.Gate != "wave_failures" {
			t.Fatalf("%+v", d)
		}
	})
}

func TestExecuteCompletion(t *testing.T) {
	t.Parallel()
	t.Run("no active wave, blocked task left → ask wave_failures", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.Tasks[0].Status, st.Tasks[1].Status, st.Tasks[2].Status = bundle.StatusDone, bundle.StatusBlocked, bundle.StatusDone
		if d := mustDecide(t, st, facts()); d.Action != decide.ActAsk || d.Op.Gate != "wave_failures" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("all done/waived → transition finish", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.Tasks[0].Status, st.Tasks[1].Status, st.Tasks[2].Status = bundle.StatusDone, bundle.StatusWaived, bundle.StatusDone
		if d := mustDecide(t, st, facts()); d.Action != decide.ActTransition || d.Phase != bundle.PhaseFinish {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("execute with zero tasks is an error", func(t *testing.T) {
		t.Parallel()
		if _, err := decide.Decide(state(bundle.PhaseExecute), facts()); err == nil {
			t.Fatal("expected error")
		}
	})
}

// internalWaveFixture is the shared fixture task 8's internal-review tests
// build on: phase execute, one active wave (N 0, slice 1, attempt 1) whose
// two tasks are both recorded, with no close record yet — the window
// decideInternal runs in, between the unrecorded-tasks check and the
// `Close == nil` exec (two-layers design §3.2, §3.3).
func internalWaveFixture() (*bundle.State, decide.Facts) {
	st := execState()
	st.ActiveWave = &bundle.ActiveWave{N: 0, Slice: 1, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
	f := facts()
	f.Wave.Recorded = map[int]bool{1: true, 2: true}
	return st, f
}

// TestDecideDispatchesUnrecordedLenses covers row 15a (two-layers design
// §3.2): with one lens still unrecorded for this attempt, Decide dispatches
// exactly the lenses that still owe a record — not the ones already in.
func TestDecideDispatchesUnrecordedLenses(t *testing.T) {
	t.Parallel()
	st, f := internalWaveFixture()
	f.Wave.Internal = decide.InternalFacts{
		Lenses: []string{"correctness", "intent"}, Recorded: map[string]bool{"correctness": true},
		HasDoneDigest: true,
	}
	d := mustDecide(t, st, f)
	if d.Action != decide.ActDispatchLenses || d.Wave != 0 || d.Attempt != 1 {
		t.Fatalf("%+v", d)
	}
	if len(d.Lenses) != 1 || d.Lenses[0] != "intent" {
		t.Fatalf("lenses = %v, want [intent]", d.Lenses)
	}
}

// TestDecideDispatchesTheVerifierWhenCandidatesExist covers row 15b
// (two-layers design §3.3): every lens recorded, candidates merged and
// unverified → dispatch the reviewer in verify mode.
func TestDecideDispatchesTheVerifierWhenCandidatesExist(t *testing.T) {
	t.Parallel()
	st, f := internalWaveFixture()
	f.Wave.Internal = decide.InternalFacts{
		Lenses:        []string{"correctness", "intent"},
		Recorded:      map[string]bool{"correctness": true, "intent": true},
		Candidates:    3,
		HasDoneDigest: true,
	}
	d := mustDecide(t, st, f)
	if d.Action != decide.ActDispatch || d.Agent == nil {
		t.Fatalf("%+v", d)
	}
	if d.Agent.Agent != op.AgentReviewer || d.Agent.Mode != "verify" {
		t.Fatalf("agent %+v", d.Agent)
	}
}

// TestDecideSkipsStraightToCloseWhenInternalDone covers every shape of
// InternalFacts.Done (two-layers design §3.4): whichever way the internal
// review reads as complete, row 15's exec close-wave runs exactly as it did
// before the internal layer existed.
func TestDecideSkipsStraightToCloseWhenInternalDone(t *testing.T) {
	t.Parallel()
	lenses := []string{"correctness", "intent"}
	bothRecorded := map[string]bool{"correctness": true, "intent": true}
	cases := []struct {
		name     string
		internal decide.InternalFacts
	}{
		{"lenses empty", decide.InternalFacts{HasDoneDigest: true}},
		{"no done digest", decide.InternalFacts{Lenses: lenses, Recorded: map[string]bool{}}},
		{"recorded, zero candidates", decide.InternalFacts{
			Lenses: lenses, Recorded: bothRecorded, Candidates: 0, HasDoneDigest: true,
		}},
		{"recorded, candidates verified", decide.InternalFacts{
			Lenses: lenses, Recorded: bothRecorded, Candidates: 2, VerifyRecorded: true, HasDoneDigest: true,
		}},
		{"skipped", decide.InternalFacts{
			Lenses: lenses, Recorded: map[string]bool{}, HasDoneDigest: true, Skipped: true,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st, f := internalWaveFixture()
			f.Wave.Internal = c.internal
			d := mustDecide(t, st, f)
			if d.Action != decide.ActExec || d.Op.Command != "takt close-wave --slug demo" {
				t.Fatalf("%+v", d)
			}
		})
	}
}

// TestDecideAsksAgentInvalidAtTheReviewerCap covers the cap shared with the
// auditor's (two-layers design D14): three unusable reviewer replies asks
// agent_invalid instead of dispatching a fourth time, and — because the
// internal layer is advisory — the question offers a skip.
func TestDecideAsksAgentInvalidAtTheReviewerCap(t *testing.T) {
	t.Parallel()
	st, f := internalWaveFixture()
	f.Wave.Internal = decide.InternalFacts{
		Lenses: []string{"correctness"}, Recorded: map[string]bool{}, HasDoneDigest: true,
	}
	f.ReviewerAttempts = 3
	f.ReviewerProblems = []string{"no fenced json block"}
	d := mustDecide(t, st, f)
	if d.Action != decide.ActAsk || d.Op.Gate != "agent_invalid" {
		t.Fatalf("%+v", d)
	}
	if d.Op.Context["agent"] != op.AgentReviewer {
		t.Fatalf("context = %+v", d.Op.Context)
	}
	found := false
	for _, o := range d.Op.Options {
		if o.Choice == "skip" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the reviewer's agent_invalid question must offer skip: %+v", d.Op.Options)
	}
}

// TestDecideInternalNeverRunsWithTasksUnrecorded pins the load-bearing order
// in decideActiveWave: decideInternal is only ever consulted once every task
// of the wave has recorded, even when the run has lenses configured and
// nothing recorded for them yet.
func TestDecideInternalNeverRunsWithTasksUnrecorded(t *testing.T) {
	t.Parallel()
	st, f := internalWaveFixture()
	f.Wave.Recorded = map[int]bool{1: true} // task 2 unrecorded
	f.Wave.Internal = decide.InternalFacts{
		Lenses: []string{"correctness", "intent"}, Recorded: map[string]bool{},
	}
	d := mustDecide(t, st, f)
	if d.Action != decide.ActStop || d.Op.Reason != "wave_in_flight" {
		t.Fatalf("%+v", d)
	}
}

func TestFinishAndArchivedStop(t *testing.T) {
	t.Parallel()
	if d := mustDecide(
		t,
		state(bundle.PhaseFinish),
		facts(),
	); d.Action != decide.ActExec ||
		!strings.Contains(d.Op.Command, "takt verify") {
		t.Fatalf("%+v", d)
	}
	if d := mustDecide(
		t,
		state(bundle.PhaseArchived),
		facts(),
	); d.Action != decide.ActStop ||
		d.Op.Reason != "archived" {
		t.Fatalf("%+v", d)
	}
}

func TestQuestionShapes(t *testing.T) {
	t.Parallel()
	for _, g := range []string{"owner", "gate_review", "alignment_confirm", "plan_invalid", "agent_invalid",
		"wave_failures", "review_error"} {
		q := decide.Question(g, map[string]any{"gate": "spec", "slug": "demo", "agent": "alignment-auditor"})
		if q.Op != op.Ask || q.Gate != g || len(q.Options) < 2 || q.Answer == "" || q.Question == "" {
			t.Errorf("%s: %+v", g, q)
		}
	}
}

// TestWaveFailuresContextShape covers the JSON-shape half of review M10: the
// gate's context is persisted as the pending gate's payload and re-rendered
// from it verbatim (spec §4.3), so every id list has to be a list — `null`
// in a question the user reads is durable noise — blocked ids belong under
// `blocked` rather than lumped in with the failed ones, and the rework tasks
// that ride along with a failure have to be named too.
func TestWaveFailuresContextShape(t *testing.T) {
	t.Parallel()
	t.Run("no active wave: blocked ids are their own list", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.Tasks[0].Status = bundle.StatusFailed
		st.Tasks[1].Status = bundle.StatusBlocked
		st.Tasks[2].Status = bundle.StatusDone
		d := mustDecide(t, st, facts())
		if d.Action != decide.ActAsk || d.Op.Gate != "wave_failures" {
			t.Fatalf("%+v", d)
		}
		assertIDs(t, d.Op.Context, "failed", []int{1})
		assertIDs(t, d.Op.Context, "blocked", []int{2})
		assertIDs(t, d.Op.Context, "exhausted", nil)
		assertIDs(t, d.Op.Context, "rework", nil)
	})
	t.Run("active wave: a failure names the rework tasks riding along", func(t *testing.T) {
		t.Parallel()
		st := execState()
		st.Tasks[1].Attempt = 1 // one rework left at max_rework 1
		st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
		f := facts()
		f.Wave.Recorded = map[int]bool{1: true, 2: true}
		f.Wave.Close = &decide.CloseFacts{Failed: []int{1}, Rework: []int{2}}
		d := mustDecide(t, st, f)
		if d.Action != decide.ActAsk || d.Op.Gate != "wave_failures" {
			t.Fatalf("%+v", d)
		}
		assertIDs(t, d.Op.Context, "failed", []int{1})
		assertIDs(t, d.Op.Context, "blocked", nil)
		assertIDs(t, d.Op.Context, "exhausted", nil)
		assertIDs(t, d.Op.Context, "rework", []int{2})
		if !strings.Contains(d.Op.Question, "rework pending [2]") {
			t.Fatalf("the question must name the rework tasks: %q", d.Op.Question)
		}
	})
}

func TestVocabRunStepsAreTheOpSteps(t *testing.T) {
	t.Parallel()
	if got, want := decide.Vocab().RunSteps, op.Steps(); !slices.Equal(got, want) {
		t.Fatalf("Vocab().RunSteps = %v, want op.Steps() = %v", got, want)
	}
}

// assertIDs checks one id list of a gate context: present, a list (never
// null once marshalled), and holding exactly want.
func assertIDs(t *testing.T, ctx map[string]any, key string, want []int) {
	t.Helper()
	got, ok := ctx[key].([]int)
	if !ok {
		t.Fatalf("context[%q] = %#v, want an []int", key, ctx[key])
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "null" {
		t.Fatalf("context[%q] marshals to null; a gate's id lists are always lists", key)
	}
	if len(got) != len(want) {
		t.Fatalf("context[%q] = %v, want %v", key, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("context[%q] = %v, want %v", key, got, want)
		}
	}
}

// planFacts is the facts a plan-phase run has when the alignment audit is
// the only thing left to do: a valid index, the plan gate satisfied, the
// audit in state a, and attempts unusable auditor replies since the last
// reset.
func planFacts(a decide.AlignmentFacts, attempts int) decide.Facts {
	f := facts()
	f.HasIndex, f.IndexValid = true, true
	f.PlanGate = decide.GateStatus{Satisfied: true}
	f.Alignment = a
	f.AlignmentAttempts = attempts
	f.AlignmentProblems = []string{"no fenced json block"}
	return f
}

// finishStateNeedingTheAssessor is row 21's shape: verified at HEAD, goals
// on, goals not yet checked — the state that dispatches the goal assessor.
func finishStateNeedingTheAssessor() (*bundle.State, decide.Facts) {
	return finishState(), decide.Facts{Finish: decide.FinishFacts{Verified: true}}
}

// choices joins a question's choices, so a test can name the whole option
// list in one comparison.
func choices(q op.Op) string {
	out := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		out = append(out, o.Choice)
	}
	return strings.Join(out, ",")
}

// capRow is one row of the agent_invalid cap table: what Decide must do,
// for which agent, and — when it dispatches — in which mode.
type capRow struct {
	name         string
	facts        decide.Facts
	wantOp       string // "" asserts only that the row does not raise the gate
	wantGate     string
	wantAgent    string
	wantMode     string
	wantAttempts int
}

// TestAgentInvalidGateCapsTheAuditor covers spec §5.3 rows 10 and 11: three
// replies takt could not parse and the run asks instead of handing the same
// brief out a fourth time.
func TestAgentInvalidGateCapsTheAuditor(t *testing.T) {
	t.Parallel()
	planSt := state(bundle.PhasePlan)
	confirmed := decide.AlignmentFacts{ClausesPresent: true, ClausesConfirmed: true, ClauseCount: 2}
	audited := decide.AlignmentFacts{
		ClausesPresent: true, ClausesConfirmed: true, VerdictsPresent: true, ClauseCount: 2,
	}
	const auditor = "alignment-auditor"
	for _, c := range []capRow{
		{"clauses, two rejections: dispatch", planFacts(decide.AlignmentFacts{}, 2),
			"dispatch", "", auditor, "clauses", 0},
		{"clauses, three rejections: ask", planFacts(decide.AlignmentFacts{}, 3),
			"ask", "agent_invalid", auditor, "", 3},
		{"verdicts, two rejections: dispatch", planFacts(confirmed, 2),
			"dispatch", "", auditor, "verdicts", 0},
		{"verdicts, four rejections: ask", planFacts(confirmed, 4),
			"ask", "agent_invalid", auditor, "", 4},
		{"verdicts present: no ask despite rejections", planFacts(audited, 3), "", "", "", "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assertCapRow(t, mustDecide(t, planSt, c.facts), c)
		})
	}
}

// assertCapRow checks one row of the cap table.
func assertCapRow(t *testing.T, d decide.Decision, c capRow) {
	t.Helper()
	switch c.wantOp {
	case "":
		if d.Action == decide.ActAsk && d.Op.Gate == "agent_invalid" {
			t.Fatalf("must not ask: %+v", d)
		}
	case "ask":
		if d.Action != decide.ActAsk || d.Op.Gate != c.wantGate {
			t.Fatalf("%+v", d)
		}
		if d.Op.Context["agent"] != c.wantAgent || d.Op.Context["attempts"] != c.wantAttempts {
			t.Fatalf("context %+v", d.Op.Context)
		}
	case "dispatch":
		if d.Action != decide.ActDispatch || d.Agent == nil {
			t.Fatalf("%+v", d)
		}
		if d.Agent.Agent != c.wantAgent || d.Agent.Mode != c.wantMode {
			t.Fatalf("agent %+v", d.Agent)
		}
	}
}

// TestAgentInvalidGateCapsTheAssessor is the same cap on row 21.
func TestAgentInvalidGateCapsTheAssessor(t *testing.T) {
	t.Parallel()
	st, f := finishStateNeedingTheAssessor()
	f.GoalsAttempts, f.GoalsProblems = 3, []string{"no fenced json block"}
	d := mustDecide(t, st, f)
	if d.Action != decide.ActAsk || d.Op.Gate != "agent_invalid" || d.Op.Context["agent"] != "goal-assessor" {
		t.Fatalf("%+v", d)
	}
	f.GoalsAttempts = 2
	if d = mustDecide(t, st, f); d.Action != decide.ActDispatch || d.Agent == nil || d.Agent.Agent != "goal-assessor" {
		t.Fatalf("%+v", d)
	}
}

// TestAgentInvalidQuestionOffersSkipOnlyForTheAuditor: the alignment digest
// is advisory and can be skipped; the goal check cannot — a run that must
// not check its goals is initialised with --no-goals.
func TestAgentInvalidQuestionOffersSkipOnlyForTheAuditor(t *testing.T) {
	t.Parallel()
	q := decide.Question(
		"agent_invalid",
		map[string]any{"slug": "demo", "agent": "alignment-auditor", "attempts": 3, "problems": []string{"x"}},
	)
	if choices(q) != "retry,skip,stop" {
		t.Fatalf("auditor choices: %s", choices(q))
	}
	q = decide.Question(
		"agent_invalid",
		map[string]any{"slug": "demo", "agent": "goal-assessor", "attempts": 3, "problems": []string{"x"}},
	)
	if choices(q) != "retry,stop" {
		t.Fatalf("assessor choices: %s", choices(q))
	}
}

// TestGateReviewTellsTheUserWhatReviseWillActuallyDo: the revise option's
// text has to match what revising does, not what it did before the
// fixed-point design. Only a non-blocking *rework* on the spec gate gets the
// new wording — every other row a reviewer answered keeps promising the
// re-review it still performs, because acceptRevision writes the closing
// event for none of them. reject is the one the severity alone cannot tell
// apart from that rework: it can find nothing blocking and still take the
// old loop. The error row is not here at all — it offers no revise, and
// TestQuestionGateReviewOnAnErrorOffersRetryNotRevise covers it.
func TestGateReviewTellsTheUserWhatReviseWillActuallyDo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		gate     string
		verdict  string
		blocking bool
		want     string
	}{
		{"spec rework, nothing blocking", "spec", "rework", false, "closes on the edit"},
		{"spec rework, blocking", "spec", "rework", true, "re-arms"},
		{"spec reject, nothing blocking", "spec", "reject", false, "re-arms"},
		{"plan is unchanged", "plan", "rework", false, "re-arms"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			q := decide.Question("gate_review", map[string]any{
				"slug": "demo", "gate": c.gate, "verdict": c.verdict,
				"summary": "see reviews/" + c.gate + ".md", "blocking": c.blocking,
			})
			var revise string
			for _, o := range q.Options {
				if o.Choice == "revise" {
					revise = o.Description
				}
			}
			if revise == "" {
				t.Fatal("gate_review must always offer revise")
			}
			if !strings.Contains(revise, c.want) {
				t.Fatalf("revise says %q, want it to mention %q", revise, c.want)
			}
		})
	}
}

// TestQuestionGateReviewOnAnErrorOffersRetryNotRevise: an error verdict is a
// backend failure, not a review. reviews/spec.md was left alone and still
// describes the previous pass, so there are no new findings to revise
// against; the question says what went wrong and offers the one action that
// can produce a verdict (#43.2).
func TestQuestionGateReviewOnAnErrorOffersRetryNotRevise(t *testing.T) {
	t.Parallel()
	q := decide.Question("gate_review", map[string]any{
		"slug": "demo", "gate": "spec", "verdict": "error", "blocking": false,
		"summary": "see reviews/spec.md", "reason": "backend fell over",
	})
	for _, want := range []string{
		"errored: backend fell over",
		"reviews/spec.md still describes the previous pass",
	} {
		if !strings.Contains(q.Question, want) {
			t.Fatalf("question = %q, want it to mention %q", q.Question, want)
		}
	}
	var choices []string
	for _, o := range q.Options {
		choices = append(choices, o.Choice)
		if o.Choice == "revise" {
			t.Fatalf("an errored pass has nothing to revise against: %+v", q.Options)
		}
	}
	if !slices.Equal(choices, []string{"retry", "accept", "stop"}) {
		t.Fatalf("options = %v, want retry, accept, stop in that order", choices)
	}
	if !strings.HasSuffix(q.Options[0].Label, "(Recommended)") {
		t.Fatalf("the recommendation must be the first option: %+v", q.Options[0])
	}
	for _, o := range q.Options[1:] {
		if strings.Contains(o.Label, "(Recommended)") {
			t.Fatalf("exactly one option may be recommended: %+v", q.Options)
		}
	}
	if !strings.Contains(q.Options[0].Description, "takt review spec --slug demo") {
		t.Fatalf("retry must name the command that re-runs the review: %q", q.Options[0].Description)
	}
	// A receipt written before the reason field existed carries none, and
	// the question still has to read as an account of what happened.
	q = decide.Question("gate_review", map[string]any{
		"slug": "demo", "gate": "spec", "verdict": "error", "summary": "see reviews/spec.md",
	})
	if !strings.Contains(q.Question, "(no reason recorded)") {
		t.Fatalf("a reasonless error must still say so: %q", q.Question)
	}
}

// TestTheGateReviewAskCarriesTheReceiptsReason: the reason travels from the
// receipt (gatherGateFacts copies it into GateStatus) through the ask
// context to the question. Without this hop the error row would render
// "(no reason recorded)" for every failure the backend did explain.
func TestTheGateReviewAskCarriesTheReceiptsReason(t *testing.T) {
	t.Parallel()
	t.Run("spec", func(t *testing.T) {
		t.Parallel()
		f := facts()
		f.HasSpec, f.HasGoals, f.GoalsFrozen = true, true, true
		f.SpecGate = decide.GateStatus{Verdict: "error", Reason: "x"}
		d := mustDecide(t, state(bundle.PhaseBrainstorm), f)
		if d.Action != decide.ActAsk || d.Op.Gate != "gate_review" {
			t.Fatalf("%+v", d)
		}
		if d.Op.Context["reason"] != "x" {
			t.Fatalf("the question must carry the receipt's reason: %+v", d.Op.Context)
		}
	})
	t.Run("plan", func(t *testing.T) {
		t.Parallel()
		f := facts()
		f.HasSpec, f.HasGoals, f.GoalsFrozen = true, true, true
		f.SpecGate = decide.GateStatus{Satisfied: true}
		f.HasIndex, f.IndexValid = true, true
		f.PlanGate = decide.GateStatus{Verdict: "error", Reason: "x"}
		d := mustDecide(t, state(bundle.PhasePlan), f)
		if d.Action != decide.ActAsk || d.Op.Gate != "gate_review" {
			t.Fatalf("%+v", d)
		}
		if d.Op.Context["reason"] != "x" {
			t.Fatalf("the question must carry the receipt's reason: %+v", d.Op.Context)
		}
	})
}

// TestBrainstormPassesBlockingToTheGateReviewQuestion: decideBrainstorm must
// forward GateStatus.Blocking into the gate_review ask context, or the
// question has no way to tell a fixed-point revise from a re-review one.
func TestBrainstormPassesBlockingToTheGateReviewQuestion(t *testing.T) {
	t.Parallel()
	st := state(bundle.PhaseBrainstorm)
	f := decide.Facts{HasSpec: true, HasGoals: true, GoalsFrozen: true}
	f.SpecGate = decide.GateStatus{Verdict: "rework", Blocking: true}
	d, err := decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActAsk || d.Op.Gate != "gate_review" {
		t.Fatalf("%+v", d)
	}
	if d.Op.Context["blocking"] != true {
		t.Fatalf("the question must carry blocking: %+v", d.Op.Context)
	}
}

func TestSpecReviewRoundsAreCapped(t *testing.T) {
	t.Parallel()
	base := func() (*bundle.State, decide.Facts) {
		return state(bundle.PhaseBrainstorm),
			decide.Facts{HasSpec: true, HasGoals: true, GoalsFrozen: true}
	}
	st, f := base()
	f.SpecRounds = 2
	d, err := decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActExec {
		t.Fatalf("under the cap the run must still review: %+v", d)
	}

	st, f = base()
	f.SpecRounds = 3
	d, err = decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActAsk || d.Op.Gate != "gate_review_capped" {
		t.Fatalf("at the cap the run must ask instead of reviewing a fourth time: %+v", d)
	}
	if d.Op.Context["attempts"] != 3 || d.Op.Context["gate"] != "spec" {
		t.Fatalf("the question must name the gate and the round count: %+v", d.Op.Context)
	}
	var choices []string
	for _, o := range d.Op.Options {
		choices = append(choices, o.Choice)
	}
	if len(choices) != 3 {
		t.Fatalf("choices = %v, want accept/retry/stop", choices)
	}
}

// TestPendingReworkVerdictOutranksTheRoundCap pins the load-bearing order in
// decideBrainstorm: a rework verdict waiting to be answered must win even
// when the round count has also reached the cap. Immediately after a third
// consecutive rework verdict with no intervening edit, both conditions are
// true at once — needsRework(f.SpecGate) and f.SpecRounds >= maxAgentAttempts
// — and the user must still be shown gate_review (there is a verdict to
// answer), never gate_review_capped. If the two checks in decideBrainstorm
// were ever swapped, this test would fail where
// TestSpecReviewRoundsAreCapped could not: that test never sets
// f.SpecGate.Verdict, so needsRework is false throughout and it cannot tell
// the checks apart.
func TestPendingReworkVerdictOutranksTheRoundCap(t *testing.T) {
	t.Parallel()
	st := state(bundle.PhaseBrainstorm)
	f := decide.Facts{HasSpec: true, HasGoals: true, GoalsFrozen: true}
	f.SpecGate = decide.GateStatus{Satisfied: false, Verdict: "rework"}
	f.SpecRounds = 3
	d, err := decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActAsk || d.Op.Gate != "gate_review" {
		t.Fatalf("a verdict waiting to be answered must outrank the round cap: %+v", d)
	}
}

func TestCappedGateIsInTheVocabulary(t *testing.T) {
	t.Parallel()
	if !slices.Contains(decide.Vocab().Gates, "gate_review_capped") {
		t.Fatal("every gate Decide can emit must be in Vocab so the prompt parity tests see it")
	}
}

// altBackendTimeout is a deadline no default holds, so a rendering that
// quoted a constant instead of the fact it was given shows up as a mismatch.
const altBackendTimeout = 20 * time.Minute

// reviewErrorDecision is row 16 with the reviewer chain the caller names: a
// wave whose every task reported and whose close recorded a review error, so
// Decide asks the review_error gate.
func reviewErrorDecision(t *testing.T, chain []decide.ReviewerBackend) decide.Decision {
	t.Helper()
	st := execState()
	st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: t0, SessionID: "S", Tasks: []int{1, 2}}
	f := facts()
	f.Wave.Recorded = map[int]bool{1: true, 2: true}
	f.Wave.Close = &decide.CloseFacts{ReviewErrors: []int{2}}
	f.ReviewerBackends = chain
	d := mustDecide(t, st, f)
	if d.Action != decide.ActAsk || d.Op.Gate != "review_error" {
		t.Fatalf("a close that recorded a review error asks review_error: %+v", d)
	}
	return d
}

// retryOption is the one option of a rendered gate whose text this spec
// section grows.
func retryOption(t *testing.T, o *op.Op) op.Option {
	t.Helper()
	for _, opt := range o.Options {
		if opt.Choice == "retry" {
			return opt
		}
	}
	t.Fatalf("the gate must offer retry: %+v", o.Options)
	return op.Option{}
}

// TestReviewErrorNamesTheBackendTimeouts is spec A3 and goal G5: a user who
// hits this gate should not have to read the source to learn what the review
// deadline was or which key raises it. The three shapes are the three the
// chain can actually take once gatherFacts has skipped the entries config has
// no key for — several backends, one, and none at all — and the last is the
// one that must degrade to the literal key rather than invent a deadline.
//
// Everything else about the gate is asserted unchanged in every row: the
// narration, the question and the option set are what they were, because this
// section grows one description and nothing else.
func TestReviewErrorNamesTheBackendTimeouts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		chain    []decide.ReviewerBackend
		contains []string
		absent   []string
	}{
		{
			name: "every configured backend that has a key",
			chain: []decide.ReviewerBackend{
				{Name: "copilot", Timeout: factBackendTimeout},
				{Name: "claude", Timeout: factBackendTimeout},
			},
			contains: []string{"backends.copilot.timeout", "backends.claude.timeout", "15m", ".takt.json"},
			absent:   []string{"backends.<name>.timeout"},
		},
		{
			name:     "one entry, the rest skipped for having no key",
			chain:    []decide.ReviewerBackend{{Name: "claude", Timeout: altBackendTimeout}},
			contains: []string{"backends.claude.timeout", "20m", ".takt.json"},
			absent:   []string{"backends.copilot.timeout", "backends.<name>.timeout", "fake"},
		},
		{
			name:     "no backend with a key at all",
			chain:    nil,
			contains: []string{"backends.<name>.timeout", ".takt.json"},
			absent:   []string{"(now ", "m0s", "backends.claude.timeout", "backends.copilot.timeout"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := reviewErrorDecision(t, c.chain)
			desc := retryOption(t, d.Op).Description
			if !strings.HasPrefix(desc, "Re-run `takt close-wave`.") {
				t.Errorf("the retry option still says what retrying does: %q", desc)
			}
			assertHolds(t, desc, c.contains, c.absent)
			assertReviewErrorUnchanged(t, d.Op)
		})
	}
}

// assertHolds checks one rendered string against what it must name and what
// it must not.
func assertHolds(t *testing.T, got string, contains, absent []string) {
	t.Helper()
	for _, want := range contains {
		if !strings.Contains(got, want) {
			t.Errorf("retry description %q must name %q", got, want)
		}
	}
	for _, unwanted := range absent {
		if strings.Contains(got, unwanted) {
			t.Errorf("retry description %q must not hold %q", got, unwanted)
		}
	}
}

// assertReviewErrorUnchanged checks everything about the gate spec A3 leaves
// alone: only the retry option's description grows.
func assertReviewErrorUnchanged(t *testing.T, o *op.Op) {
	t.Helper()
	if o.Narration != "a task review errored" {
		t.Errorf("narration is unchanged: %q", o.Narration)
	}
	if !strings.Contains(o.Question, "The reviewer failed for task(s) [2]") {
		t.Errorf("the question is unchanged: %q", o.Question)
	}
	if got := choices(*o); got != "retry,skip,stop" {
		t.Errorf("the option set is unchanged: %v", got)
	}
	if o.Answer != "takt answer --gate review_error --choice <choice> --slug demo" {
		t.Errorf("the answer command is unchanged: %q", o.Answer)
	}
}

// TestReviewErrorRendersIdenticallyAfterAContextRoundTrip is the persistence
// half of the same section, stated as what actually happens rather than as a
// claim about a re-render path: `takt next` persists the rendered op as the
// gate payload and re-emits those stored bytes verbatim, so Question runs
// exactly once per gate — on the in-memory context. What has to survive is
// therefore the *shape*: the entries are built as []any of map[string]any,
// which is what decoding produces, so a context that has been through JSON
// renders the same option text as the one that never left memory. A []any of
// a concrete struct or map type would render once and then quietly stop
// naming anything.
func TestReviewErrorRendersIdenticallyAfterAContextRoundTrip(t *testing.T) {
	t.Parallel()
	d := reviewErrorDecision(t, []decide.ReviewerBackend{
		{Name: "copilot", Timeout: factBackendTimeout},
		{Name: "claude", Timeout: altBackendTimeout},
	})
	raw, err := json.Marshal(d.Op.Context)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	again := decide.Question("review_error", decoded)
	if !slices.Equal(d.Op.Options, again.Options) {
		t.Fatalf("a decoded context must render the same options:\nfirst  %+v\nsecond %+v",
			d.Op.Options, again.Options)
	}
	if !strings.Contains(retryOption(t, &again).Description, "backends.copilot.timeout") {
		t.Fatalf("the decoded rendering must still name the keys: %q", retryOption(t, &again).Description)
	}
}

// The two shapes the retry option's description takes, spelled out once so
// the malformed rows below can assert the whole rendering rather than a
// substring: the "named but no deadline" form is a claim about formatting —
// a bare key and no "(now …)" — and a Contains check would not notice an
// empty suffix rendered beside it.
const (
	retryNamesNothing = "Re-run `takt close-wave`. If the review timed out, " +
		"raising `backends.<name>.timeout` in `.takt.json` is the fix."
	retryNamesPrefix = "Re-run `takt close-wave`. If the review timed out, " +
		"raising the deadline in `.takt.json` is the fix: "
)

// reviewErrorTaskID is the task the hand-built contexts below say the review
// errored for, matching the task id assertReviewErrorUnchanged expects in the
// question text.
const reviewErrorTaskID = 2

// reviewErrorContext is the gate context decideActiveWave builds, with the
// `backends` entry replaced by whatever the caller wants to hand the
// renderer — or, for a nil argument, left out of the map entirely. Every
// other entry is well formed, so a row that renders differently does so
// because of its `backends` value alone.
func reviewErrorContext(backends any) map[string]any {
	ctx := map[string]any{
		"slug":  "demo",
		"tasks": []any{reviewErrorTaskID},
		"error": "see waves/0/close.s1.json",
	}
	if backends != nil {
		ctx["backends"] = backends
	}
	return ctx
}

// TestReviewErrorToleratesAMalformedBackendsContext covers the guards the
// renderer keeps for a `backends` entry that is not the shape decide writes.
// Nothing in takt produces these contexts today — Question is called once per
// gate, on the map decide just built — but the map is also what the persisted
// gate payload decodes to, so the guards are what stops a future writer of
// that payload from turning a wrong shape into a panic or an invented key.
// Every row is fed by hand, because a well-formed build can never reach them.
//
// The rows are the four guards: a value that is not a list, an element that
// is not a map, an element with no usable key, and — the one that renders
// rather than skips — a key whose deadline is missing, so it is named bare.
func TestReviewErrorToleratesAMalformedBackendsContext(t *testing.T) {
	t.Parallel()
	const claudeKey, copilotKey = "backends.claude.timeout", "backends.copilot.timeout"
	wellFormed := map[string]any{"key": copilotKey, "timeout": "15m0s"}
	namedCopilot := retryNamesPrefix + "`" + copilotKey + "` (now 15m0s)."
	cases := []struct {
		name     string
		backends any
		want     string
	}{{
		name:     "the entry is absent",
		backends: nil,
		want:     retryNamesNothing,
	}, {
		name:     "the entry is not a list",
		backends: copilotKey,
		want:     retryNamesNothing,
	}, {
		// The regression this guard exists for: a builder that returned a
		// typed slice would render once and then, decoded, name nothing.
		name:     "the entry is a typed slice, which decoding never produces",
		backends: []map[string]any{wellFormed},
		want:     retryNamesNothing,
	}, {
		name:     "an element that is not a map is skipped",
		backends: []any{claudeKey, true, wellFormed},
		want:     namedCopilot,
	}, {
		name: "an element with no usable key is skipped",
		backends: []any{
			map[string]any{"timeout": "9m0s"},
			map[string]any{"key": "", "timeout": "9m0s"},
			map[string]any{"key": true, "timeout": "9m0s"},
			wellFormed,
		},
		want: namedCopilot,
	}, {
		name:     "a key whose deadline is missing is named bare",
		backends: []any{map[string]any{"key": copilotKey}},
		want:     retryNamesPrefix + "`" + copilotKey + "`.",
	}, {
		name: "an empty or non-string deadline is named bare too",
		backends: []any{
			map[string]any{"key": copilotKey, "timeout": ""},
			map[string]any{"key": claudeKey, "timeout": true},
		},
		want: retryNamesPrefix + "`" + copilotKey + "`, `" + claudeKey + "`.",
	}, {
		name:     "a list where nothing survives the skips names no key at all",
		backends: []any{claudeKey, true, map[string]any{}, map[string]any{"key": ""}},
		want:     retryNamesNothing,
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			q := decide.Question("review_error", reviewErrorContext(c.backends))
			if got := retryOption(t, &q).Description; got != c.want {
				t.Errorf("retry description = %q, want %q", got, c.want)
			}
			// A malformed payload costs the deadline, never the gate: the
			// question a user answers is the same one.
			assertReviewErrorUnchanged(t, &q)
		})
	}
}
