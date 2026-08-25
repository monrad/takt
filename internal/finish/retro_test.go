package finish_test

import (
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

func TestBuildRetroInputs(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	st := &bundle.State{Slug: "demo", Topic: "Add a greeting", Tasks: []bundle.Task{
		{ID: 1, Wave: 0, Status: "done", Attempt: 2},
		{ID: 2, Wave: 0, Status: "waived", Attempt: 1},
		{ID: 3, Wave: 1, Status: "done", Attempt: 1},
	}}
	idx := plan.Index{Tasks: []plan.Task{{ID: 1}, {ID: 2}, {ID: 3}}}
	// wave/slice/attempt arrive from events.jsonl as JSON numbers, so the
	// fixture spells them float64 exactly as bundle.ReadEvents would hand
	// them over.
	ev := func(after time.Duration, typ string, w, sl, a int) bundle.Event {
		return bundle.Event{
			TS: t0.Add(after), Type: typ,
			Data: map[string]any{"wave": float64(w), "slice": float64(sl), "attempt": float64(a)},
		}
	}
	// Wave 0 was retried (slice 1 lands at attempt 2), then went out a second
	// slice — which runs at attempt 1 again, so wave+attempt alone would
	// collapse it into slice 1's timing.
	events := []bundle.Event{
		ev(0, "wave_dispatched", 0, 1, 1),
		ev(5*time.Minute, "wave_dispatched", 0, 1, 2),
		ev(9*time.Minute, "wave_committed", 0, 1, 2),
		ev(10*time.Minute, "wave_dispatched", 0, 2, 1),
		ev(13*time.Minute, "wave_committed", 0, 2, 1),
		ev(14*time.Minute, "wave_dispatched", 1, 1, 1),
		ev(16*time.Minute, "wave_committed", 1, 1, 1),
	}
	findings := []backend.Finding{
		{Severity: "major", File: "a.go", Line: 12, Title: "unchecked error", Detail: "err is dropped"},
		{Severity: "nit", File: "b.go", Line: 3, Title: "stale comment", Detail: "says wave 0"},
	}
	closes := []wave.CloseResult{
		{Wave: 0, Attempt: 2, Tasks: []wave.TaskResult{
			{Task: 1, Status: "done", Review: &backend.ReviewResult{Verdict: "approve", Findings: findings}},
			{Task: 2, Status: "blocked", Reason: "needs schema"},
		}},
		{Wave: 1, Attempt: 1, Tasks: []wave.TaskResult{{Task: 3, Status: "done"}}},
	}
	in := finish.BuildRetroInputs(st, idx, events, closes, &finish.VerifyRecord{Passed: true}, nil)
	if in.Tasks != 3 || in.Waves != 2 || in.ReviewFindings != len(findings) {
		t.Fatalf("%+v", in)
	}
	if len(in.Retries) != 1 || in.Retries[0].Task != 1 || in.Retries[0].Attempts != 2 {
		t.Fatalf("retries: %+v", in.Retries)
	}
	if len(in.Failures) != 1 || in.Failures[0].Task != 2 ||
		in.Failures[0].Status != "waived" || in.Failures[0].Reason != "needs schema" {
		t.Fatalf("failures: %+v", in.Failures)
	}
	// Three spans: wave 0 slice 1 (the attempt that landed), wave 0 slice 2,
	// wave 1. The two slices of wave 0 both ran at attempt 1 at some point,
	// so keying the pairing on the slice too is what keeps them apart.
	if len(in.WaveTimings) != 3 {
		t.Fatalf("timings: %+v", in.WaveTimings)
	}
	for i, want := range []finish.WaveTiming{
		{Wave: 0, Slice: 1, Attempt: 2}, {Wave: 0, Slice: 2, Attempt: 1}, {Wave: 1, Slice: 1, Attempt: 1},
	} {
		got := in.WaveTimings[i]
		if got.Wave != want.Wave || got.Slice != want.Slice || got.Attempt != want.Attempt {
			t.Fatalf("timing %d = %+v, want wave %d slice %d attempt %d",
				i, got, want.Wave, want.Slice, want.Attempt)
		}
	}
	if d := in.WaveTimings[0].CommittedAt.Sub(in.WaveTimings[0].DispatchedAt); d != 4*time.Minute {
		t.Fatalf("wave 0 slice 1 spans its landing attempt only, got %s", d)
	}
	if d := in.WaveTimings[1].CommittedAt.Sub(in.WaveTimings[1].DispatchedAt); d != 3*time.Minute {
		t.Fatalf("wave 0 slice 2 span = %s", d)
	}
	if in.Verify == nil || !in.Verify.Passed || in.Goals != nil {
		t.Fatalf("%+v", in)
	}
	if in.Slug != "demo" || in.Topic != "Add a greeting" {
		t.Fatalf("%+v", in)
	}
}

// TestWaveTimingsPairAcrossTheSliceUpgrade covers a bundle that straddles
// the upgrade to per-slice records. Its wave_dispatched events were written
// by a build that had no slice to record; the wave_committed that answers
// one of them was written by this build, which heals a slice-less wave to
// slice 1 and says so. Keyed on the raw numbers those two never pair, and
// the retro of the run that upgraded mid-flight loses the spans it is
// mostly there to report.
func TestWaveTimingsPairAcrossTheSliceUpgrade(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	legacy := func(after time.Duration, typ string, w, a int) bundle.Event {
		return bundle.Event{
			TS: t0.Add(after), Type: typ,
			Data: map[string]any{"wave": float64(w), "attempt": float64(a)},
		}
	}
	healed := func(after time.Duration, typ string, w, sl, a int) bundle.Event {
		return bundle.Event{
			TS: t0.Add(after), Type: typ,
			Data: map[string]any{"wave": float64(w), "slice": float64(sl), "attempt": float64(a)},
		}
	}
	events := []bundle.Event{
		legacy(0, "wave_dispatched", 0, 1),
		healed(4*time.Minute, "wave_committed", 0, 1, 1),
		legacy(5*time.Minute, "wave_dispatched", 1, 1),
		legacy(9*time.Minute, "wave_committed", 1, 1),
	}
	in := finish.BuildRetroInputs(&bundle.State{}, plan.Index{}, events, nil, nil, nil)
	if len(in.WaveTimings) != 2 {
		t.Fatalf("both spans must pair: %+v", in.WaveTimings)
	}
	for i, want := range []finish.WaveTiming{{Wave: 0, Slice: 1, Attempt: 1}, {Wave: 1, Slice: 1, Attempt: 1}} {
		got := in.WaveTimings[i]
		if got.Wave != want.Wave || got.Slice != want.Slice || got.Attempt != want.Attempt {
			t.Fatalf("timing %d = %+v, want wave %d slice %d attempt %d", i, got, want.Wave, want.Slice, want.Attempt)
		}
		if d := got.CommittedAt.Sub(got.DispatchedAt); d != 4*time.Minute {
			t.Fatalf("timing %d spans %s", i, d)
		}
	}
}
