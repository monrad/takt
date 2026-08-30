package finish_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/spec"
)

// The wave and task numbers the fixtures carry, named as internal/gate's
// follow-up tests name theirs, so a table of follow-ups reads as data rather
// than as a row of bare integers.
const (
	waveZero   = 0
	waveOne    = 1
	waveTwo    = 2
	taskOne    = 1
	taskFour   = 4
	sliceOne   = 1
	sliceTwo   = 2
	attemptOne = 1
	attemptTwo = 2
)

// doc joins an expected document's lines. The skeleton is compared byte for
// byte — it is what the session copies to retro.md — and a []string of lines
// is the one shape that can hold a markdown fence without fighting Go's
// string literals.
func doc(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

// t0 anchors every timestamp in the fixtures, so the Numbers block is a
// constant the goldens can spell out.
var t0 = time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)

// committed is a wave_committed event as bundle.ReadEvents hands it over:
// every number decoded from events.jsonl is a float64.
func committed(w, sl, a int, sha string, ids ...int) bundle.Event {
	raw := make([]any, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, float64(id))
	}
	return bundle.Event{
		TS: t0, Type: "wave_committed",
		Data: map[string]any{
			"wave": float64(w), "slice": float64(sl), "attempt": float64(a),
			"sha": sha, "tasks": raw,
		},
	}
}

// fullRunEvents is the log of a run that used every shape the table has to
// carry: a wave that committed two slices, a wave that committed, was
// reworked and committed again, and a commit whose event was backfilled by a
// healed finish. Task 6 is not in the index the fixture builds.
func fullRunEvents() []bundle.Event {
	backfilled := committed(waveTwo, sliceOne, attemptOne, "eee5555", 6)
	backfilled.Data["backfilled"] = true
	return []bundle.Event{
		committed(waveZero, sliceOne, attemptTwo, "aaa1111", 1, 2),
		committed(waveOne, sliceOne, attemptOne, "ccc3333", 4),
		{TS: t0, Type: "gate_answered", Data: map[string]any{
			"gate": "spec", "choice": "approve", "reason": "the lock is the only way to snapshot the pair",
		}},
		{TS: t0, Type: "gate_answered", Data: map[string]any{"gate": "gate_review", "choice": "revise"}},
		committed(waveZero, sliceTwo, attemptOne, "bbb2222", 3),
		{TS: t0, Type: "task_waived", Data: map[string]any{
			"task": float64(5), "reason": "the lint directive its fix needs is rejected by the config",
		}},
		committed(waveOne, sliceOne, attemptTwo, "ddd4444", 4),
		{TS: t0, Type: "goal_waived", Data: map[string]any{"goal": "G4", "reason": "docs land with the next run"}},
		backfilled,
	}
}

// fullRunIndex knows every committed task but 6, whose row must still render
// — as the bare id (G2).
func fullRunIndex() plan.Index {
	return plan.Index{Tasks: []plan.Task{
		{ID: 1, Title: "Add the flag"},
		{ID: 2, Title: "Wire it up"},
		{ID: 3, Title: "Document the flag"},
		{ID: 4, Title: "Rename the field"},
		{ID: 5, Title: "Backfill the old rows"},
	}}
}

// fullRunFollowUps carries every severity and both locators the section can
// render: a gate follow-up, a wave-and-task one, and a wave-only one, whose
// "wave 2" with no task is the case the omission rule exists for.
func fullRunFollowUps() []gate.FollowUp {
	return []gate.FollowUp{
		{Gate: "spec", Severity: "blocking", Title: "the spec omits the lock",
			Detail: "two files are replaced in sequence", Source: gate.SourceOverride},
		{Severity: "minor", Wave: new(waveZero), Task: taskOne, Title: "a stale comment"},
		{Severity: "blocking", Wave: new(waveTwo), Title: "the backfilled commit has no review",
			Detail: "the heal wrote the event, not a record"},
		{Severity: "major", Wave: new(waveOne), Task: taskFour, Title: "the rename has no test",
			Detail: "no caller exercises the new name"},
		{Severity: "nit", Wave: new(waveOne), Title: "spelling"},
		{Severity: "minor", Gate: "plan", Title: "the plan repeats itself"},
	}
}

