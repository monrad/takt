package finish_test

import (
	"encoding/json"
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
	// closed is the wave_closed each attempt writes, carrying the findings
	// its own task reviews raised — what BuildRetroInputs counts.
	closed := func(after time.Duration, w, sl, a, reviewFindings int) bundle.Event {
		e := ev(after, "wave_closed", w, sl, a)
		e.Data["review_findings"] = float64(reviewFindings)
		return e
	}
	// Wave 0 was retried (slice 1 lands at attempt 2), then went out a second
	// slice — which runs at attempt 1 again, so wave+attempt alone would
	// collapse it into slice 1's timing. Attempt 1 of slice 1 never closed
	// here, so it contributes no timing.
	events := []bundle.Event{
		ev(0, "wave_dispatched", 0, 1, 1),
		{TS: t0.Add(3 * time.Minute), Type: "gate_reviewed", Data: map[string]any{
			"gate": "spec", "verdict": "approve", "findings": float64(1),
		}},
		ev(5*time.Minute, "wave_dispatched", 0, 1, 2),
		ev(9*time.Minute, "wave_committed", 0, 1, 2),
		closed(9*time.Minute, 0, 1, 2, 2),
		ev(10*time.Minute, "wave_dispatched", 0, 2, 1),
		ev(13*time.Minute, "wave_committed", 0, 2, 1),
		closed(13*time.Minute, 0, 2, 1, 0),
		ev(14*time.Minute, "wave_dispatched", 1, 1, 1),
		ev(16*time.Minute, "wave_committed", 1, 1, 1),
		closed(16*time.Minute, 1, 1, 1, 0),
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
	if in.Tasks != 3 || in.Waves != 2 {
		t.Fatalf("%+v", in)
	}
	// The counts come from the events, not from the two findings the close
	// record on disk still holds: 1 gate finding + 2 on the wave_closed.
	if in.GateReviewFindings != 1 || in.TaskReviewFindings != 2 || in.ReviewFindings != 3 {
		t.Fatalf("findings counts = %+v", in)
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
// takes them as data the same way it already takes events and closes. The
// fixture is deliberately minimal: everything else is nil, so nothing but
// the pass-through can make this test pass or fail (#45).
func TestBuildRetroInputsCarriesFollowUps(t *testing.T) {
	t.Parallel()
	fu := []gate.FollowUp{
		{Gate: "spec", Severity: "minor", Title: "wording", Source: gate.SourceApprove},
	}
	in := finish.BuildRetroInputs(&bundle.State{}, plan.Index{}, nil, nil, nil, nil, fu, nil)
	if len(in.FollowUps) != 1 {
		t.Fatalf("want 1 follow-up, got %d", len(in.FollowUps))
	}
	if got := in.FollowUps[0]; got.Gate != "spec" || got.Severity != "minor" ||
		got.Title != "wording" || got.Source != gate.SourceApprove {
		t.Fatalf("follow-up lost detail: %+v", got)
	}
}

// TestWaveTimingsPairAcrossTheSliceUpgrade covers a bundle that straddles
// the upgrade to per-slice records. Its wave_dispatched events were written
// by a build that had no slice to record; the events that answer one of them
// were written by this build, which heals a slice-less wave to slice 1 and
// says so. Keyed on the raw numbers those never pair, and the retro of the
// run that upgraded mid-flight loses the spans it is mostly there to report.
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
		healed(4*time.Minute, "wave_closed", 0, 1, 1),
		legacy(5*time.Minute, "wave_dispatched", 1, 1),
		legacy(9*time.Minute, "wave_committed", 1, 1),
		legacy(9*time.Minute, "wave_closed", 1, 1),
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

// TestRetroInputsCountEveryReviewOnce pins the source of the findings
// counts to the event log (#23). The run reviewed the spec gate twice — an
// errored pass that raised nothing, then an approving pass with two
// findings — and closed wave 0 slice 1 twice: attempt 1 was reworked, so its
// close record has since been deleted, and only attempt 2's record is on
// disk. Summing the records would lose the reworked attempt's review and
// count nothing at all for the gate.
func TestRetroInputsCountEveryReviewOnce(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	gateEv := func(after time.Duration, verdict string, findings int) bundle.Event {
		return bundle.Event{TS: t0.Add(after), Type: "gate_reviewed", Data: map[string]any{
			"gate": "spec", "verdict": verdict, "findings": float64(findings),
		}}
	}
	closedEv := func(after time.Duration, attempt int, committed bool, reviewFindings int) bundle.Event {
		return bundle.Event{TS: t0.Add(after), Type: "wave_closed", Data: map[string]any{
			"wave": float64(0), "slice": float64(1), "attempt": float64(attempt),
			"committed": committed, "review_findings": float64(reviewFindings),
		}}
	}
	events := []bundle.Event{
		gateEv(0, "error", 0),
		gateEv(time.Minute, "approve", 2),
		closedEv(5*time.Minute, 1, false, 1),
		closedEv(9*time.Minute, 2, true, 3),
	}
	// Only the attempt that landed still has a record: persistClose deletes
	// the retired one.
	closes := []wave.CloseResult{{Wave: 0, Slice: 1, Attempt: 2, ReviewFindings: 3,
		Tasks: []wave.TaskResult{{Task: 1, Status: "done", Review: &backend.ReviewResult{
			Verdict: "approve", Findings: []backend.Finding{
				{Severity: "minor", File: "a.go", Line: 1, Title: "one"},
				{Severity: "minor", File: "a.go", Line: 2, Title: "two"},
				{Severity: "nit", File: "b.go", Line: 3, Title: "three"},
			}}}}}}
	in := finish.BuildRetroInputs(&bundle.State{}, plan.Index{}, events, closes, nil, nil, nil, nil)
	if in.GateReviewFindings != 2 {
		t.Fatalf("gate_review_findings = %d, want 2 (the errored pass raised none)", in.GateReviewFindings)
	}
	if in.TaskReviewFindings != 4 {
		t.Fatalf("task_review_findings = %d, want 4 (1 from the reworked attempt + 3 from the one that landed)",
			in.TaskReviewFindings)
	}
	if in.ReviewFindings != 6 {
		t.Fatalf("review_findings = %d, want 6 — the sum of the two", in.ReviewFindings)
	}
}

// TestWaveTimingsIncludeAnAttemptThatClosedWithoutCommitting covers #25: a
// reworked attempt closed, so it has a span, and it did not commit, so it
// says so and its committed_at key is absent from the JSON rather than
// written as a year-1 timestamp.
func TestWaveTimingsIncludeAnAttemptThatClosedWithoutCommitting(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	ev := func(after time.Duration, typ string, w, sl, a int) bundle.Event {
		return bundle.Event{
			TS: t0.Add(after), Type: typ,
			Data: map[string]any{"wave": float64(w), "slice": float64(sl), "attempt": float64(a)},
		}
	}
	events := []bundle.Event{
		ev(0, "wave_dispatched", 0, 1, 1),
		ev(5*time.Minute, "wave_closed", 0, 1, 1),
		ev(6*time.Minute, "wave_dispatched", 0, 1, 2),
		ev(9*time.Minute, "wave_closed", 0, 1, 2),
		ev(9*time.Minute, "wave_committed", 0, 1, 2),
	}
	in := finish.BuildRetroInputs(&bundle.State{}, plan.Index{}, events, nil, nil, nil, nil, nil)
	if len(in.WaveTimings) != 2 {
		t.Fatalf("both attempts must report a span: %+v", in.WaveTimings)
	}
	first, second := in.WaveTimings[0], in.WaveTimings[1]
	if first.Attempt != 1 || second.Attempt != 2 {
		t.Fatalf("spans out of order: %+v", in.WaveTimings)
	}
	if !first.ClosedAt.Equal(t0.Add(5*time.Minute)) || first.Committed || !first.CommittedAt.IsZero() {
		t.Fatalf("attempt 1 = %+v, want closed at t0+5m and uncommitted", first)
	}
	b, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["committed_at"]; ok {
		t.Fatalf("an attempt that did not commit must have no committed_at: %s", b)
	}
	if !second.Committed || !second.CommittedAt.Equal(t0.Add(9*time.Minute)) {
		t.Fatalf("attempt 2 = %+v, want committed at t0+9m", second)
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
	// The blind pass approved with no findings; the scoped pass that
	// followed found a.go:5, near the confirmed a.go:4 internal finding —
	// but the scoped pass graded the very claim being measured, so its
	// agreement must not count as overlap (two-layers design §9).
	if ir.Overlap != 0 {
		t.Fatalf("overlap = %d, want 0 (the blind pass raised nothing)", ir.Overlap)
	}
}

// TestRetroInputsOverlapCountsTheBlindPassOwnFinding covers the other
// direction TestRetroInputsInstrumentTheInternalReview does not: a blind
// pass that itself raised a finding near a confirmed internal one counts as
// overlap, even though a scoped pass followed it and landed a finding
// elsewhere. Together the two tests pin overlapCount to tr.BlindReview, not
// tr.Review, whenever a scoped pass ran (two-layers design §9).
func TestRetroInputsOverlapCountsTheBlindPassOwnFinding(t *testing.T) {
	t.Parallel()
	st := &bundle.State{Slug: "demo", Topic: "Add a greeting"}
	idx := plan.Index{}
	internals := []wave.InternalRecord{{
		Wave: 0, Slice: 1, Attempt: 1, Lenses: []string{"correctness"},
		Candidates: []wave.Candidate{
			{
				ID:      "c1",
				Finding: backend.Finding{Severity: "blocking", File: "a.go", Line: 4, Title: "x"},
				Task:    3,
				Lenses:  []string{"correctness"},
			},
		},
		Confirmed: []string{"c1"},
	}}
	closes := []wave.CloseResult{{Wave: 0, Slice: 1, Attempt: 1, Tasks: []wave.TaskResult{
		{
			Task:   3,
			Status: "done",
			// Within 3 lines of the confirmed a.go:4 finding.
			BlindReview: &backend.ReviewResult{Verdict: "approve",
				Findings: []backend.Finding{{Severity: "major", File: "a.go", Line: 6, Title: "nearby"}}},
			// The scoped pass's own finding is nowhere near a.go:4 — if
			// overlapCount read tr.Review instead of tr.BlindReview, this
			// case would wrongly report 0.
			Review: &backend.ReviewResult{Verdict: "rework",
				Findings: []backend.Finding{{Severity: "minor", File: "b.go", Line: 1, Title: "unrelated"}}},
			Internal: []wave.InternalFinding{
				{
					Finding: backend.Finding{Severity: "blocking", File: "a.go", Line: 4, Title: "x"},
					Lenses:  []string{"correctness"},
				},
			},
		},
	}}}
	in := finish.BuildRetroInputs(st, idx, nil, closes, nil, nil, nil, internals)
	if in.Internal == nil {
		t.Fatal("no internal review block")
	}
	if in.Internal.Overlap != 1 {
		t.Fatalf("overlap = %d, want 1 (the blind pass's own a.go:6 is within tolerance of a.go:4)",
			in.Internal.Overlap)
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
