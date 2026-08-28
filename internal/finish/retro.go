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
	WaveTimings    []WaveTiming   `json:"wave_timings"`
	Verify         *VerifyRecord  `json:"verify,omitempty"`
	Goals          *GoalsRecord   `json:"goals,omitempty"`
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
	// Overlap is how many confirmed internal findings the backend's own
	// grading pass also raised — same file, within a few lines (a
	// heuristic: overlapLineTolerance).
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

// WaveTiming is dispatch → commit for the slice-attempt that committed.
type WaveTiming struct {
	Wave         int       `json:"wave"`
	Slice        int       `json:"slice"`
	Attempt      int       `json:"attempt"`
	DispatchedAt time.Time `json:"dispatched_at"`
	CommittedAt  time.Time `json:"committed_at"`
}

// The event types the wave timings are paired from, and the data keys that
// identify which dispatch each one belongs to.
const (
	evDispatched = "wave_dispatched"
	evCommitted  = "wave_committed"
	keyWave      = "wave"
	keySlice     = "slice"
	keyAttempt   = "attempt"
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
	for _, c := range closes {
		for _, tr := range c.Tasks {
			if tr.Review != nil {
				in.ReviewFindings += len(tr.Review.Findings)
			}
		}
	}
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
// grading pass also found: same file, within overlapLineTolerance lines.
func overlapCount(tr wave.TaskResult) int {
	if tr.Review == nil {
		return 0
	}
	n := 0
	for _, f := range tr.Internal {
		for _, b := range tr.Review.Findings {
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

// waveTimings pairs each wave_committed with the wave_dispatched of the same
// dispatch — wave, slice and attempt. A wave that was retried therefore
// reports the attempt that actually landed, not the whole span of the wave,
// and a wave larger than max_parallel reports one span per slice: its slices
// all run at attempt 1, so wave and attempt alone would collapse them into
// one. An event written before slices were recorded carries no slice key and
// decodes to 0, which is floored to 1: that is the number takt heals a
// slice-less wave to, so a bundle that upgraded mid-run — an old
// wave_dispatched answered by a healed wave_committed that says slice 1 —
// still pairs, and two old events still pair with each other.
func waveTimings(events []bundle.Event) []WaveTiming {
	type key struct{ w, s, a int }
	dispatched := map[key]time.Time{}
	out := []WaveTiming{}
	for _, e := range events {
		w, _ := e.Data[keyWave].(float64)
		sl, _ := e.Data[keySlice].(float64)
		a, _ := e.Data[keyAttempt].(float64)
		k := key{int(w), max(int(sl), 1), int(a)}
		switch e.Type {
		case evDispatched:
			dispatched[k] = e.TS
		case evCommitted:
			if d, ok := dispatched[k]; ok {
				out = append(out,
					WaveTiming{Wave: k.w, Slice: k.s, Attempt: k.a, DispatchedAt: d, CommittedAt: e.TS})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b WaveTiming) int { return cmp.Compare(a.Wave, b.Wave) })
	return out
}

// RetroInputsPath is where `next` writes the inputs for the run op.
func RetroInputsPath(bundleDir string) string {
	return filepath.Join(bundleDir, "finish", "retro-inputs.json")
}

// WriteRetroInputs writes them atomically.
func WriteRetroInputs(bundleDir string, in RetroInputs) error {
	return bundle.WriteJSONAtomic(RetroInputsPath(bundleDir), in)
}