// fullRun is the input of the full-run golden: everything the four rendered
// sections can carry at once.
func fullRun() (finish.RetroInputs, finish.SkeletonExtras) {
	in := finish.RetroInputs{
		Slug: "demo", Topic: "Add a greeting", Tasks: 6, Waves: 3,
		Failures: []finish.RetroFailure{
			{Task: 5, Status: "waived", Reason: "the lint directive its fix needs is rejected by the config"},
			{Task: 6, Status: "blocked", Reason: "needs a schema change"},
		},
		WaveTimings: []finish.WaveTiming{{
			Wave: waveZero, Slice: sliceOne, Attempt: attemptTwo,
			DispatchedAt: t0, ClosedAt: t0.Add(9 * time.Minute),
			Committed: true, CommittedAt: t0.Add(9 * time.Minute),
		}},
		Verify:    &finish.VerifyRecord{SHA: "ddd4444", Overridden: "the flake is upstream"},
		Goals:     &finish.GoalsRecord{SHA: "ddd4444", Waived: map[string]string{"G4": "docs land with the next run"}},
		FollowUps: fullRunFollowUps(),
		Internal: &finish.InternalReview{
			Candidates: 4, Confirmed: 3, FalsePositives: 1, Unattributed: 1,
			ByLens:       map[string]finish.LensStats{"correctness": {Reported: 3, Confirmed: 2}},
			ScopedPasses: 2, ScopedChanged: 1, Overlap: 1, Skipped: 1,
		},
	}
	st := &bundle.State{Disposition: &bundle.Disposition{
		Choice: "pr", At: t0, Reason: "the branch wants a second reader",
	}}
	assumptions := []spec.Assumption{
		{Question: "Who writes the retro?", Decision: "The driving session",
			Rationale: "the observations exist only there", Source: "user-confirmed"},
		{Question: "Does the rewrite take the lock?", Decision: "Yes",
			Rationale: "per-file atomicity gives no two-file snapshot", Source: "assumed"},
	}
	ex := finish.SkeletonExtras{
		Shipped:   finish.BuildShipped(fullRunEvents(), fullRunIndex()),
		Decisions: finish.BuildDecisions(fullRunEvents(), st, assumptions),
	}
	return in, ex
}

func fullRunIn() finish.RetroInputs    { in, _ := fullRun(); return in }
func fullRunEx() finish.SkeletonExtras { _, ex := fullRun(); return ex }

