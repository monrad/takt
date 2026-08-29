# Retro as the project's record, not takt's telemetry (#63)

## 1. Problem

takt's retro is written for takt; masterplan's is written for the project. Comparing the
three retros under `docs/takt/` with masterplan's fourteen:

- `internal/brief/templates/run-retro.md` fixes three sections and says "bullet points
  grounded in the inputs". The writer is told to report `finish/retro-inputs.json` and
  nothing else. The sweep retro's most useful lines — the cross-vendor reviewer
  contradicting itself between attempts, T5 waived over a lint directive the lint config
  rejects, the driving binary predating the run — are there only because the writer broke
  that rule.
- The follow-ups tail dominates: 27 entries in a 49-line retro, 20 entries over half of
  154 lines. It duplicates `follow-ups.json` verbatim instead of saying which ones matter.
- Lens yield, overlap and per-attempt timings are telemetry for improving takt (#55 needs
  them), not information for the maintainer of the repository the run was for. They already
  have a durable home: `finish/retro-inputs.json` *is* the measurement record.
- The sections a reader returns to months later — *Decisions*, *What is NOT proven here*,
  *Lessons* — have no place in takt's retro at all.

## 2. What changes

1. `run-retro.md` is rewritten around seven sections, and its "grounded in the inputs" rule
   is scoped to numbers only — the session's own observations from driving the run are
   invited, and given a place.
2. takt renders the deterministic sections itself into a new `finish/retro-skeleton.md`.
   The session copies it to `retro.md` and fills the prose slots.
3. `gate_answered` carries the user's `--reason`, and a new `internal/spec` parses spec.md's
   assumptions table — the two inputs *Decisions* needs and takt does not have today.
4. A new `takt retro --rewrite` re-derives the inputs and skeleton and re-emits the retro
   run op, in the `finish` **and** `archived` phases.

The retro stays a `run` op, written by the **driving session** — not a subagent. That is
load-bearing: the observations §1 asks for exist only in the session that drove the run. A
fresh subagent could only re-report the inputs, which is the failure being fixed.

## 3. The retro's new shape

```
# Retro — <slug>
## What shipped     prose (2–3 sentences), then a wave × tasks × commit table
## Decisions        gate answers with their --reason, waivers, the spec's user-confirmed
                    assumptions, the disposition
## What went well / what was hard
                    prose — the session's own account; cite the inputs where they back a claim
## Not proven       seeded with waived goals/tasks and overridden or skipped verification;
                    prose for whatever else a reader must not assume is true
## Lessons          prose — for the next run in this repository
## Follow-ups       blocking and major by title with a one-line detail; minors and nits as a
                    count and a pointer to follow-ups.json, which holds every one verbatim
## Numbers          the internal_review / wave_timings block verbatim from the inputs, so
                    cross-run comparison (#55) still works
```

Rendered by takt: the *What shipped* table, *Decisions*, the *Not proven* seed, *Follow-ups*,
*Numbers*. Written by the session: every `<!-- prose: … -->` slot.

## 4. The skeleton renderer

New `internal/finish/skeleton.go`:

```go
// SkeletonExtras is what the skeleton needs that RetroInputs does not carry.
// Both fields are already resolved: RenderSkeleton looks nothing up.
type SkeletonExtras struct {
    Shipped   []ShippedRow // one per wave_committed event
    Decisions []Decision   // gate answers, waivers, disposition, spec assumptions
}

func RenderSkeleton(in RetroInputs, ex SkeletonExtras) string
```

`RenderSkeleton` is **pure** — no filesystem, no clock — exactly as `BuildRetroInputs` is, so
a replayed `next` writes the same bytes and re-emitting the retro op is free (design §5.4).

- **`ShippedRow{Wave, Slice, Attempt int, SHA string, Tasks []ShippedTask}`**, where
  `ShippedTask{ID int; Title string}` — one row per `wave_committed` event, ordered by wave,
  slice, attempt, including backfilled ones and including a retried attempt that also
  committed. Each is a real commit on the branch; hiding one would make the table lie. The
  slice column is rendered only when some wave has more than one slice.

  The rows are built by `BuildShipped(events []bundle.Event, idx plan.Index) []ShippedRow`,
  which is where `plan.Index` is read and each task id resolved to its title. A committed id
  the index does not know renders as its id alone. `RenderSkeleton` therefore needs no index
  and performs no lookup — it renders what it is handed, which is what keeps it pure.
