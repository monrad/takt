package finish

import (
	"cmp"
	"path/filepath"
	"slices"
	"time"

	"github.com/monrad/takt/internal/bundle"
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
	closes []wave.CloseResult, v *VerifyRecord, g *GoalsRecord,
) RetroInputs {
	in := RetroInputs{
		Slug: st.Slug, Topic: st.Topic, Tasks: len(idx.Tasks),
		Retries: []RetroRetry{}, Failures: []RetroFailure{}, WaveTimings: []WaveTiming{},
		Verify: v, Goals: g,
	}
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
	return in
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
// one. Events written before slices were recorded carry no slice key and
// decode to 0, which still pairs them with each other.
func waveTimings(events []bundle.Event) []WaveTiming {
	type key struct{ w, s, a int }
	dispatched := map[key]time.Time{}
	out := []WaveTiming{}
	for _, e := range events {
		w, _ := e.Data[keyWave].(float64)
		sl, _ := e.Data[keySlice].(float64)
		a, _ := e.Data[keyAttempt].(float64)
		k := key{int(w), int(sl), int(a)}
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
	return writeJSONAtomic(RetroInputsPath(bundleDir), in)
}
