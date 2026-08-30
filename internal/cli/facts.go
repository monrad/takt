package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
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
	evReviewerInvalid  = "reviewer_invalid"
	evReviewerReset    = "reviewer_attempts_reset"

	// reasonRecorded marks the reset a usable reply appends, as against the
	// one `agent_invalid`'s retry appends — which carries the problems.
	reasonRecorded = "recorded"

	// reasonStaleAttempt is why a digest or reviewer reply for an attempt the
	// active wave has moved past is ignored rather than acted on — spelled
	// once so goconst sees one definition instead of the same literal
	// repeated across recordTask and recordReviewer.
	reasonStaleAttempt = "not the active wave attempt"
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
		// The two terms every deadline that wraps a backend call is derived
		// from (spec A2.2). They are the binary's own per-unit caps, so the
		// session's deadline for an `exec` op is computed from exactly the
		// numbers the binary will apply to itself.
		BackendTimeout: time.Duration(ws.Cfg.Backends.ReviewBudgetTimeout()),
		VerifyTimeout:  time.Duration(ws.Cfg.VerifyTimeout),
		// The chain the review_error gate names when a review errors, which
		// is a fact about config alone and so is gathered with the rest of
		// them (spec A3).
		ReviewerBackends: reviewerBackends(ws.Cfg.Backends),
		Wave:             decide.WaveFacts{Recorded: map[int]bool{}},
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
	pi := gatherIndexFacts(&f, ws, bdir)
	f.PlanAttempts = countSinceReset(events, "plan_invalid", "plan_attempts_reset")
	f.AlignmentAttempts = countSinceReset(events, evAlignmentInvalid, evAlignmentReset)
	f.AlignmentProblems = lastProblems(events, evAlignmentInvalid, evAlignmentReset)
	f.GoalsAttempts = countSinceReset(events, evGoalsInvalid, evGoalsReset)
	f.GoalsProblems = lastProblems(events, evGoalsInvalid, evGoalsReset)
	f.ReviewerAttempts = countSinceReset(events, evReviewerInvalid, evReviewerReset)
	f.ReviewerProblems = lastProblems(events, evReviewerInvalid, evReviewerReset)
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
		// Row 20 is the only consumer of the verify command count, and an
		// index it cannot be counted from fails this gather — so it is
		// counted only while row 20 is still ahead, the same economy
		// gatherFinishFacts gathers the availability facts under. Once HEAD
		// is verified nothing would read the count, and a plan index that
		// went unreadable after verification must not fail the goals, retro,
		// branch_finish, push_pr and archive rows, none of which read it.
		if !f.Finish.Verified {
			if f.Finish.VerifyCommands, err = verifyCommandCount(bdir, pi); err != nil {
				return f, err
			}
		}
	}
	err = gatherWaveFacts(&f, bdir, st, events, pi)
	return f, err
}

// reviewerBackends is the configured reviewer chain as the review_error gate
// names it (spec A3): every entry with a real backends.<name>.timeout key, in
// the preference order backends.reviewer lists them, carrying the deadline
// that key holds today.
//
// An entry config cannot speak for — "fake", or a name it does not know — is
// skipped rather than rendered as a key that does not exist; backends.reviewer
// is not validated against a closed set, so such an entry is legal. No health
// probe is made: gatherFacts must not shell out, so which backend would
// actually run is unknowable here, and naming every candidate is accurate
// without one.
func reviewerBackends(b config.Backends) []decide.ReviewerBackend {
	out := make([]decide.ReviewerBackend, 0, len(b.Reviewer))
	for _, name := range b.Reviewer {
		d, ok := b.Timeout(name)
		if !ok {
			continue
		}
		out = append(out, decide.ReviewerBackend{Name: name, Timeout: time.Duration(d)})
	}
	return out
}

// planIndex is the one read of plan.index.json a gatherFacts call makes,
// carrying both answers that read has to give. The plan facts read it
// softly — an index that cannot be parsed is a fact about the plan, which
// row 8 judges and no other row needs — while the two deadline counts
// cannot: a budget counted off an index nobody could read is a deadline for
// work the binary will never reach, since close-wave and verify both fail
// on the same file first. Exactly one field is set, and the file is read
// once, so the soft half and the hard half can never disagree about what it
// held.
type planIndex struct {
	idx plan.Index // the parsed index; meaningful only when err is nil
	err error      // why there is none: unreadable, or unparsable
}