- **`Decision{Kind, Subject, Choice, Reason}`** — `Kind` is one of `gate`, `task_waiver`,
  `goal_waiver`, `disposition`, `spec_assumption`. Built by a second pure function in the
  same file, `BuildDecisions(events []bundle.Event, st *bundle.State, as []spec.Assumption)
  []Decision`, so the caller in `internal/cli` reads the bundle and everything in
  `internal/finish` stays a function of what it is handed. Sources, in order:

  1. Every `gate_answered` event carrying a **non-empty reason**. A reasonless answer is not
     a decision — `gate_review → revise` is process, not a choice a reader needs — and an
     event written before §5.1's change carries no reason at all, so legacy events simply do
     not appear. This is the single rule; there is no separate legacy rendering.
  2. Every `task_waived` event.
  3. Every `goal_waived` event.
  4. `state.Disposition`, **when it is non-nil**. On the first pass it always is nil:
     `decideFinish` emits the retro at row 22 and asks `branch_finish` at row 23, so the
     disposition does not exist yet, and §10 keeps that ordering out of scope. The
     disposition therefore reaches the retro only through `takt retro --rewrite`, which is
     run after archiving — one more reason the rewrite path exists. The Decisions section
     renders an explicit "disposition: not yet chosen" line on the first pass, so a reader
     sees an unanswered question rather than a missing one.
  5. The `user-confirmed` rows of the spec's assumptions table, passed in as `as`.

  `BuildDecisions` is the **only** consumer of the parsed assumptions: it filters them and
  emits `spec_assumption` decisions. `SkeletonExtras` deliberately carries no assumptions
  field, so there is exactly one path by which an assumption reaches the page and no way to
  render one twice.
- **Follow-ups** are bucketed from `in.FollowUps`: `blocking` and `major` rendered as
  `severity — title (where) — detail`, the rest as one line giving the counts and naming
  `follow-ups.json`. `where` is the existing `gate` / `wave N/task M` locator.
- **Numbers** fences `{"internal_review": …, "wave_timings": …}` from `in`. Absent when the
  run recorded neither.
- The **Not proven** seed comes from `in` alone: tasks whose status is `waived` (and any
  other non-`done` status) from `in.Failures`, waived goals from `in.Goals`, and an
  overridden or skipped verification from `in.Verify`.
- An empty run renders every section with its heading and an explicit "none" line — never a
  bare heading, which reads as an omission rather than a fact.

Written to `finish/retro-skeleton.md` by the same code path that writes the inputs
(`writeRetroInputs` in `internal/cli/cmd_next.go`), atomically via `bundle.WriteFileAtomic`.
It is a bundle file and is committed by whichever command next commits the bundle.

## 5. Decisions' two new inputs

### 5.1 `gate_answered` gains `reason`

`clearGate` (`internal/cli/bundleops.go:248`) records `{gate, choice}` and drops the
`--reason` that its only caller (`internal/cli/cmd_answer.go:74`) has in scope. Thread it
through and record it `omitempty`. This gives *Decisions* one uniform source for every gate
the user answered with a reason, instead of stitching gate receipts, `task_waived`,
`goal_waived` and `disposition` back together. An event written before this field existed
carries no reason and so contributes no decision at all, which is §4's single omission rule
applied to legacy events rather than an exception to it: the run that wrote them recorded no
reason anywhere, and inventing a reasonless decision line would say less than saying
nothing.

### 5.2 `internal/spec` — the assumptions table

New package, parallel to `internal/goals` (which parses `goals.md`):

```go
type Assumption struct{ Question, Decision, Rationale, Source string }
func ParseAssumptions(b []byte) []Assumption
```

- Finds a `## Assumptions & Open Decisions` heading (case-insensitive, tolerating trailing
  text on the line), then reads the markdown table under it: header row, separator row, then
  data rows until a blank line or the next heading.
- Columns are matched **by header name**, not position, so a spec that orders them
  differently still parses.
- Tolerant by construction: no section, no table, missing headers or a short row yields an
  empty slice and never an error. A spec is prose written by an agent; a retro must not fail
  because one lacks a table.
- Returns every row with its `Source`; the skeleton filters to `user-confirmed`. The parser
  stays dumb.

## 6. The template rewrite

`internal/brief/templates/run-retro.md` is replaced. It must say, explicitly:

- **Start from the skeleton.** Copy `{{.SkeletonPath}}` to `{{.RetroPath}}` and fill each
  `<!-- prose: … -->` slot. Do not rewrite the rendered sections; they are the record.
