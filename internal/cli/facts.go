package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// The invalid/reset event pairs the agent attempt caps are counted from
// (spec §4.4). `takt record --agent` appends the invalid ones; `takt answer
// --gate agent_invalid --choice retry` appends the resets.
const (
	evAlignmentInvalid = "alignment_invalid"
	evAlignmentReset   = "alignment_attempts_reset"
	evGoalsInvalid     = "goals_invalid"
	evGoalsReset       = "goals_attempts_reset"
)

// fileNonEmpty reports whether p exists and holds something.
func fileNonEmpty(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Size() > 0
}

// gatherFacts reads everything Decide needs from the bundle (spec §5.3).
func gatherFacts(
	ctx context.Context, ws *workspace, bdir string, st *bundle.State,
	force, recovering bool, now time.Time, session string,
) (decide.Facts, error) {
	f := decide.Facts{
		Now: now, SessionID: session, Force: force, Recover: recovering,
		LockTTL: time.Duration(ws.Cfg.LockTTL), WaveStaleAfter: time.Duration(ws.Cfg.WaveStaleAfter),
		Wave: decide.WaveFacts{Recorded: map[int]bool{}},
	}
	f.HasSpec = fileNonEmpty(filepath.Join(bdir, "spec.md"))
	if b, err := os.ReadFile(filepath.Join(bdir, "goals.md")); err == nil {
		f.HasGoals = true
		f.GoalsFrozen = st.GoalsHash != nil && *st.GoalsHash == goals.Hash(b)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		return f, err
	}
	gatherIndexFacts(&f, ws, bdir)
	f.PlanAttempts = countSinceReset(events, "plan_invalid", "plan_attempts_reset")
	f.AlignmentAttempts = countSinceReset(events, evAlignmentInvalid, evAlignmentReset)
	f.AlignmentProblems = lastProblems(events, evAlignmentInvalid, evAlignmentReset)
	f.GoalsAttempts = countSinceReset(events, evGoalsInvalid, evGoalsReset)
	f.GoalsProblems = lastProblems(events, evGoalsInvalid, evGoalsReset)
	if err = gatherGateFacts(&f, bdir, st, events); err != nil {
		return f, err
	}
	if a, aerr := readAlignment(bdir); aerr == nil && a != nil && a.AnchorHash == anchorHash(st.Topic) {
		f.Alignment = decide.AlignmentFacts{
			ClausesPresent:   len(a.Clauses) > 0 || a.Skipped,
			ClausesConfirmed: a.Confirmed || a.Skipped,
			VerdictsPresent:  len(a.Verdicts) > 0 || a.Skipped,
			ClauseCount:      len(a.Clauses),
		}
	}
	if st.Phase == bundle.PhaseFinish {
		if f.Finish, err = gatherFinishFacts(ctx, ws, bdir, st); err != nil {
			return f, err
		}
	}
	err = gatherWaveFacts(&f, bdir, st)
	return f, err
}

// gatherIndexFacts fills the plan-index half of the facts: present, parsed,
// validated against this bundle's spec and goals, and accompanied by the
// plan.md the same planner was asked to write. This is the single seam every
// decision about the plan's validity reads from — `takt next` and `takt
// record --agent planner` both come through here.
func gatherIndexFacts(f *decide.Facts, ws *workspace, bdir string) {
	raw, err := os.ReadFile(filepath.Join(bdir, "plan.index.json"))
	if err != nil {
		return
	}
	f.HasIndex = true
	if idx, perr := plan.ParseIndex(raw); perr != nil {
		f.IndexProblems = []string{perr.Error()}
	} else {
		for _, p := range plan.Validate(idx, validateOpts(ws, bdir)) {
			f.IndexProblems = append(f.IndexProblems, p.String())
		}
	}
	// The planner writes plan.md as well as the index (spec §13), and the
	// plan gate hashes it — so a missing plan.md is a defect of the plan the
	// planner produced and is judged here with the rest of them. Checked
	// anywhere else, the index still reads valid to Decide: row 9 then emits
	// `exec takt review plan`, which dies in gate.Hash on the file nobody
	// wrote, and keeps dying, because nothing counts a planner attempt or
	// re-dispatches the planner (review N0). As an index problem it takes
	// row 8 instead, exactly as a malformed index does.
	if !fileNonEmpty(filepath.Join(bdir, "plan.md")) {
		f.IndexProblems = append(f.IndexProblems, "plan.md is missing or empty")
	}
	f.IndexValid = len(f.IndexProblems) == 0
}

// gatherGateFacts computes the two review gates this run has enabled, each
// only once its artifacts are all readable (spec §9).
func gatherGateFacts(f *decide.Facts, bdir string, st *bundle.State, events []bundle.Event) error {
	if st.Config.Review.Spec && f.HasSpec {
		s, err := gate.Compute(bdir, gate.Spec, events)
		if err != nil {
			return err
		}
		f.SpecGate = decide.GateStatus{Satisfied: s.Satisfied, Verdict: s.Verdict}
	}
	if st.Config.Review.Plan && f.HasIndex && f.IndexValid && fileNonEmpty(filepath.Join(bdir, "plan.md")) {
		s, err := gate.Compute(bdir, gate.Plan, events)
		if err != nil {
			return err
		}
		f.PlanGate = decide.GateStatus{Satisfied: s.Satisfied, Verdict: s.Verdict}
	}
	return nil
}

// gatherWaveFacts records which of the active wave's tasks have a digest for
// the current attempt, and its close record when one was written.
func gatherWaveFacts(f *decide.Facts, bdir string, st *bundle.State) error {
	aw := st.ActiveWave
	if aw == nil {
		return nil
	}
	for _, id := range aw.Tasks {
		if fileNonEmpty(digestPath(bdir, aw.N, id, aw.Attempt)) {
			f.Wave.Recorded[id] = true
		}
	}
	c, err := wave.ReadClose(bdir, aw.N, sliceOf(aw))
	if err != nil {
		return err
	}
	if closeMatchesDispatch(c, aw) {
		f.Wave.Close = &decide.CloseFacts{
			Committed: c.Committed, Failed: c.Failed, Blocked: c.Blocked,
			Rework: c.Rework, ReviewErrors: c.ReviewErrors,
		}
	}
	return nil
}

// countSinceReset counts events of type typ since the last reset event.
func countSinceReset(events []bundle.Event, typ, reset string) int {
	n := 0
	for _, e := range events {
		switch e.Type {
		case typ:
			n++
		case reset:
			n = 0
		}
	}
	return n
}

// lastProblems returns the rejection reasons the retried brief shows the
// agent and the agent_invalid question quotes: the problems of the newest
// `invalid` event, or of the newest `reset` when that is later. A retry
// carries the reasons it reset the count past onto its own event, because
// handing them to the next attempt is the whole point of the retry — the
// count starts over, the reasons do not.
func lastProblems(events []bundle.Event, invalid, reset string) []string {
	var out []string
	for _, e := range events {
		if e.Type == invalid || e.Type == reset {
			out = problemsOf(e.Data)
		}
	}
	return out
}

// problemsOf reads an event's recorded problem list. The data is read
// through comma-ok assertions: a malformed event yields no problems, never
// a panic.
func problemsOf(data map[string]any) []string {
	raw, isList := data[keyProblems].([]any)
	if !isList {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if s, ok := p.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// readArtifact returns a bundle file's text ("" when absent).
func readArtifact(bdir, name string) string {
	b, _ := os.ReadFile(filepath.Join(bdir, name))
	return strings.TrimRight(string(b), "\n")
}
