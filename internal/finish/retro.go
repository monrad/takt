package finish

import (
	"cmp"
	"path/filepath"
	"slices"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// RetroInputs is what the session writes retro.md from (spec §7.5 step 3).
type RetroInputs struct {
	Slug           string         `json:"slug"`
	Topic          string         `json:"topic"`
	Tasks          int            `json:"tasks"`
	Waves          int            `json:"waves"`
	Retries        []RetroRetry   `json:"retries"`
	Failures       []RetroFailure `json:"failures"`
	ReviewFindings int            `json:"review_findings"`
	// GateReviewFindings and TaskReviewFindings split ReviewFindings, which
	// is their sum: the findings every gate pass raised, and the findings
	// every attempt's task reviews raised. Both are read from the event log
	// — the only append-only record of an attempt whose close record a
	// later attempt retired (#23).
	GateReviewFindings int           `json:"gate_review_findings"`
	TaskReviewFindings int           `json:"task_review_findings"`
	WaveTimings        []WaveTiming  `json:"wave_timings"`
	Verify             *VerifyRecord `json:"verify,omitempty"`
	Goals              *GoalsRecord  `json:"goals,omitempty"`
	// FollowUps are review findings that closed with their gate instead of
	// being acted on — an approving pass's minors, or a verdict the user
	// overrode. The retro lists them so they reach a human (#29).
	FollowUps []gate.FollowUp `json:"follow_ups,omitempty"`
	// Internal instruments both review layers — the lens candidates and what
	// the backend's scoped pass did with them (two-layers design §9). Nil
	// when the run recorded no internal review at all.
	Internal *InternalReview `json:"internal_review,omitempty"`
}

// LensStats is one lens's tally across the run: how many candidates it
// reported and how many of those the verifier confirmed.
type LensStats struct {
	Reported  int `json:"reported"`
	Confirmed int `json:"confirmed"`
}

// InternalReview tallies the internal review layer against the backend's own
// grading pass, so the retro can say what each found that the other did not
// (two-layers design §9).
type InternalReview struct {
	Candidates     int `json:"candidates"`
	Confirmed      int `json:"confirmed"`
	FalsePositives int `json:"false_positives"`
	Unattributed   int `json:"unattributed"`
	// ByLens is keyed by lens name: how many candidates it reported and how
	// many of those were confirmed.
	ByLens map[string]LensStats `json:"by_lens"`
	// ScopedPasses is how many task reviews ran a scoped pass — a
	// BlindReview followed by a scoped Review — over the confirmed internal
	// findings (two-layers design §3.5).
	ScopedPasses int `json:"scoped_passes"`
	// ScopedChanged is how many of those scoped passes landed a different
	// verdict than the blind pass that preceded them.
	ScopedChanged int `json:"scoped_changed_verdict"`
	// Overlap is how many confirmed internal findings the backend's blind
	// pass — the one that never saw the lens candidates — also raised on its
	// own: same file, within a few lines (a heuristic: overlapLineTolerance).
	// The scoped pass that can follow it is graded on those very candidates,
	// so its agreement would be an echo, not independent overlap (two-layers
	// design §9).
	Overlap int `json:"overlap"`
	// Skipped is how many dispatches ran with no internal review at all
	// (internal_review_skipped events).
	Skipped int `json:"skipped"`
}

// RetroRetry is a task that needed more than one attempt.
type RetroRetry struct {
	Task     int `json:"task"`
	Attempts int `json:"attempts"`
}

// RetroFailure is a task that did not end `done`, with its last reason.
type RetroFailure struct {
	Task   int    `json:"task"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// WaveTiming is dispatch → close for one dispatched attempt that closed —
// one per attempt, so a wave that was reworked reports the attempt that was
// thrown away as well as the one that landed. Committed says whether that
// attempt's close made a commit; CommittedAt is present only when it did.
type WaveTiming struct {
	Wave         int       `json:"wave"`
	Slice        int       `json:"slice"`
	Attempt      int       `json:"attempt"`
	DispatchedAt time.Time `json:"dispatched_at"`
	ClosedAt     time.Time `json:"closed_at"`
	Committed    bool      `json:"committed"`
	// CommittedAt is omitted for an attempt that closed without committing.
	// omitzero, not omitempty: encoding/json never omits a zero-valued
	// struct under omitempty and would write a year-1 timestamp, while Go
	// 1.24+ omits a zero time.Time under omitzero (#25).
	CommittedAt time.Time `json:"committed_at,omitzero"`
}

// The event types the retro counts and pairs, and the data keys that
// identify which dispatch each one belongs to and what it counted.
const (
	evDispatched      = "wave_dispatched"
	evCommitted       = "wave_committed"
	evClosed          = "wave_closed"
	evGateReviewed    = "gate_reviewed"
	keyWave           = "wave"
	keySlice          = "slice"
	keyAttempt        = "attempt"
	keyFindings       = "findings"
	keyReviewFindings = "review_findings"
)

// BuildRetroInputs is pure: state + index + events + close records → inputs.
// Nothing here reads the filesystem, so the same bundle always yields the
// same file and re-emitting the retro op is free (spec §5.4).
func BuildRetroInputs(
	st *bundle.State, idx plan.Index, events []bundle.Event,
	closes []wave.CloseResult, v *VerifyRecord, g *GoalsRecord, followUps []gate.FollowUp,
	internals []wave.InternalRecord,
) RetroInputs {
	in := RetroInputs{
		Slug: st.Slug, Topic: st.Topic, Tasks: len(idx.Tasks),
		Retries: []RetroRetry{}, Failures: []RetroFailure{}, WaveTimings: []WaveTiming{},
		Verify: v, Goals: g,
	}
	in.FollowUps = followUps
	waves := map[int]bool{}
	reasons := lastReasons(closes)
	for _, t := range st.Tasks {
		waves[t.Wave] = true
		if t.Attempt > 1 {
			in.Retries = append(in.Retries, RetroRetry{Task: t.ID, Attempts: t.Attempt})
		}
		if t.Status != bundle.StatusDone {
			in.Failures = append(in.Failures, RetroFailure{Task: t.ID, Status: t.Status, Reason: reasons[t.ID]})
		}
	}
	in.Waves = len(waves)
	countReviewFindings(&in, events)
	in.WaveTimings = waveTimings(events)
	in.Internal = buildInternalReview(internals, closes)
	if in.Internal != nil {
		for _, e := range events {
			if e.Type == "internal_review_skipped" {
				in.Internal.Skipped++
			}
		}
	}
	return in
}

// countReviewFindings sums every review the run recorded — gate passes and
// each attempt's task reviews, kept apart so the retro can say which is
// which — from the event log rather than the close records on disk: the
// record of a reworked attempt is deleted when the next attempt closes,
// while its wave_closed event, carrying the findings it graded, stays. A
// wave_closed written before that key existed counts zero, which is the
// status quo (#23).
func countReviewFindings(in *RetroInputs, events []bundle.Event) {
	for _, e := range events {
		switch e.Type {
		case evGateReviewed:
			n, _ := e.Data[keyFindings].(float64)
			in.GateReviewFindings += int(n)
		case evClosed:
			n, _ := e.Data[keyReviewFindings].(float64)
			in.TaskReviewFindings += int(n)
		}
	}
	in.ReviewFindings = in.GateReviewFindings + in.TaskReviewFindings
}

// overlapLineTolerance is how close a backend finding must be to a confirmed
// internal finding, same file, to count as overlap — a heuristic, named as
// one in the retro template (two-layers design §9).
const overlapLineTolerance = 3

// buildInternalReview tallies both layers so the retro can say what each
// found that the other did not (two-layers design §9). Nil when the run
// recorded no internal review at all.
func buildInternalReview(internals []wave.InternalRecord, closes []wave.CloseResult) *InternalReview {
	if len(internals) == 0 {
		return nil
	}
	ir := &InternalReview{ByLens: map[string]LensStats{}}
	for _, rec := range internals {
		tallyInternalRecord(ir, rec)
	}
	for _, c := range closes {
		for _, tr := range c.Tasks {
			tallyScopedPass(ir, tr)
			ir.Overlap += overlapCount(tr)
		}
	}
	return ir
}

// tallyInternalRecord folds one dispatch's candidates into the running
// totals: how many were reported and confirmed overall, per lens, and how
// many confirmed findings named no task.
func tallyInternalRecord(ir *InternalReview, rec wave.InternalRecord) {
	confirmed := map[string]bool{}
	for _, id := range rec.Confirmed {
		confirmed[id] = true
	}
	ir.Candidates += len(rec.Candidates)
	ir.Confirmed += len(rec.Confirmed)
	ir.FalsePositives += len(rec.Candidates) - len(rec.Confirmed)
	for _, c := range rec.Candidates {
		if confirmed[c.ID] && c.Task == 0 {
			ir.Unattributed++
		}
		for _, lens := range c.Lenses {
			s := ir.ByLens[lens]
			s.Reported++
			if confirmed[c.ID] {
				s.Confirmed++
			}
			ir.ByLens[lens] = s
		}
	}
}

// tallyScopedPass counts one task review as a scoped pass when a blind pass
// preceded it, and as a verdict change when the scoped pass landed on a
// different verdict than the blind one (two-layers design §3.5).
func tallyScopedPass(ir *InternalReview, tr wave.TaskResult) {
	if tr.BlindReview == nil {
		return
	}
	ir.ScopedPasses++
	if tr.Review != nil && tr.Review.Verdict != tr.BlindReview.Verdict {
		ir.ScopedChanged++
	}
}

// overlapCount is the confirmed internal findings of one task the backend's
// blind pass also found on its own: same file, within overlapLineTolerance
// lines. When no scoped pass ran, tr.Review is the blind pass; when one did,
// tr.BlindReview holds it aside and tr.Review became the scoped pass — the
// backend's adjudication of the very candidates being measured here, so it
// must not be the one read (two-layers design §9).
func overlapCount(tr wave.TaskResult) int {
	blind := tr.Review
	if tr.BlindReview != nil {
		blind = tr.BlindReview
	}
	if blind == nil {
		return 0
	}
	n := 0
	for _, f := range tr.Internal {
		for _, b := range blind.Findings {
			if b.File == f.File && abs(b.Line-f.Line) <= overlapLineTolerance {
				n++
				break
			}
		}
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// lastReasons is each task's reason from the latest close record that
// graded it.
func lastReasons(closes []wave.CloseResult) map[int]string {
	out := map[int]string{}
	for _, c := range closes {
		for _, tr := range c.Tasks {
			if tr.Reason != "" {
				out[tr.Task] = tr.Reason
			}
		}
	}
	return out
}

// timingKey is the dispatch an event belongs to: wave, slice and attempt.
type timingKey struct{ wave, slice, attempt int }

// timingKeyOf reads one event's dispatch key. An event written before slices
// were recorded carries no slice key and decodes to 0, which is floored to
// 1: that is the number takt heals a slice-less wave to, so a bundle that
// upgraded mid-run — an old wave_dispatched answered by a healed event that
// says slice 1 — still pairs, and two old events still pair with each other.
func timingKeyOf(e bundle.Event) timingKey {
	w, _ := e.Data[keyWave].(float64)
	sl, _ := e.Data[keySlice].(float64)
	a, _ := e.Data[keyAttempt].(float64)
	return timingKey{wave: int(w), slice: max(int(sl), 1), attempt: int(a)}
}

// closeKeyOf reads one close record's dispatch key. The slice is floored the
// way [timingKeyOf] floors an event's, so a record written before slices
// were recorded pairs with the events of the slice takt heals it to rather
// than with a slice 0 that never ran.
func closeKeyOf(c wave.CloseResult) timingKey {
	return timingKey{wave: c.Wave, slice: max(c.Slice, 1), attempt: c.Attempt}
}

// waveTimings reports one span per dispatched attempt that closed: each
// wave_closed is paired with the wave_dispatched of the same wave, slice and
// attempt, and carries the commit time of the wave_committed with that key
// when the attempt committed. A wave that was reworked therefore reports the
// attempt that was thrown away as well as the one that landed, and a wave
// larger than max_parallel reports one span per slice: its slices all run at
// attempt 1, so wave and attempt alone would collapse them into one. A
// dispatch with no wave_closed — an attempt still running — is omitted, and
// the spans come out ordered by wave, then slice, then attempt (#25).
//
// One dispatch can close twice: a close that finds failures raises
// wave_failures without committing, the user waives them, and the next close
// commits under the same key, because a waive does not bump the attempt.
// That is one dispatch and gets one span, the last close in log order
// winning — it is the close that describes how the dispatch actually ended
// (#71).
func waveTimings(events []bundle.Event) []WaveTiming {
	dispatched, committed := collectDispatches(events)
	spans := map[timingKey]WaveTiming{}
	for _, e := range events {
		if e.Type != evClosed {
			continue
		}
		k := timingKeyOf(e)
		d, ok := dispatched[k]
		if !ok {
			continue
		}
		wt := WaveTiming{Wave: k.wave, Slice: k.slice, Attempt: k.attempt, DispatchedAt: d, ClosedAt: e.TS}
		if at, done := committed[k]; done {
			wt.Committed, wt.CommittedAt = true, at
		}
		spans[k] = wt
	}
	out := make([]WaveTiming, 0, len(spans))
	for _, wt := range spans {
		out = append(out, wt)
	}
	// The keys are unique, so ordering on them is total and the map's own
	// random iteration order cannot reach the output.
	slices.SortFunc(out, func(a, b WaveTiming) int {
		return cmp.Or(
			cmp.Compare(a.Wave, b.Wave), cmp.Compare(a.Slice, b.Slice), cmp.Compare(a.Attempt, b.Attempt))
	})
	return out
}

// collectDispatches indexes the dispatch and commit times by the dispatch
// they belong to, so waveTimings can walk the closes once and pair each one
// with events that may sit anywhere in the log.
func collectDispatches(events []bundle.Event) (map[timingKey]time.Time, map[timingKey]time.Time) {
	dispatched, committed := map[timingKey]time.Time{}, map[timingKey]time.Time{}
	for _, e := range events {
		switch e.Type {
		case evDispatched:
			dispatched[timingKeyOf(e)] = e.TS
		case evCommitted:
			committed[timingKeyOf(e)] = e.TS
		}
	}
	return dispatched, committed
}

// RetroInputsPath is where `next` writes the inputs for the run op.
func RetroInputsPath(bundleDir string) string {
	return filepath.Join(bundleDir, "finish", "retro-inputs.json")
}

// WriteRetroInputs writes them atomically.
func WriteRetroInputs(bundleDir string, in RetroInputs) error {
	return bundle.WriteJSONAtomic(RetroInputsPath(bundleDir), in)
}
