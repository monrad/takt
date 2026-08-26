package decide_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
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

func facts() decide.Facts {
	return decide.Facts{Now: t0, SessionID: "S", LockTTL: 10 * time.Minute, WaveStaleAfter: 30 * time.Minute,
		Wave: decide.WaveFacts{Recorded: map[int]bool{}}}
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
	for _, g := range []string{"owner", "gate_review", "alignment_confirm", "plan_invalid", "wave_failures", "review_error"} {
		q := decide.Question(g, map[string]any{"gate": "spec", "slug": "demo"})
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
	cases := []struct {
		name     string
		facts    decide.Facts
		wantOp   string
		wantGate string
		wantAg   string
	}{
		{"clauses, two rejections: dispatch", planFacts(decide.AlignmentFacts{}, 2),
			"dispatch", "", "alignment-auditor"},
		{"clauses, three rejections: ask", planFacts(decide.AlignmentFacts{}, 3),
			"ask", "agent_invalid", ""},
		{"verdicts, two rejections: dispatch", planFacts(confirmed, 2),
			"dispatch", "", "alignment-auditor"},
		{"verdicts, three rejections: ask", planFacts(confirmed, 3),
			"ask", "agent_invalid", ""},
		{"verdicts present: no ask despite rejections", planFacts(audited, 3), "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assertCapRow(t, mustDecide(t, planSt, c.facts), c.wantOp, c.wantGate, c.wantAg)
		})
	}
}

// assertCapRow checks one row of the cap table. An empty wantOp asserts only
// that the row does not raise the gate — whatever else it decides.
func assertCapRow(t *testing.T, d decide.Decision, wantOp, wantGate, wantAgent string) {
	t.Helper()
	switch wantOp {
	case "":
		if d.Action == decide.ActAsk && d.Op.Gate == "agent_invalid" {
			t.Fatalf("must not ask: %+v", d)
		}
	case "ask":
		if d.Action != decide.ActAsk || d.Op.Gate != wantGate {
			t.Fatalf("%+v", d)
		}
		if d.Op.Context["agent"] != "alignment-auditor" || d.Op.Context["attempts"] != 3 {
			t.Fatalf("context %+v", d.Op.Context)
		}
	case "dispatch":
		if d.Action != decide.ActDispatch || d.Agent == nil || d.Agent.Agent != wantAgent {
			t.Fatalf("%+v", d)
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