- **"Grounded in the inputs" applies to numbers only.** *What went well / what was hard* and
  *Lessons* are the session's own account of driving this run — the reviewer that
  contradicted itself, the waiver whose grounds the tree disproves, the tool that
  misbehaved. Cite the inputs where they back a claim.
- **A rewrite from a fresh session has no observations.** Write what the skeleton and inputs
  support and do not invent an account of a run you did not drive.
- The old line describing the inputs as "per-wave dispatch→commit timings" goes: `WaveTiming`
  became dispatch→close and a reworked attempt has no `committed_at` (this closes the
  still-open follow-up 19 from the sweep run).

`brief.RunData` gains `SkeletonPath`; the retro op's `inputs` gains `skeleton_path`.

## 7. `takt retro --rewrite`

New `internal/cli/cmd_retro.go`, registered as `retro` in `cli.go`'s `commands` table.

- Flags: `--slug`, `--dir` (the standard pair) and `--rewrite`.
- Without `--rewrite` it is a usage error naming the flag. Re-derivation is harmless, but the
  verb should state its intent.
- Allowed phases: `finish` and `archived`. Anything earlier fails with the existing
  phase-error wording. The archived case is the motivating one: a retro read months later and
  found wanting must be redoable.
- It re-derives `finish/retro-inputs.json` and `finish/retro-skeleton.md`, renders the
  `run-retro` template and prints the same `run` op `next` emits, with
  `narration: "rewrite the retrospective"`.
- **It takes the run lock, exactly as `next` does**, and writes no state. Per-file atomic
  renames are not enough: it replaces two tracked files in sequence, and a concurrent `next`
  on an archived run calls `recommitArchive`, which stages and commits whatever in the bundle
  is dirty. That commit can land between the two writes and capture a half-updated pair.
  Purity makes the *content* reproducible; it does not make the *pair* a snapshot. The lock
  is what does. A held lock is reported with takt's existing lock error, which `takt unlock`
  already resolves.

Two supporting changes:

- **Extract the shared path.** `nextRun.writeRetroInputs` and the retro branch of
  `nextRun.run` move to a `bdir`/`state`-taking helper (a new `internal/cli/retro.go`) that
  both `cmdNext` and `cmdRetro` call. No behaviour change on the `next` side.
- **`doneRetro` accepts `archived`.** `finishPhaseOnly` becomes a finish-or-archived check
  for this step, so the recording half of a rewrite works too. `doneAlready` already compares
  the artifact hash, so a changed `retro.md` re-records with no further change. The resulting
  `retro done` commit on an archived run is an ordinary bundle commit; design §7.5 step 5
  already contemplates the bundle directory being written to after archiving.

A run whose branch was deleted by a `merge` or `discard` disposition has no checked-out
bundle to rewrite; `openTarget`'s existing "no run named <slug>" error is the right answer
there and is left alone.

## 8. Documentation

- `docs/superpowers/specs/2026-08-24-takt-design.md`: add `finish/retro-skeleton.md` to the
  §4.2 bundle layout; rewrite §7.5 step 3 to describe the skeleton, the seven sections and
  the disposition's absence on the first pass; add `retro` to the command table in **§5.1**
  (§6 is the command prompt, not the command list).
- `commands/takt.md` and `hosts/copilot/skills/takt/SKILL.md`: the op-table `run` row for
  `retro` currently reads "write `inputs.retro_path` from `inputs.inputs_path`" — it must
  name the skeleton. `internal/prompt/prompt_test.go`'s cross-host invariants hold the two
  prompts to the same text.

## 9. Testing

- `internal/spec/assumptions_test.go` — a well-formed table; reordered columns; no section;
  a heading with no table; a short row; rows of every `source` value.
- `internal/finish/skeleton_test.go` — golden renders: a full run (multi-wave, multi-slice, a
  retried commit, decisions of all five kinds, mixed-severity follow-ups); a run with no
  commits, no decisions and no follow-ups; a run with only minor follow-ups; a run with no
  `internal_review`. Purity: the same input renders identical bytes twice.
- `BuildShipped` — a committed task id absent from the index renders as the bare id.
- `BuildDecisions` — a `gate_answered` with a reason becomes a decision; one without a reason
  and one written before the field existed both produce nothing; a nil disposition renders
  the "not yet chosen" line and a non-nil one renders the choice.
