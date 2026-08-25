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
	// wave/attempt arrive from events.jsonl as JSON numbers, so the fixture
	// spells them float64 exactly as bundle.ReadEvents would hand them over.
	ev := func(after time.Duration, typ string, w, a int) bundle.Event {
		return bundle.Event{
			TS: t0.Add(after), Type: typ,
			Data: map[string]any{"wave": float64(w), "attempt": float64(a)},
		}
	}
	events := []bundle.Event{
		ev(0, "wave_dispatched", 0, 1),
		ev(5*time.Minute, "wave_dispatched", 0, 2),
		ev(9*time.Minute, "wave_committed", 0, 2),
		ev(10*time.Minute, "wave_dispatched", 1, 1),
		ev(12*time.Minute, "wave_committed", 1, 1),
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
	if len(in.WaveTimings) != 2 || in.WaveTimings[0].Attempt != 2 ||
		in.WaveTimings[0].CommittedAt.Sub(in.WaveTimings[0].DispatchedAt) != 4*time.Minute {
		t.Fatalf("timings: %+v", in.WaveTimings)
	}
	if in.Verify == nil || !in.Verify.Passed || in.Goals != nil {
		t.Fatalf("%+v", in)
	}
	if in.Slug != "demo" || in.Topic != "Add a greeting" {
		t.Fatalf("%+v", in)
	}
}