// gatherIndexFacts fills the plan-index half of the facts: present, parsed,
// validated against this bundle's spec and goals, and accompanied by the
// plan.md the same planner was asked to write. This is the single seam every
// decision about the plan's validity reads from — `takt next` and `takt
// record --agent planner` both come through here.
//
// It also hands the parsed index back, for the wave and finish facts to
// count the deadlines' work from. That index is missing only when the file
// cannot be read or cannot be parsed — never merely because validation
// found problems — which is the parse-only view readIndex gives close-wave
// and verify. Counting only a valid index would floor the session's
// deadline at [deadline.Floor] while the binary budgeted the real thing,
// which is the containment break this plumbing exists to close.
func gatherIndexFacts(f *decide.Facts, ws *workspace, bdir string) planIndex {
	raw, err := os.ReadFile(indexPath(bdir))
	if err != nil {
		return planIndex{err: err}
	}
	f.HasIndex = true
	parsed, perr := plan.ParseIndex(raw)
	if perr != nil {
		f.IndexProblems = []string{perr.Error()}
	} else {
		for _, p := range plan.Validate(parsed, validateOpts(ws, bdir)) {
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
	if perr != nil {
		return planIndex{err: perr}
	}
	return planIndex{idx: parsed}
}

// gatherGateFacts computes the two review gates this run has enabled, each
// only once its artifacts are all readable (spec §9).
func gatherGateFacts(f *decide.Facts, bdir string, st *bundle.State, events []bundle.Event) error {
	if st.Config.Review.Spec && f.HasSpec {
		s, err := gate.Compute(bdir, gate.Spec, events)
		if err != nil {
			return err
		}
		f.SpecGate = decide.GateStatus{
			Satisfied: s.Satisfied, Verdict: s.Verdict, Blocking: s.Blocking, Reason: s.Reason,
		}
		f.SpecRounds = gate.Rounds(events, gate.Spec)
	}
	if st.Config.Review.Plan && f.HasIndex && f.IndexValid && fileNonEmpty(filepath.Join(bdir, "plan.md")) {
		s, err := gate.Compute(bdir, gate.Plan, events)
		if err != nil {
			return err
		}
		f.PlanGate = decide.GateStatus{
			Satisfied: s.Satisfied, Verdict: s.Verdict, Blocking: s.Blocking, Reason: s.Reason,
		}
	}
	return nil
}

// gatherWaveFacts records which of the active wave's tasks have a digest for
// the current attempt, its close record when one was written, the internal
// review's state for the dispatch, and the work the close itself has to fit
// into — which is what the session's deadline for `exec takt close-wave` is
// derived from.
func gatherWaveFacts(
	f *decide.Facts, bdir string, st *bundle.State, events []bundle.Event, pi planIndex,
) error {
	aw := st.ActiveWave
	if aw == nil {
		return nil
	}
	countWaveWork(f, st, aw, pi)
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
	f.Wave.Internal = gatherInternalFacts(bdir, st, aw, events)
	return nil
}

// countWaveWork counts what the close of this wave has to do, over exactly
// the set closeBudget counts on the binary side: the active wave's PENDING
// tasks, which after a recovery can hold more tasks than the dispatch's own
// list. A task the plan index does not hold contributes no verify commands
// — close-wave cannot run commands it cannot read — and reviews are counted
// only when the run has review.tasks on, since a run without it makes no
// backend call at all.
//
// An index that could not be read or parsed leaves both counts at zero, and
// that is the honest count rather than a shortcut: closeWaveBudgeted fails
// on readIndex before it builds a budget at all, so the binary verifies
// nothing and reviews nobody. Counting the pending tasks anyway would size
// the session's deadline for reviews that never happen.
func countWaveWork(f *decide.Facts, st *bundle.State, aw *bundle.ActiveWave, pi planIndex) {
	if pi.err != nil {
		return
	}
	tasks := 0
	for _, t := range st.Tasks {
		if t.Wave != aw.N || t.Status != bundle.StatusPending {
			continue
		}
		tasks++
		if pt := pi.idx.Task(t.ID); pt != nil {
			f.Wave.VerifyCommands += len(pt.Verify)
		}
	}
	if st.Config.Review.Tasks {
		f.Wave.ReviewTasks = tasks
	}
}

// gatherInternalFacts reads the internal review's state for the active
// dispatch (two-layers design §4.1). Candidates is computed through the
// same wave.MergeCandidates every other consumer uses, so decide, the
// verify brief and close-wave can never disagree about the list.
func gatherInternalFacts(
	bdir string, st *bundle.State, aw *bundle.ActiveWave, events []bundle.Event,
) decide.InternalFacts {
	in := decide.InternalFacts{
		Lenses: st.Config.Review.Lenses, Recorded: map[string]bool{},
	}
	if len(in.Lenses) == 0 {
		return in
	}
	for _, id := range aw.Tasks {
		if d, _, _ := latestDigest(bdir, aw.N, id, aw.Attempt); d != nil && d.Status == bundle.StatusDone {
			in.HasDoneDigest = true
			break
		}
	}
	records := map[string]*wave.LensRecord{}
	all := true
	for _, l := range in.Lenses {
		r, err := wave.ReadLensRecord(bdir, aw.N, sliceOf(aw), aw.Attempt, l)
		if err != nil || r == nil {
			all = false
			continue
		}
		in.Recorded[l] = true
		records[l] = r
	}
	if all {
		in.Candidates = len(wave.MergeCandidates(in.Lenses, records))
	}
	if r, err := wave.ReadInternalRecord(bdir, aw.N, sliceOf(aw), aw.Attempt); err == nil && r != nil {
		in.VerifyRecorded = true
	}
	in.Skipped = internalSkipped(events, aw.N, sliceOf(aw), aw.Attempt)
	return in
}

// internalSkipped reports an internal_review_skipped event for exactly this
// dispatch.
func internalSkipped(events []bundle.Event, waveN, slice, attempt int) bool {
	for _, e := range events {
		if e.Type == "internal_review_skipped" &&
			toInt(e.Data[keyWave]) == waveN && toInt(e.Data[keySlice]) == slice &&
			toInt(e.Data[keyAttempt]) == attempt {
			return true
		}
	}
	return false
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

// endAttemptStreak records that a usable reply ended a run of rejected ones:
// the attempt cap starts over, and — because this reset carries no problems,
// unlike the one `answer --gate agent_invalid --choice retry` writes — so
// does what the next brief quotes back, which is what keeps a rejection from
// one auditor mode out of the other's brief (spec §5.3 rows 10, 11, 21).
//
// There are two ways to have something to end: rejections since the last
// reset, and a retry's reset carrying the problems of the ones before it —
// those problems outlive the count on purpose (the retried brief quotes
// them), so the record that answers them has to retire them. With neither,
// nothing is appended: a clean run's log stays clean, and so does a second
// valid record in a row.
//
// A lost append is reported by the caller, at exit 0 (the warnings
// contract): every call site runs after the substantive write has already
// landed, so failing here would halt a run over bookkeeping. A lost read is
// returned for the same reason — a log that cannot be read is a streak that
// cannot be judged, which is the same loss arriving one step earlier. The
// cost either way is that the streak keeps counting, and the next brief keeps
// quoting the old rejection, until some later reset lands.
func endAttemptStreak(bdir, invalid, reset string, data map[string]any) error {
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		return err
	}
	if countSinceReset(events, invalid, reset) == 0 && len(lastProblems(events, invalid, reset)) == 0 {
		return nil
	}
	return bundle.AppendEvent(bdir, reset, data)
}

// warnStreakLoss folds a failed endAttemptStreak into the document a record
// verb is about to print: one sentence naming what was not written, appended
// to any warning already there, and nothing at all when the reset landed —
// the key is absent rather than empty, so a clean record's JSON is what it
// has always been. The exit code and every existing key are untouched.
func warnStreakLoss(out map[string]any, err error) map[string]any {
	if err == nil {
		return out
	}
	prev, _ := out[keyWarnings].([]string)
	out[keyWarnings] = append(prev, "attempt-streak reset not recorded: "+err.Error())
	return out
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