- `internal/cli/cmd_retro_test.go` — `--rewrite` prints a `run`/`retro` op naming all three
  paths and writes both files; bare `takt retro` is a usage error; `execute` phase is refused;
  `archived` is accepted; a run whose lock is already held is refused with the lock error.
- `internal/cli/finish_test.go` — where the `done --step retro` path is already tested:
  it succeeds in the `archived` phase and still refuses in `execute`.
- `internal/cli/cmd_answer_test.go` — an answered gate's `gate_answered` event carries the
  `--reason`, and omits the key when none was given.
- `internal/brief/brief_test.go` — the rendered retro instructions carry all seven headings,
  name the skeleton path, and no longer contain "dispatch→commit".
- `internal/prompt/prompt_test.go` — the retro op row's new wording is present in both host
  prompts.

## 10. Out of scope

- Rewriting the three existing retros under `docs/takt/`. They are historical records.
- Any change to `follow-ups.json`'s own shape or to `gate.FollowUp`.
- #55's cross-run comparison tooling. This change only guarantees the *Numbers* block it will
  read stays intact.
- Any change to `decideFinish`'s row order or to when the retro op is decided.

## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| How do the deterministic sections reach `retro.md`? | takt renders `finish/retro-skeleton.md`; the session copies it and fills the prose slots | A markdown file needs no JSON escaping — this repo has already lost time to escape restoration — and stays readable and diffable on its own | user-confirmed |
| What shape does the rewrite path take? | `takt retro --rewrite`, allowed in `finish` and `archived` | Explicit intent, and the archived case is the motivating one: a retro found wanting months later must be redoable | user-confirmed |
| Are the three existing retros rewritten as dogfood? | No | They are the record of runs that happened; rewriting them costs a wave and edits history | user-confirmed |
| Is spec.md's assumptions table parsed? | Yes, by a new `internal/spec` | "Locked decisions carried from spec" is exactly what a compacted session cannot reconstruct, and the brainstorm step already mandates the table | user-confirmed |
| Does `gate_answered` gain the reason? | Yes | One call site has it in scope; without it *Decisions* must stitch four event types together | user-confirmed |
| Who writes the retro? | The driving session, as today — not a subagent | The observations the issue asks for exist only in the session that drove the run | user-confirmed |
| Which follow-ups are rendered in full? | `blocking` and `major`; `minor` and `nit` as counts plus a pointer | The issue's own proposal; `follow-ups.json` already holds every one verbatim | user-confirmed |
| Does `takt retro --rewrite` take the run lock? | Yes, as `next` does | It replaces two tracked files in sequence, and an archived `next` can `recommitArchive` between them; per-file atomicity gives no two-file snapshot (spec review, blocking) | assumed |
| Can the disposition appear in the first retro? | No — `decideFinish` emits the retro before `branch_finish`, so it renders "not yet chosen"; only a post-archive `--rewrite` shows it | Reordering the finish rows is out of scope, and the alternative — a retro that claims a disposition that does not exist — is worse than one that says so (spec review, blocking) | assumed |
| Do reasonless gate answers become decisions? | No. Only a `gate_answered` carrying a reason does, which also means legacy events contribute nothing | `gate_review → revise` is process, not a decision a reader returns to; one rule beats a rule plus a legacy exception (spec review, blocking) | assumed |
| Who resolves task ids to titles? | `BuildShipped`, which reads `plan.Index`; `ShippedRow` carries `{ID, Title}` and `RenderSkeleton` looks nothing up | Keeps the renderer pure and index-free, and keeps the lookup in the one place that already reads the bundle (spec review, blocking) | assumed |
| Does `done --step retro` reject a retro whose prose slots are unfilled? | Yes — a `<!-- prose:` marker still present is an error naming the slot | Cheap guard against committing the skeleton verbatim, which is the failure mode the skeleton introduces | assumed |
| How are prose slots marked? | HTML comments, `<!-- prose: … -->` | Invisible in rendered markdown, greppable, and unambiguous for the `done` check above | assumed |
| One table row per commit, or per wave? | Per `wave_committed` event — retried attempts and backfills included | Each is a real commit on the branch; collapsing them would make the table claim a history that did not happen | assumed |
| Where does the skeleton live? | `finish/retro-skeleton.md`, beside `retro-inputs.json` | `finish/` already holds the run's derived finish-phase artifacts | assumed |
| Which package parses spec.md? | A new `internal/spec` | Parallel to `internal/goals`, which parses `goals.md`; keeps `internal/finish` free of markdown parsing | assumed |
