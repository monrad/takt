// Package decide is the pure control loop of takt (spec §5.3): given the
// state and facts gathered from disk, it returns the one thing to do next.
// It performs no I/O; `takt next` executes the Decision.
package decide

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/op"
)

// Context map keys shared by every ctx built here and by Question — pulled
// out as constants because goconst flags the repeated literals, not because
// callers reference them (they still describe the JSON key in op.Op.Context).
const (
	ctxSlug      = "slug"
	ctxWave      = "wave"
	ctxCount     = "count"
	ctxFailed    = "failed"
	ctxBlocked   = "blocked"
	ctxExhausted = "exhausted"
	ctxRework    = "rework"
)

// ids normalises a nil id list to an empty one. A gate's context is
// persisted as the pending gate's payload and re-rendered from it verbatim
// (spec §4.3), so `null` where the user expects a list is durable noise —
// every id list in a question renders as `[]` when it is empty.
func ids(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

// Action is what `takt next` must do with a Decision.
type Action string

// Actions. The first five map 1:1 to op kinds; the rest are side effects
// `takt next` performs before deciding again.
const (
	ActAsk        Action = "ask"
	ActRun        Action = "run"
	ActExec       Action = "exec"
	ActDispatch   Action = "dispatch"
	ActStop       Action = "stop"
	ActTransition Action = "transition" // Phase
	ActLoadPlan   Action = "load_plan"  // materialise tasks, phase → execute
	ActLaunch     Action = "launch"     // Wave, Tasks, Attempt
	ActRecover    Action = "recover"    // Tasks to reset, then launch with Attempt
	ActClearWave  Action = "clear_wave" // close.json says committed
	ActArchive    Action = "archive"    // phase → archived, commit, apply the disposition, stop
)

// GateStatus summarises a gate receipt (spec §9).
type GateStatus struct {
	Satisfied bool
	Verdict   string // "", approve, rework, reject, error, skipped, overridden
}

// AlignmentFacts summarises alignment.json. ClauseCount is how many clauses
// are on disk: the alignment_confirm question names the range A1..An, and
// `takt next` persists that op as the gate payload, so a missing count is a
// durable "A1..A<nil>" in the user's face (review finding 4).
type AlignmentFacts struct {
	ClausesPresent   bool
	ClausesConfirmed bool
	VerdictsPresent  bool
	ClauseCount      int
}

// CloseFacts summarises waves/<n>/close.json for the active attempt.
type CloseFacts struct {
	Committed    bool
	Failed       []int
	Blocked      []int
	Rework       []int
	ReviewErrors []int
}

// WaveFacts is what is on disk for the active wave attempt.
type WaveFacts struct {
	Recorded map[int]bool // task id → digest present for this attempt
	Close    *CloseFacts  // nil until close-wave wrote close.json
}

// Facts is everything Decide needs beyond the state.
type Facts struct {
	Now            time.Time
	SessionID      string
	Force          bool
	Recover        bool
	LockTTL        time.Duration
	WaveStaleAfter time.Duration

	HasSpec       bool
	HasGoals      bool
	GoalsFrozen   bool
	HasIndex      bool
	IndexValid    bool
	IndexProblems []string
	PlanAttempts  int

	SpecGate  GateStatus
	PlanGate  GateStatus
	Alignment AlignmentFacts
	Wave      WaveFacts
	Finish    FinishFacts
}

// Decision is Decide's answer.
type Decision struct {
	Action  Action
	Op      *op.Op    // ask / run / exec / stop; nil for side-effect actions
	Phase   string    // transition target
	Wave    int       // launch / recover
	Attempt int       // launch / recover
	Tasks   []int     // launch / recover
	Agent   *op.Agent // dispatch of a single non-task agent (Brief filled by the caller)
}

// maxPlannerAttempts is how many invalid indexes are tolerated before asking (§5.3 row 8).
const maxPlannerAttempts = 3

// Decide applies the §5.3 precedence table.
func Decide(st *bundle.State, f Facts) (Decision, error) {
	if st.PendingGate != nil {
		return rerender(st.PendingGate)
	}
	switch st.Phase {
	case bundle.PhaseBrainstorm:
		return decideBrainstorm(st, f), nil
	case bundle.PhasePlan:
		return decidePlan(st, f), nil
	case bundle.PhaseExecute:
		return decideExecute(st, f)
	case bundle.PhaseFinish:
		return decideFinish(st, f), nil
	case bundle.PhaseArchived:
		return stop("run archived", "archived"), nil
	}
	return Decision{}, fmt.Errorf("unknown phase %q", st.Phase)
}

func rerender(pg *bundle.PendingGate) (Decision, error) {
	var o op.Op
	if len(pg.Payload) == 0 {
		return Decision{}, errors.New("pending_gate has no payload")
	}
	if err := json.Unmarshal(pg.Payload, &o); err != nil {
		return Decision{}, fmt.Errorf("pending_gate payload: %w", err)
	}
	return Decision{Action: ActAsk, Op: &o}, nil
}

func stop(narration, reason string) Decision {
	return Decision{Action: ActStop, Op: &op.Op{Op: op.Stop, Narration: narration, Reason: reason}}
}

func ask(gate string, ctx map[string]any) Decision {
	q := Question(gate, ctx)
	return Decision{Action: ActAsk, Op: &q}
}

func exec(narration, command string, timeoutS int) Decision {
	return Decision{
		Action: ActExec,
		Op:     &op.Op{Op: op.Exec, Narration: narration, Command: command, TimeoutS: timeoutS},
	}
}

func run(step, narration string, inputs map[string]any) Decision {
	return Decision{Action: ActRun, Op: &op.Op{Op: op.Run, Narration: narration, Step: step, Inputs: inputs,
		Done: fmt.Sprintf("takt done --step %s --slug %v", step, inputs["slug"])}}
}

const (
	reviewTimeoutS = 900
	closeTimeoutS  = 1800
)

func decideBrainstorm(st *bundle.State, f Facts) Decision {
	in := map[string]any{ctxSlug: st.Slug, "topic": st.Topic}
	if !f.HasSpec {
		return run("brainstorm", "brainstorm the spec", in)
	}
	if st.Config.Goals && (!f.HasGoals || !f.GoalsFrozen) {
		return run("goals", "distil and freeze the goals", in)
	}
	if st.Config.Review.Spec && !f.SpecGate.Satisfied {
		if needsRework(f.SpecGate) {
			return ask(
				"gate_review",
				map[string]any{
					ctxSlug:   st.Slug,
					"gate":    "spec",
					"verdict": f.SpecGate.Verdict,
					"summary": "see reviews/spec.md",
				},
			)
		}
		return exec("review the spec", "takt review spec --slug "+st.Slug, reviewTimeoutS)
	}
	return Decision{Action: ActTransition, Phase: bundle.PhasePlan}
}

func needsRework(g GateStatus) bool {
	return g.Verdict == "rework" || g.Verdict == "reject" || g.Verdict == "error"
}

func decidePlan(st *bundle.State, f Facts) Decision {
	if !f.HasIndex || !f.IndexValid {
		if f.PlanAttempts >= maxPlannerAttempts {
			return ask(
				"plan_invalid",
				map[string]any{ctxSlug: st.Slug, "attempts": f.PlanAttempts, "problems": f.IndexProblems},
			)
		}
		return Decision{Action: ActDispatch, Agent: &op.Agent{Agent: "planner", Label: "plan the run"}}
	}
	if st.Config.Review.Plan && !f.PlanGate.Satisfied {
		if needsRework(f.PlanGate) {
			return ask(
				"gate_review",
				map[string]any{
					ctxSlug:   st.Slug,
					"gate":    "plan",
					"verdict": f.PlanGate.Verdict,
					"summary": "see reviews/plan.md",
				},
			)
		}
		return exec("review the plan", "takt review plan --slug "+st.Slug, reviewTimeoutS)
	}
	if st.Config.Alignment {
		switch {
		case !f.Alignment.ClausesPresent:
			return Decision{
				Action: ActDispatch,
				Agent:  &op.Agent{Agent: "alignment-auditor", Mode: "clauses", Label: "decompose the request"},
			}
		case !f.Alignment.ClausesConfirmed:
			return ask("alignment_confirm", map[string]any{ctxSlug: st.Slug, ctxCount: f.Alignment.ClauseCount})
		case !f.Alignment.VerdictsPresent:
			return Decision{
				Action: ActDispatch,
				Agent:  &op.Agent{Agent: "alignment-auditor", Mode: "verdicts", Label: "audit the plan"},
			}
		}
	}
	return Decision{Action: ActLoadPlan}
}

func decideExecute(st *bundle.State, f Facts) (Decision, error) {
	if len(st.Tasks) == 0 {
		return Decision{}, errors.New("phase is execute but tasks is empty — the plan was never loaded")
	}
	if aw := st.ActiveWave; aw != nil {
		return decideActiveWave(st, aw, f), nil
	}
	pending, failed, blocked := []int{}, []int{}, []int{}
	for _, t := range st.Tasks {
		switch t.Status {
		case bundle.StatusPending:
			pending = append(pending, t.ID)
		case bundle.StatusFailed:
			failed = append(failed, t.ID)
		case bundle.StatusBlocked:
			blocked = append(blocked, t.ID)
		}
	}
	if len(pending) > 0 {
		wave := lowestWave(st, pending)
		var ids []int
		for _, id := range pending {
			if st.Task(id).Wave == wave {
				ids = append(ids, id)
			}
		}
		sort.Ints(ids)
		return Decision{Action: ActLaunch, Wave: wave, Tasks: ids, Attempt: 1}, nil
	}
	if len(failed) > 0 || len(blocked) > 0 {
		// Failed and blocked are shown under their own headings: lumping the
		// blocked ids in with the failed ones told the user a task had tried
		// and failed when it had reported that it could not start.
		return ask(
			"wave_failures",
			map[string]any{
				ctxSlug:      st.Slug,
				ctxWave:      lowestWave(st, append(slices.Clone(failed), blocked...)),
				ctxFailed:    failed,
				ctxBlocked:   blocked,
				ctxExhausted: []int{},
				ctxRework:    []int{},
			},
		), nil
	}
	return Decision{Action: ActTransition, Phase: bundle.PhaseFinish}, nil
}

func lowestWave(st *bundle.State, ids []int) int {
	w := -1
	for _, id := range ids {
		if t := st.Task(id); t != nil && (w < 0 || t.Wave < w) {
			w = t.Wave
		}
	}
	return w
}

func decideActiveWave(st *bundle.State, aw *bundle.ActiveWave, f Facts) Decision {
	var unrecorded []int
	for _, id := range aw.Tasks {
		if !f.Wave.Recorded[id] {
			unrecorded = append(unrecorded, id)
		}
	}
	if len(unrecorded) > 0 {
		fresh := f.Now.Sub(aw.StartedAt) < f.WaveStaleAfter
		if !f.Recover && aw.SessionID == f.SessionID && fresh {
			return stop(
				fmt.Sprintf(
					"wave %d in flight: %d of %d results recorded",
					aw.N,
					len(aw.Tasks)-len(unrecorded),
					len(aw.Tasks),
				),
				"wave_in_flight",
			)
		}
		return Decision{Action: ActRecover, Wave: aw.N, Tasks: unrecorded, Attempt: aw.Attempt + 1}
	}
	c := f.Wave.Close
	if c == nil {
		return exec(
			fmt.Sprintf("closing wave %d: verify + review %d tasks", aw.N, len(aw.Tasks)),
			"takt close-wave --slug "+st.Slug,
			closeTimeoutS,
		)
	}
	if c.Committed {
		return Decision{Action: ActClearWave, Wave: aw.N}
	}
	if len(c.ReviewErrors) > 0 {
		return ask(
			"review_error",
			map[string]any{
				ctxSlug: st.Slug,
				ctxWave: aw.N,
				"tasks": c.ReviewErrors,
				"error": "see waves/" + strconv.Itoa(aw.N) + "/close.json",
			},
		)
	}
	retry, exhausted := []int{}, []int{}
	for _, id := range c.Rework {
		if t := st.Task(id); t != nil && t.Attempt < 1+st.Config.MaxRework {
			retry = append(retry, id)
		} else {
			exhausted = append(exhausted, id)
		}
	}
	if len(c.Failed) == 0 && len(c.Blocked) == 0 && len(exhausted) == 0 && len(retry) > 0 {
		return Decision{Action: ActLaunch, Wave: aw.N, Tasks: retry, Attempt: aw.Attempt + 1}
	}
	// retry rides along: when a failed or blocked task is holding the wave,
	// the rework tasks that would have been re-dispatched on their own are
	// part of what the user is deciding about and the question has to name
	// them (they are re-dispatched by whichever choice reopens the wave).
	return ask(
		"wave_failures",
		map[string]any{
			ctxSlug:      st.Slug,
			ctxWave:      aw.N,
			ctxFailed:    ids(c.Failed),
			ctxBlocked:   ids(c.Blocked),
			ctxExhausted: exhausted,
			ctxRework:    retry,
		},
	)
}
