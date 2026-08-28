package finish_test

import (
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gate"
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
	in := finish.BuildRetroInputs(st, idx, events, closes, &finish.VerifyRecord{Passed: true}, nil, nil, nil)
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

// TestBuildRetroInputsCarriesFollowUps checks that follow-ups.json's items
// pass straight through to RetroInputs — BuildRetroInputs stays pure, so it
// takes them as data the same way it already takes events and closes.
func TestBuildRetroInputsCarriesFollowUps(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	st := &bundle.State{Slug: "demo", Topic: "Add a greeting", Tasks: []bundle.Task{
		{ID: 1, Wave: 0, Status: "done", Attempt: 2},
		{ID: 2, Wave: 0, Status: "waived", Attempt: 1},
		{ID: 3, Wave: 1, Status: "done", Attempt: 1},
	}}
	idx := plan.Index{Tasks: []plan.Task{{ID: 1}, {ID: 2}, {ID: 3}}}
	ev := func(after time.Duration, typ string, w, sl, a int) bundle.Event {
		return bundle.Event{
			TS: t0.Add(after), Type: typ,
			Data: map[string]any{"wave": float64(w), "slice": float64(sl), "attempt": float64(a)},
		}
	}
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
	fu := []gate.FollowUp{
		{Gate: "spec", Severity: "minor", Title: "wording", Source: gate.SourceApprove},
	}
	in := finish.BuildRetroInputs(st, idx, events, closes, &finish.VerifyRecord{Passed: true}, nil, fu, nil)
	if len(in.FollowUps) != 1 {
		t.Fatalf("want 1 follow-up, got %d", len(in.FollowUps))
	}
	if in.FollowUps[0].Severity != "minor" || in.FollowUps[0].Title != "wording" {
		t.Fatalf("follow-up lost detail: %+v", in.FollowUps[0])
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
	in := finish.BuildRetroInputs(&bundle.State{}, plan.Index{}, events, nil, nil, nil, nil, nil)
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

// TestRetroInputsInstrumentTheInternalReview covers the retro's internal
// review block (two-layers design §9): candidates vs confirmed, by-lens
// tallies, the scoped-pass verdict-change count, and the overlap between a
// confirmed internal finding and the backend's own grading pass.
func TestRetroInputsInstrumentTheInternalReview(t *testing.T) {
	t.Parallel()
	st := &bundle.State{Slug: "demo", Topic: "Add a greeting"}
	idx := plan.Index{}
	internals := []wave.InternalRecord{{
		Wave: 0, Slice: 1, Attempt: 1, Lenses: []string{"correctness", "intent"},
		Candidates: []wave.Candidate{
			{
				ID:      "c1",
				Finding: backend.Finding{Severity: "blocking", File: "a.go", Line: 4, Title: "x"},
				Task:    3,
				Lenses:  []string{"correctness"},
			},
			{
				ID:      "c2",
				Finding: backend.Finding{Severity: "minor", File: "z.go", Line: 9, Title: "y"},
				Task:    0,
				Lenses:  []string{"intent"},
			},
			{
				ID:      "c3",
				Finding: backend.Finding{Severity: "nit", File: "b.go", Line: 1, Title: "z"},
				Task:    3,
				Lenses:  []string{"intent"},
			},
		},
		Confirmed: []string{"c1", "c2"},
	}}
	closes := []wave.CloseResult{{Wave: 0, Slice: 1, Attempt: 1, Tasks: []wave.TaskResult{
		{
			Task:        3,
			Status:      "done",
			BlindReview: &backend.ReviewResult{Verdict: "approve"},
			Review: &backend.ReviewResult{Verdict: "rework",
				Findings: []backend.Finding{{Severity: "blocking", File: "a.go", Line: 5, Title: "near x"}}},
			Internal: []wave.InternalFinding{
				{
					Finding: backend.Finding{Severity: "blocking", File: "a.go", Line: 4, Title: "x"},
					Lenses:  []string{"correctness"},
				},
			},
		},
	}}}
	in := finish.BuildRetroInputs(st, idx, nil, closes, nil, nil, nil, internals)
	ir := in.Internal
	if ir == nil {
		t.Fatal("no internal review block")
	}
	if ir.Candidates != 3 || ir.Confirmed != 2 || ir.FalsePositives != 1 || ir.Unattributed != 1 {
		t.Fatalf("counts = %+v", ir)
	}
	if ir.ByLens["correctness"].Reported != 1 || ir.ByLens["correctness"].Confirmed != 1 ||
		ir.ByLens["intent"].Reported != 2 || ir.ByLens["intent"].Confirmed != 1 {
		t.Fatalf("by_lens = %+v", ir.ByLens)
	}
	if ir.ScopedPasses != 1 || ir.ScopedChanged != 1 {
		t.Fatalf("scoped = %+v", ir)
	}
	if ir.Overlap != 1 { // a.go:4 internal vs a.go:5 backend — within 3 lines
		t.Fatalf("overlap = %d", ir.Overlap)
	}
}

// TestRetroInputsInternalReviewNilWithNoRecords checks that a run with no
// internal review recorded gets no internal_review block at all, rather than
// an all-zero one — nil is what run-retro.md's "carries internal_review"
// gate checks.
func TestRetroInputsInternalReviewNilWithNoRecords(t *testing.T) {
	t.Parallel()
	st := &bundle.State{Slug: "demo", Topic: "Add a greeting"}
	in := finish.BuildRetroInputs(st, plan.Index{}, nil, nil, nil, nil, nil, nil)
	if in.Internal != nil {
		t.Fatalf("want nil internal review, got %+v", in.Internal)
	}
}

// TestRetroInputsCountsSkippedInternalReviews checks that Skipped tallies
// internal_review_skipped events across the run, and only when there is an
// internal review block to attach the count to.
func TestRetroInputsCountsSkippedInternalReviews(t *testing.T) {
	t.Parallel()
	st := &bundle.State{Slug: "demo", Topic: "Add a greeting"}
	internals := []wave.InternalRecord{{Wave: 0, Slice: 1, Attempt: 1}}
	events := []bundle.Event{
		{
			Type: "internal_review_skipped",
			Data: map[string]any{"wave": float64(1), "slice": float64(1), "attempt": float64(1)},
		},
		{
			Type: "internal_review_skipped",
			Data: map[string]any{"wave": float64(2), "slice": float64(1), "attempt": float64(1)},
		},
		{Type: "wave_committed"},
	}
	in := finish.BuildRetroInputs(st, plan.Index{}, events, nil, nil, nil, nil, internals)
	if in.Internal == nil || in.Internal.Skipped != 2 {
		t.Fatalf("skipped = %+v", in.Internal)
	}
}