// TestRenderSkeletonGolden pins the whole document: the skeleton is copied
// to retro.md verbatim, so its bytes are the contract with the template's
// seven headings and with the session that fills the prose slots.
func TestRenderSkeletonGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   finish.RetroInputs
		ex   finish.SkeletonExtras
		want string
	}{
		{name: "full run", in: fullRunIn(), ex: fullRunEx(), want: fullRunDoc()},
		{name: "empty run", in: finish.RetroInputs{Slug: "empty"}, want: emptyRunDoc()},
		{name: "minors only", in: minorsOnlyIn(), want: minorsOnlyDoc()},
		{name: "no internal_review", in: noInternalIn(), want: noInternalDoc()},
		{name: "no wave_timings", in: noTimingsIn(), want: noTimingsDoc()},
		{name: "skipped verification", in: skippedVerifyIn(), ex: skippedVerifyEx(), want: skippedVerifyDoc()},
		{name: "unruly free text", in: unrulyIn(), ex: unrulyEx(), want: unrulyDoc()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := finish.RenderSkeleton(tc.in, tc.ex)
			if got != tc.want {
				t.Fatalf("skeleton mismatch\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// TestRenderSkeletonIsPure checks the property the whole design rests on: a
// replayed `next` rewrites the same bytes, so re-emitting the retro op costs
// nothing (design §5.4).
func TestRenderSkeletonIsPure(t *testing.T) {
	t.Parallel()
	in, ex := fullRun()
	first := finish.RenderSkeleton(in, ex)
	second := finish.RenderSkeleton(in, ex)
	if first != second {
		t.Fatalf("RenderSkeleton is not pure:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestBuildShippedResolvesTitlesAndKeepsUnknownIdsBare checks G2: the titles
// are resolved here, once, and an id the index does not know still ships as
// a row — with its id alone rather than not at all.
func TestBuildShippedResolvesTitlesAndKeepsUnknownIdsBare(t *testing.T) {
	t.Parallel()
	rows := finish.BuildShipped(fullRunEvents(), fullRunIndex())
	if len(rows) != 5 {
		t.Fatalf("one row per wave_committed event, got %d: %+v", len(rows), rows)
	}
	// Sorted by wave, then slice, then attempt, whatever order the log had.
	want := []struct{ w, sl, a int }{{0, 1, 2}, {0, 2, 1}, {1, 1, 1}, {1, 1, 2}, {2, 1, 1}}
	for i, w := range want {
		got := rows[i]
		if got.Wave != w.w || got.Slice != w.sl || got.Attempt != w.a {
			t.Fatalf("row %d = %+v, want wave %d slice %d attempt %d", i, got, w.w, w.sl, w.a)
		}
	}
	if rows[0].SHA != "aaa1111" || len(rows[0].Tasks) != 2 || rows[0].Tasks[1] != (finish.ShippedTask{
		ID: 2, Title: "Wire it up",
	}) {
		t.Fatalf("titles must be resolved from the index: %+v", rows[0])
	}
	// The backfilled commit carries task 6, which the index does not know.
	if rows[4].Tasks[0] != (finish.ShippedTask{ID: 6}) {
		t.Fatalf("an id the index does not know keeps an empty title: %+v", rows[4])
	}
	if !strings.Contains(finish.RenderSkeleton(fullRunIn(), fullRunEx()), "| 2 | 1 | 1 | 6 | eee5555 |") {
		t.Fatal("an unknown id renders as the bare id")
	}
}

// TestBuildShippedFloorsASliceLessCommitToOne covers the events a bundle
// written before slices existed still holds: no slice key at all, or one
// recorded as 0. Both name the slice takt heals such a wave to, so they sort
// and render beside the events written after the upgrade rather than under a
// slice 0 that never ran.
func TestBuildShippedFloorsASliceLessCommitToOne(t *testing.T) {
	t.Parallel()
	sliceless := bundle.Event{TS: t0, Type: "wave_committed", Data: map[string]any{
		"wave": float64(1), "attempt": float64(2), "sha": "old2222", "tasks": []any{float64(2)},
	}}
	zero := committed(waveOne, 0, attemptOne, "old1111", 1)
	rows := finish.BuildShipped([]bundle.Event{sliceless, zero}, fullRunIndex())
	if len(rows) != 2 {
		t.Fatalf("both commits ship: %+v", rows)
	}
	for i, r := range rows {
		if r.Slice != sliceOne {
			t.Fatalf("row %d = %+v, want the slice floored to 1", i, r)
		}
	}
	// Floored alike, they sort by attempt, and the column of ones is hidden.
	if rows[0].SHA != "old1111" || rows[1].SHA != "old2222" {
		t.Fatalf("rows must sort by attempt once the slice is floored: %+v", rows)
	}
	got := finish.RenderSkeleton(finish.RetroInputs{Slug: "legacy"}, finish.SkeletonExtras{Shipped: rows})
	if !strings.Contains(got, "| wave | attempt | tasks | commit |") {
		t.Fatalf("a floored slice is not a second slice:\n%s", got)
	}
}

// TestBuildDecisionsSourcesAndOmissions checks G6: one decision per source,
// and the omissions that keep the section a list of choices rather than of
// events.
func TestBuildDecisionsSourcesAndOmissions(t *testing.T) {
	t.Parallel()
	st := &bundle.State{Disposition: &bundle.Disposition{Choice: "pr", Reason: "wants a second reader"}}
	as := []spec.Assumption{
		{Question: "Who writes the retro?", Decision: "The driving session",
			Rationale: "the observations exist only there", Source: "user-confirmed"},
		{Question: "Does the rewrite take the lock?", Decision: "Yes",
			Rationale: "no two-file snapshot otherwise", Source: "assumed"},
	}
	events := []bundle.Event{
		{Type: "gate_answered", Data: map[string]any{"gate": "spec", "choice": "approve", "reason": "scope is right"}},
		// A reasonless answer, and one written before the field existed:
		// the same rule drops both.
		{Type: "gate_answered", Data: map[string]any{"gate": "gate_review", "choice": "revise", "reason": ""}},
		{Type: "gate_answered", Data: map[string]any{"gate": "plan", "choice": "approve"}},
		{Type: "task_waived", Data: map[string]any{"task": float64(5), "reason": "the fix needs a rejected directive"}},
		{Type: "goal_waived", Data: map[string]any{"goal": "G4", "reason": "docs land next run"}},
	}
	got := finish.BuildDecisions(events, st, as)
	want := []finish.Decision{
		{Kind: finish.DecisionGate, Subject: "spec", Choice: "approve", Reason: "scope is right"},
		{Kind: finish.DecisionTaskWaiver, Subject: "task 5", Reason: "the fix needs a rejected directive"},
		{Kind: finish.DecisionGoalWaiver, Subject: "G4", Reason: "docs land next run"},
		{Kind: finish.DecisionDisposition, Choice: "pr", Reason: "wants a second reader"},
		{Kind: finish.DecisionSpecAssumption, Subject: "Who writes the retro?", Choice: "The driving session",
			Reason: "the observations exist only there"},
	}
	if len(got) != len(want) {
		t.Fatalf("decisions = %+v, want %d of them", got, len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("decision %d = %+v, want %+v", i, got[i], w)
		}
	}
	assertNoDisposition(t, finish.BuildDecisions(events, &bundle.State{}, as), "a nil disposition")
	assertNoDisposition(t, finish.BuildDecisions(events, nil, as), "a nil state")
	nilDisp := finish.BuildDecisions(events, nil, as)
	if len(nilDisp) != len(want)-1 {
		t.Fatalf("a nil state drops the disposition and nothing else: %+v", nilDisp)
	}
	first := finish.RenderSkeleton(finish.RetroInputs{Slug: "demo"}, finish.SkeletonExtras{Decisions: nilDisp})
	if !strings.Contains(first, "\ndisposition: not yet chosen\n") {
		t.Fatalf("the first pass must say the disposition is unanswered:\n%s", first)
	}
	after := finish.RenderSkeleton(finish.RetroInputs{Slug: "demo"}, finish.SkeletonExtras{Decisions: got})
	const chosen = "- disposition: pr (wants a second reader)"
	if strings.Contains(after, "not yet chosen") || !strings.Contains(after, chosen) {
		t.Fatalf("a chosen disposition must render its choice:\n%s", after)
	}
}

// assertNoDisposition fails when a disposition reached the decisions: neither
// a state that chose none nor no state at all is a choice a reader can read.
func assertNoDisposition(t *testing.T, ds []finish.Decision, what string) {
	t.Helper()
	for _, d := range ds {
		if d.Kind == finish.DecisionDisposition {
			t.Fatalf("%s must produce no disposition decision: %+v", what, ds)
		}
	}
}

// TestRenderSkeletonPlaceholderKeepsSourceOrder pins where the unanswered
// disposition sits: the place a chosen one would take, after the waivers and
// before the spec's assumptions, so filling it in later moves nothing. It is
// not a bullet, so the bullets around it are held off by a blank line —
// otherwise markdown reads it as a continuation of the bullet above it.
func TestRenderSkeletonPlaceholderKeepsSourceOrder(t *testing.T) {
	t.Parallel()
	ds := []finish.Decision{
		{Kind: finish.DecisionGoalWaiver, Subject: "G4", Reason: "docs land next run"},
		{Kind: finish.DecisionSpecAssumption, Subject: "Who writes the retro?", Choice: "The session",
			Reason: "the observations exist only there"},
	}
	got := finish.RenderSkeleton(finish.RetroInputs{Slug: "ordered"}, finish.SkeletonExtras{Decisions: ds})
	ls := strings.Split(got, "\n")
	waiver := slices.IndexFunc(ls, func(l string) bool { return strings.HasPrefix(l, "- goal_waiver:") })
	pending := slices.Index(ls, "disposition: not yet chosen")
	assumption := slices.IndexFunc(ls, func(l string) bool { return strings.HasPrefix(l, "- spec_assumption:") })
	if waiver < 0 || pending < 0 || assumption < 0 || waiver >= pending || pending >= assumption {
		t.Fatalf("the placeholder sits where a chosen disposition would:\n%s", got)
	}
	if ls[pending-1] != "" || ls[pending+1] != "" {
		t.Fatalf("the placeholder is a paragraph of its own, not a bullet's second line:\n%s", got)
	}
}

// minorsOnlyIn is a run whose only follow-ups are summarised ones: the
// section must then be the count line alone. "praise" and "advice" are
// severities takt does not name, and are counted under their own names —
// after the two it does name and sorted between themselves, so the line is
// the same on every render — rather than dropped.
func minorsOnlyIn() finish.RetroInputs {
	return finish.RetroInputs{Slug: "minors", FollowUps: []gate.FollowUp{
		{Severity: "minor", Wave: new(waveZero), Task: taskOne, Title: "a stale comment"},
		{Severity: "praise", Gate: "plan", Title: "the plan reads well"},
		{Severity: "advice", Gate: "plan", Title: "the plan could name the lock"},
		{Severity: "minor", Gate: "plan", Title: "the plan repeats itself"},
	}}
}

// noInternalIn is a run that recorded timings but no internal review: the
// block is still fenced, with the half that is missing spelled out as null.
func noInternalIn() finish.RetroInputs {
	return finish.RetroInputs{Slug: "timings", WaveTimings: []finish.WaveTiming{{
		Wave: waveZero, Slice: sliceOne, Attempt: attemptOne,
		DispatchedAt: t0, ClosedAt: t0.Add(9 * time.Minute),
		Committed: true, CommittedAt: t0.Add(9 * time.Minute),
	}}}
}

// noTimingsIn is the other half: an internal review with no timing beside
// it. The empty list is rendered as one, not as null, which would read as a
// measurement the run never took.
func noTimingsIn() finish.RetroInputs {
	return finish.RetroInputs{Slug: "untimed", Internal: &finish.InternalReview{
		Candidates: 1, Confirmed: 1,
		ByLens: map[string]finish.LensStats{"tests": {Reported: 1, Confirmed: 1}},
	}}
}

// skippedVerifyIn is a run that ran no commands at all, whose disposition
// was answered without a reason: both are facts the section states rather
// than omits.
func skippedVerifyIn() finish.RetroInputs {
	return finish.RetroInputs{Slug: "unverified", Verify: &finish.VerifyRecord{SHA: "aaa1111", Skipped: true}}
}

func skippedVerifyEx() finish.SkeletonExtras {
	return finish.SkeletonExtras{Decisions: []finish.Decision{
		{Kind: finish.DecisionDisposition, Choice: "merge"},
	}}
}

// unrulyIn carries the free text a run can actually record: a reason with a
// line break and a "## " behind it, a title with a pipe, a detail that would
// forge a bullet. Every one of them must land on the single line it was
// rendered onto.
func unrulyIn() finish.RetroInputs {
	return finish.RetroInputs{
		Slug:  "unruly",
		Goals: &finish.GoalsRecord{Waived: map[string]string{"G1": "the docs slipped\n## Not a heading"}},
		FollowUps: []gate.FollowUp{{
			Severity: "major", Wave: new(waveZero), Task: taskOne,
			Title: "a title with a | pipe", Detail: "first line\r\n- forged bullet",
		}},
	}
}

func unrulyEx() finish.SkeletonExtras {
	return finish.SkeletonExtras{
		Shipped: []finish.ShippedRow{{
			Wave: waveZero, Slice: sliceOne, Attempt: attemptOne, SHA: "aaa1111",
			Tasks: []finish.ShippedTask{{ID: 1, Title: "Split a | cell\nand a row"}},
		}},
		Decisions: []finish.Decision{{
			Kind: finish.DecisionGate, Subject: "spec", Choice: "approve",
			Reason: "scope is right\n## Injected",
		}},
	}
}

// fenced wraps a JSON block in the markdown fence the Numbers section
// renders. It is a function because a Go raw string cannot hold a backtick,
// and the block itself is spelled as one so the goldens read as JSON.
func fenced(body string) string { return "```json\n" + body + "```\n" }

// fullRunDoc is the whole document of a run that exercised every
// rendered section: five commits including the reworked wave's two and the
// backfilled one, a decision of each kind, follow-ups of each severity and
// both halves of the Numbers block.
func fullRunDoc() string {
	return doc(
		"# Retro — demo",
		"",
		"## What shipped",
		"",
		"<!-- prose: what shipped — two or three sentences -->",
		"",
		"| wave | slice | attempt | tasks | commit |",
		"| --- | --- | --- | --- | --- |",
		"| 0 | 1 | 2 | 1 — Add the flag; 2 — Wire it up | aaa1111 |",
		"| 0 | 2 | 1 | 3 — Document the flag | bbb2222 |",
		"| 1 | 1 | 1 | 4 — Rename the field | ccc3333 |",
		"| 1 | 1 | 2 | 4 — Rename the field | ddd4444 |",
		"| 2 | 1 | 1 | 6 | eee5555 |",
		"",
		"## Decisions",
		"",
		"- gate: spec — approve (the lock is the only way to snapshot the pair)",
		"- task_waiver: task 5 (the lint directive its fix needs is rejected by the config)",
		"- goal_waiver: G4 (docs land with the next run)",
		"- disposition: pr (the branch wants a second reader)",
		"- spec_assumption: Who writes the retro? — The driving session (the observations exist only there)",
		"",
		"## What went well / what was hard",
		"",
		"<!-- prose: what went well / what was hard — the session's own account of driving this run -->",
		"",
		"## Not proven",
		"",
		"- task 5 — waived: the lint directive its fix needs is rejected by the config",
		"- task 6 — blocked: needs a schema change",
		"- goal G4 — waived: docs land with the next run",
		"- verification — overridden: the flake is upstream",
		"",
		"<!-- prose: not proven — what else must a reader not assume is true -->",
		"",
		"## Lessons",
		"",
		"<!-- prose: lessons — for the next run in this repository -->",
		"",
		"## Follow-ups",
		"",
		"- blocking — the spec omits the lock (gate spec) — two files are replaced in sequence",
		"- blocking — the backfilled commit has no review (wave 2) — the heal wrote the event, not a record",
		"- major — the rename has no test (wave 1/task 4) — no caller exercises the new name",
		"- 2 minor, 1 nit — see follow-ups.json, which holds every one verbatim",
		"",
		"## Numbers",
		"",
	) + fenced(`{
  "internal_review": {
    "candidates": 4,
    "confirmed": 3,
    "false_positives": 1,
    "unattributed": 1,
    "by_lens": {
      "correctness": {
        "reported": 3,
        "confirmed": 2
      }
    },
    "scoped_passes": 2,
    "scoped_changed_verdict": 1,
    "overlap": 1,
    "skipped": 1
  },
  "wave_timings": [
    {
      "wave": 0,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-25T10:00:00Z",
      "closed_at": "2026-08-25T10:09:00Z",
      "committed": true,
      "committed_at": "2026-08-25T10:09:00Z"
    }
  ]
}
`)
}

// emptyRunDoc is a run that shipped nothing and decided nothing: every one
// of the seven headings is here, and each section says what it has —
// "none", the unanswered disposition, or the slot the session fills — so no
// heading reads as an omission (spec §4).
func emptyRunDoc() string {
	return doc(
		"# Retro — empty",
		"",
		"## What shipped",
		"",
		"<!-- prose: what shipped — two or three sentences -->",
		"",
		"none",
		"",
		"## Decisions",
		"",
		"disposition: not yet chosen",
		"",
		"## What went well / what was hard",
		"",
		"<!-- prose: what went well / what was hard — the session's own account of driving this run -->",
		"",
		"## Not proven",
		"",
		"none",
		"",
		"<!-- prose: not proven — what else must a reader not assume is true -->",
		"",
		"## Lessons",
		"",
		"<!-- prose: lessons — for the next run in this repository -->",
		"",
		"## Follow-ups",
		"",
		"none",
		"",
		"## Numbers",
		"",
		"none",
	)
}

// minorsOnlyDoc has no follow-up rendered in full: the count line, naming
// the file that holds them verbatim, is the whole section.
func minorsOnlyDoc() string {
	return doc(
		"# Retro — minors",
		"",
		"## What shipped",
		"",
		"<!-- prose: what shipped — two or three sentences -->",
		"",
		"none",
		"",
		"## Decisions",
		"",
		"disposition: not yet chosen",
		"",
		"## What went well / what was hard",
		"",
		"<!-- prose: what went well / what was hard — the session's own account of driving this run -->",
		"",
		"## Not proven",
		"",
		"none",
		"",
		"<!-- prose: not proven — what else must a reader not assume is true -->",
		"",
		"## Lessons",
		"",
		"<!-- prose: lessons — for the next run in this repository -->",
		"",
		"## Follow-ups",
		"",
		"- 2 minor, 1 advice, 1 praise — see follow-ups.json, which holds every one verbatim",
		"",
		"## Numbers",
		"",
		"none",
	)
}

// noInternalDoc still fences the Numbers block: half of it is missing, and
// null says so where an absent key would not.
func noInternalDoc() string {
	return doc(
		"# Retro — timings",
		"",
		"## What shipped",
		"",
		"<!-- prose: what shipped — two or three sentences -->",
		"",
		"none",
		"",
		"## Decisions",
		"",
		"disposition: not yet chosen",
		"",
		"## What went well / what was hard",
		"",
		"<!-- prose: what went well / what was hard — the session's own account of driving this run -->",
		"",
		"## Not proven",
		"",
		"none",
		"",
		"<!-- prose: not proven — what else must a reader not assume is true -->",
		"",
		"## Lessons",
		"",
		"<!-- prose: lessons — for the next run in this repository -->",
		"",
		"## Follow-ups",
		"",
		"none",
		"",
		"## Numbers",
		"",
	) + fenced(`{
  "internal_review": null,
  "wave_timings": [
    {
      "wave": 0,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-25T10:00:00Z",
      "closed_at": "2026-08-25T10:09:00Z",
      "committed": true,
      "committed_at": "2026-08-25T10:09:00Z"
    }
  ]
}
`)
}

// noTimingsDoc is the mirror image: the timings are an empty list, not a
// null, so a reader comparing runs sees a measurement that found nothing
// rather than one that was never taken.
func noTimingsDoc() string {
	return doc(
		"# Retro — untimed",
		"",
		"## What shipped",
		"",
		"<!-- prose: what shipped — two or three sentences -->",
		"",
		"none",
		"",
		"## Decisions",
		"",
		"disposition: not yet chosen",
		"",
		"## What went well / what was hard",
		"",
		"<!-- prose: what went well / what was hard — the session's own account of driving this run -->",
		"",
		"## Not proven",
		"",
		"none",
		"",
		"<!-- prose: not proven — what else must a reader not assume is true -->",
		"",
		"## Lessons",
		"",
		"<!-- prose: lessons — for the next run in this repository -->",
		"",
		"## Follow-ups",
		"",
		"none",
		"",
		"## Numbers",
		"",
	) + fenced(`{
  "internal_review": {
    "candidates": 1,
    "confirmed": 1,
    "false_positives": 0,
    "unattributed": 0,
    "by_lens": {
      "tests": {
        "reported": 1,
        "confirmed": 1
      }
    },
    "scoped_passes": 0,
    "scoped_changed_verdict": 0,
    "overlap": 0,
    "skipped": 0
  },
  "wave_timings": []
}
`)
}

// skippedVerifyDoc seeds Not proven from a verification that never ran, and
// renders a disposition whose reason is missing as one — the kind and the
// reason are what the section is for, so neither is dropped.
func skippedVerifyDoc() string {
	return doc(
		"# Retro — unverified",
		"",
		"## What shipped",
		"",
		"<!-- prose: what shipped — two or three sentences -->",
		"",
		"none",
		"",
		"## Decisions",
		"",
		"- disposition: merge (no reason given)",
		"",
		"## What went well / what was hard",
		"",
		"<!-- prose: what went well / what was hard — the session's own account of driving this run -->",
		"",
		"## Not proven",
		"",
		"- verification — skipped, the run had no commands to run",
		"",
		"<!-- prose: not proven — what else must a reader not assume is true -->",
		"",
		"## Lessons",
		"",
		"<!-- prose: lessons — for the next run in this repository -->",
		"",
		"## Follow-ups",
		"",
		"none",
		"",
		"## Numbers",
		"",
		"none",
	)
}

// unrulyDoc is the same document with every line break folded away: the
// pipe inside a table cell escaped, the "## " that would have opened an
// eighth section pulled onto its bullet, and the forged bullet kept on the
// line it was written into.
func unrulyDoc() string {
	return doc(
		"# Retro — unruly",
		"",
		"## What shipped",
		"",
		"<!-- prose: what shipped — two or three sentences -->",
		"",
		"| wave | attempt | tasks | commit |",
		"| --- | --- | --- | --- |",
		"| 0 | 1 | 1 — Split a \\| cell and a row | aaa1111 |",
		"",
		"## Decisions",
		"",
		"- gate: spec — approve (scope is right ## Injected)",
		"",
		"disposition: not yet chosen",
		"",
		"## What went well / what was hard",
		"",
		"<!-- prose: what went well / what was hard — the session's own account of driving this run -->",
		"",
		"## Not proven",
		"",
		"- goal G1 — waived: the docs slipped ## Not a heading",
		"",
		"<!-- prose: not proven — what else must a reader not assume is true -->",
		"",
		"## Lessons",
		"",
		"<!-- prose: lessons — for the next run in this repository -->",
		"",
		"## Follow-ups",
		"",
		"- major — a title with a | pipe (wave 0/task 1) — first line - forged bullet",
		"",
		"## Numbers",
		"",
		"none",
	)
}

// headings is the order the document renders them in, and the strings
// internal/brief/templates/run-retro.md names: the session fills this
// document's slots, so the two must agree byte for byte.
var headings = []string{
	"## What shipped",
	"## Decisions",
	"## What went well / what was hard",
	"## Not proven",
	"## Lessons",
	"## Follow-ups",
	"## Numbers",
}

// TestRenderSkeletonNeverLeavesAHeadingBare checks the rule the empty run
// exists to prove: every heading is present and carries content — a "none",
// a rendered line or a prose slot — because a bare heading reads as an
// omission rather than as a fact (spec §4). It also counts the headings: the
// seven are the contract with the template, and free text folded onto its
// own line is what keeps an eighth out.
func TestRenderSkeletonNeverLeavesAHeadingBare(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		got  string
	}{
		{name: "empty run", got: finish.RenderSkeleton(finish.RetroInputs{Slug: "empty"}, finish.SkeletonExtras{})},
		{name: "full run", got: finish.RenderSkeleton(fullRunIn(), fullRunEx())},
		{name: "unruly free text", got: finish.RenderSkeleton(unrulyIn(), unrulyEx())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSevenHeadings(t, tc.got)
		})
	}
}

// assertSevenHeadings is that check: the document carries the seven headings
// and no eighth, and each one is followed by a blank line and then content.
func assertSevenHeadings(t *testing.T, got string) {
	t.Helper()
	ls := strings.Split(got, "\n")
	n := 0
	for _, l := range ls {
		if strings.HasPrefix(l, "## ") {
			n++
		}
	}
	if n != len(headings) {
		t.Fatalf("the document has %d headings, want %d:\n%s", n, len(headings), got)
	}
	for _, h := range headings {
		assertHeadingCarriesContent(t, ls, h, got)
	}
}

// assertHeadingCarriesContent fails when one heading is missing or bare.
func assertHeadingCarriesContent(t *testing.T, ls []string, h, got string) {
	t.Helper()
	i := slices.Index(ls, h)
	if i < 0 {
		t.Fatalf("heading %q missing:\n%s", h, got)
	}
	if i+2 >= len(ls) || ls[i+1] != "" || ls[i+2] == "" || strings.HasPrefix(ls[i+2], "## ") {
		t.Fatalf("heading %q is bare:\n%s", h, got)
	}
}

// TestWriteSkeleton checks the pair that puts the rendered document in the
// bundle, beside the inputs it was rendered from.
func TestWriteSkeleton(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := finish.RenderSkeleton(fullRunIn(), fullRunEx())
	if err := finish.WriteSkeleton(dir, want); err != nil {
		t.Fatalf("WriteSkeleton: %v", err)
	}
	if got := finish.SkeletonPath(dir); got != filepath.Join(dir, "finish", "retro-skeleton.md") {
		t.Fatalf("SkeletonPath = %q", got)
	}
	b, err := os.ReadFile(finish.SkeletonPath(dir))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(b) != want {
		t.Fatalf("written bytes differ from the rendered document:\n%s", b)
	}
}

// TestRenderSkeletonSliceColumn covers the whole rule the full-run golden
// pins one half of. A slice that closes with nothing to commit leaves no
// row, so the commits alone cannot say whether a wave was split: the timings
// are read beside them, and a row numbered above the first slice is evidence
// on its own.
func TestRenderSkeletonSliceColumn(t *testing.T) {
	t.Parallel()
	row := func(w, sl, a int, sha string) finish.ShippedRow {
		return finish.ShippedRow{Wave: w, Slice: sl, Attempt: a, SHA: sha,
			Tasks: []finish.ShippedTask{{ID: 1, Title: "One"}}}
	}
	timing := func(w, sl int) finish.WaveTiming {
		return finish.WaveTiming{Wave: w, Slice: sl, Attempt: attemptOne, DispatchedAt: t0, ClosedAt: t0}
	}
	for _, tc := range []struct {
		name    string
		rows    []finish.ShippedRow
		timings []finish.WaveTiming
		want    string
	}{
		{
			name: "no wave was split",
			rows: []finish.ShippedRow{row(waveZero, sliceOne, attemptOne, "aaa1111"),
				row(waveOne, sliceOne, attemptTwo, "bbb2222")},
			timings: []finish.WaveTiming{timing(waveZero, sliceOne), timing(waveOne, sliceOne)},
			want:    "| wave | attempt | tasks | commit |",
		},
		{
			name:    "the first slice committed nothing",
			rows:    []finish.ShippedRow{row(waveZero, sliceTwo, attemptOne, "aaa1111")},
			timings: []finish.WaveTiming{timing(waveZero, sliceOne), timing(waveZero, sliceTwo)},
			want:    "| wave | slice | attempt | tasks | commit |",
		},
		{
			name:    "the second slice committed nothing",
			rows:    []finish.ShippedRow{row(waveZero, sliceOne, attemptOne, "aaa1111")},
			timings: []finish.WaveTiming{timing(waveZero, sliceOne), timing(waveZero, sliceTwo)},
			want:    "| wave | slice | attempt | tasks | commit |",
		},
		{
			name: "a split wave with no timings at all",
			rows: []finish.ShippedRow{row(waveZero, sliceTwo, attemptOne, "aaa1111")},
			want: "| wave | slice | attempt | tasks | commit |",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := finish.RetroInputs{Slug: "sliced", WaveTimings: tc.timings}
			got := finish.RenderSkeleton(in, finish.SkeletonExtras{Shipped: tc.rows})
			if !strings.Contains(got, tc.want+"\n") {
				t.Fatalf("want header %q:\n%s", tc.want, got)
			}
		})
	}
}

// TestRenderSkeletonNumbersReportsAnUnmarshallableTimestamp covers the one
// way the block can fail to marshal: a WaveTiming's times come from the
// event log, and [time.Time] refuses a year outside [0,9999]. Saying so must
// not leave a fence open in a document the session then copies.
func TestRenderSkeletonNumbersReportsAnUnmarshallableTimestamp(t *testing.T) {
	t.Parallel()
	in := finish.RetroInputs{Slug: "far-future", WaveTimings: []finish.WaveTiming{{
		Wave: waveZero, Slice: sliceOne, Attempt: attemptOne,
		DispatchedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), ClosedAt: t0,
	}}}
	got := finish.RenderSkeleton(in, finish.SkeletonExtras{})
	if strings.Contains(got, "json") && strings.Count(got, "\n```") != 0 {
		t.Fatalf("a fence must not be opened when the block cannot be marshalled:\n%s", got)
	}
	if !strings.Contains(got, "numbers could not be rendered: ") {
		t.Fatalf("the failure must be stated, not swallowed:\n%s", got)
	}
}
