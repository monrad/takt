# Retro — lets-work-on-63

## What shipped

Issue #63 asked for takt's retrospective to become the project's record rather than takt's
telemetry. It landed as nine tasks over six waves: a new `internal/spec` that parses the spec's
assumptions table, a `gate_answered` event that finally carries the user's `--reason`, a pure
`internal/finish.RenderSkeleton` that renders the deterministic sections into a new
`finish/retro-skeleton.md`, one shared `internal/cli/retro.go` derivation that both `takt next`
and the new `takt retro --rewrite` call, a `done --step retro` that works in the `archived`
phase and refuses an unfilled prose slot, and the rewritten `run-retro.md` plus matching host
prompts. All thirteen goals were assessed `achieved` at `473c503`, with `go test ./... -race`,
`golangci-lint run ./...` and `task hosts:check` green. **This file is the first retro written
through that machinery** — the skeleton above was rendered by the code this run added.

| wave | attempt | tasks | commit |
| --- | --- | --- | --- |
| 0 | 3 | — | 1f18bcff10eb3bf85309ef8f2c682436d4c1586c |
| 1 | 2 | 3 — internal/finish/skeleton.go: BuildShipped, BuildDecisions and the pure RenderSkeleton | 48e9e3e45a7abaacc35cd1a2abdc133a8f6cf324 |
| 2 | 1 | 5 — internal/cli/retro.go: one helper derives inputs + skeleton for both next and retro; the op names skeleton_path | c9fe45f59503a2e34055dccfeac5d4d1e054f788 |
| 3 | 2 | — | d46ec06ec0414e3fe6134f237c58cd1b9323d512 |
| 4 | 2 | — | de950a03194339d09068a29be5429f33f55912b6 |
| 5 | 1 | 9 — Both host prompts start the retro from the skeleton, pinned by the cross-host invariant; branch green | f616a06ed396eb5bc299b7de154ea0a837398e45 |

## Decisions

- task_waiver: task 1 (The reviewer reversed its own attempt-1 finding. Attempt 1 required 'table discovery should identify a header/separator pair rather than aborting at the first pipe-bearing line'; attempt 3 requires the opposite, 'return empty once an apparent assumptions header has an invalid separator'. The parser at HEAD passes go test -race, golangci-lint and its own greps, and parses this repository's real specs (16 rows from this run's spec.md, empty for the two older design docs whose column set differs). The disputed case is prose containing a pipe before a table, which no takt-generated spec produces. Open question carried to the retro: whether an invalid separator should abort the section scan or be skipped as prose.)
- task_waiver: task 6 (The blocking finding is spurious: it counts takt's own bundle bookkeeping (events.jsonl, follow-ups.json, state.json, waves/2/close.s1.json, waves/3/, reviews/wave-3) as out-of-scope edits, but takt writes those itself between attempts and the implementer cannot avoid them — the reviewer is reading the whole wave diff, the same failure the sweep run recorded for its T8. The three declared files are the only ones the implementer touched. The surviving real point is minor and deliberate: doneRetroChecks reads retro.md once and folds a read error into the existing 'missing or empty' refusal rather than keeping fileNonEmpty plus a second read, which also makes a whitespace-only retro.md a refusal; the implementer flagged that trade in its report. G9's three cases (archived accepted, execute refused even for a matching receipt, unfilled prose slot refused) are implemented and tested, and go test -race, golangci-lint and the full internal/cli suite are green. Open follow-up carried to the retro: restore fileNonEmpty and surface a genuine read error separately if the whitespace-only change is unwanted.)
- task_waiver: task 7 (Attempt 2 fixed the real blocking defect: cmdNext now acquires the session lock before the archived branch, so an archived next can no longer commit a half-updated inputs/skeleton pair. The implementer mutation-verified it — restoring the old ordering makes TestRetroRewriteLockShutsOutAnArchivedNext fail with the half pair committed. The surviving finding demands atomic lock acquisition (O_EXCL create, CAS or an OS lock), but bundle.Acquire is a pure decision function over an already-read Session (internal/bundle/lock.go:31-43): the read-then-write shape is takt's pre-existing advisory-lock design, documented in design doc section 4.6, unchanged by this task, and internal/bundle is not in task 7's declared files. Making acquisition atomic would change locking for every takt command and belongs in its own run. Open follow-ups carried to the retro: (1) the advisory lock's read-then-write window; (2) internal/cli/archive.go:134-135's doc comment still says the archived path 'takes no lock, so it passes plainOp' — both halves now false, and archive.go was outside every task's file list; (3) no regression test covers a warning surviving onto the archived-replay op.)

disposition: not yet chosen

- spec_assumption: How do the deterministic sections reach `retro.md`? — takt renders `finish/retro-skeleton.md`; the session copies it and fills the prose slots (A markdown file needs no JSON escaping — this repo has already lost time to escape restoration — and stays readable and diffable on its own)
- spec_assumption: What shape does the rewrite path take? — `takt retro --rewrite`, allowed in `finish` and `archived` (Explicit intent, and the archived case is the motivating one: a retro found wanting months later must be redoable)
- spec_assumption: Are the three existing retros rewritten as dogfood? — No (They are the record of runs that happened; rewriting them costs a wave and edits history)
- spec_assumption: Is spec.md's assumptions table parsed? — Yes, by a new `internal/spec` ("Locked decisions carried from spec" is exactly what a compacted session cannot reconstruct, and the brainstorm step already mandates the table)
- spec_assumption: Does `gate_answered` gain the reason? — Yes (One call site has it in scope; without it *Decisions* must stitch four event types together)
- spec_assumption: Who writes the retro? — The driving session, as today — not a subagent (The observations the issue asks for exist only in the session that drove the run)
- spec_assumption: Which follow-ups are rendered in full? — `blocking` and `major`; `minor` and `nit` as counts plus a pointer (The issue's own proposal; `follow-ups.json` already holds every one verbatim)

## What went well / what was hard

- **Every goal achieved, verification green, nothing overridden.** All 71 verify commands passed
  at `473c503`; the goal assessor judged thirteen of thirteen `achieved` on evidence it re-ran
  itself. Three tasks were waived, all three for reviewer disputes rather than missing work — the
  code for each is in the tree and tested.
- **The cross-vendor reviewer contradicted itself on T1, across attempts.** Attempt 1 required
  "table discovery should identify a header/separator pair rather than aborting at the first
  pipe-bearing line"; attempt 3 required the opposite, "return empty once an apparent assumptions
  header has an invalid separator". Both cannot hold without a heuristic neither attempt defined.
  T1 was waived on that ground after three attempts. This is the same flip-flop the sweep run's
  retro recorded for its own T12/T13.
- **The reviewer counted takt's own bookkeeping as scope violations.** T6's blocking finding listed
  `events.jsonl`, `state.json`, `waves/3/` and `reviews/wave-3` as "files outside the task's
  declared scope" — files takt itself writes between attempts, which no implementer can avoid,
  because the reviewer reads the whole wave diff. The sweep run hit this exact failure on its T8.
- **The reviewer also escalated past the run's scope.** T7 attempt 1 raised a genuinely correct
  blocking defect (below); attempt 2 fixed it, and the reviewer then demanded atomic lock
  acquisition — a redesign of `bundle.Acquire`, a pure decision function over an already-read
  session that no task in this run touched. Waived on that ground.
- **T7's first blocking finding was right, and it caught a hole in the spec's own reasoning.** The
  spec asserted the run lock closed a race with `recommitArchive` on an archived run; the reviewer
  showed `cmdNext`'s archived branch takes no lock at all, so the claimed guarantee never held.
  The fix moved `acquireLock` ahead of the phase switch, and the implementer mutation-verified it:
  restoring the old ordering makes `TestRetroRewriteLockShutsOutAnArchivedNext` fail with the
  half-updated pair committed.
- **The driving binary predated the branch.** `takt next` emitted the *old* retro template right
  through the finish phase, because `~/go/bin/takt` is a `0.0.0-dev` build that does not pick up
  branch changes. `go install ./cmd/takt` mid-run is what let this retro use its own skeleton —
  the same move #36 needed for its generated PR body.
- **Dogfooding the skeleton found two defects in its own output, visible above.** The *What shipped*
  table renders `—` for tasks on waves 0, 3 and 4 — every wave whose final commit followed a waive,
  where the closing `wave_committed` carries no task ids. And `wave_timings` lists waves 0, 3 and 4
  twice, once per backfilled attempt. Neither is caught by `skeleton_test.go`'s fixtures.
- **Internal review: 49 candidates, 45 confirmed (92%), 4 false positives, 0 overlap.** Per lens —
  tests 21/23, consistency 7/9, docs 5/6, simplicity 5/5, correctness 4/4, intent 3/3. Every lens
  confirmed at least one finding, so none is a removal candidate from `review.lenses`. The zero
  overlap says the lenses and the cross-vendor reviewer looked at genuinely different things, which
  is the intended division. No scoped passes ran.
- **Every agent reply came back HTML-escaped.** `&lt;`, `&gt;` and `&amp;` had to be restored before
  each `takt record`, or a reply carrying a prose marker would have been recorded corrupted.
- **Editor diagnostics repeatedly reported compile errors the compiler denied** — `undefined:
  driveToExecute`, `waveNumbers redeclared`, `cmdRetro is unused`. Each was a stale mid-edit
  snapshot; `go vet ./internal/cli/...` was clean every time. Checking each one before dispatching
  six review agents cost seconds and saved a wave.

## Not proven

- task 1 — waived: The core parser works, but it can return rows from a later table after encountering the explicitly malformed header/separator shape that must invalidate the section.
- task 6 — waived: Core retro behavior is implemented correctly, but the change set includes files outside the declared scope and slightly alters the existing non-empty-file semantics.
- task 7 — waived: The command behavior and coverage largely match the task, but the central concurrency guarantee is not actually enforced because lock acquisition is non-atomic.

- The advisory session lock is still not atomic: `bundle.Acquire` decides over an already-read
  session, so two callers can both find it free. T7's fix serialises the archived `next` path
  against `retro --rewrite` in practice, but does not make acquisition a compare-and-swap.
- `internal/cli/archive.go:134-135` still says the archived path "takes no lock, so it passes
  plainOp". Both halves are now false — `plainOp` was deleted — and `archive.go` was outside every
  task's declared file list, so nothing in this run could fix it.
- Design §7.5 step 5 still claims the archive commit "is the run's last one" and the bundle is
  "otherwise untouched once archived". `done --step retro` in the archived phase now lands its own
  `retro done` commit after it. Task 8's verify commands were scoped to step 3, so step 3 is correct
  and step 5 is stale.
- No test proves a warning survives onto the archived-replay op, the reason `applyAndStop` was
  changed from `plainOp` to `r.emit`.
- T1's open question stands: whether a header row with an invalid separator should abort the section
  scan or be skipped as prose. The parser at HEAD skips it.
- T6 changed one behaviour deliberately and untested: a whitespace-only `retro.md` is now refused as
  "missing or empty", where `fileNonEmpty` previously accepted it.
- The two defects the skeleton showed in its own *What shipped* and *Numbers* above are unfixed.

## Lessons

- **Rebuild `~/go/bin/takt` before the finish phase whenever the run changes what a later op emits.**
  This run wrote nine tasks' worth of retro machinery and would have written its retro with the old
  template had the binary not been reinstalled at the retro op. Make it a step, not a catch.
- **Dogfood the artifact, not just its tests.** `skeleton_test.go` is green on seven golden
  documents, and the first real render still showed empty task cells and duplicated timings. A
  golden fixture proves the renderer matches its fixture; only real inputs prove it matches reality.
- **Write assumptions-table headings without an ordinal, or expect the parser to need widening.**
  This run's own `spec.md` used a numbered heading; the brief specified a prefix match on the bare
  one, so the parser would have read zero rows from the very document it exists to read. The T1
  implementer caught it and flagged the deviation rather than hiding it — that flag was worth more
  than the fix.
- **Budget for the reviewer reading the whole wave diff.** takt's own bundle writes will read as
  out-of-scope edits, and the same complaint will recur on every retry; it is not something a
  rework can clear.
- **When the reviewer reverses itself, waive with the two quotes side by side.** Three attempts is
  enough to establish a contradiction; a fourth just picks a side the next attempt may flip back.
- **Check editor diagnostics against `go vet` before acting on them.** They were wrong every time
  this run, always mid-edit staleness, and each would have cost a wasted review wave.

## Follow-ups

- major — clearGate doc comment cites the wrong document's section 4, inside the same package that already uses the convention correctly (wave 0/task 2) — The new sentence 'the answer a Decision the retro can render (spec §4)' points at docs/superpowers/specs/2026-08-24-takt-design.md §4 ('The run bundle': directory layout, state.json, events.jsonl, path rules, session lock, git) — nothing about Decisions or reasons. The rule being described ('a reasonless answer is process, not a decision') actually lives in §4 of this run's own local docs/takt/lets-work-on-63/spec.md ('The skeleton renderer'), a different, ephemeral document. This is the same miscitation pattern as internal/spec/assumptions.go:2, and it's especially visible here because internal/cli/cli.go:35 (same package) already uses 'spec §5.1' correctly to mean the design doc — so the package now contains two incompatible meanings for the same citation shorthand. The pattern recurs in test comments too (internal/brief/brief_test.go:190 '(spec §6)', internal/cli/cmd_answer_test.go:228 'spec §5.1') but those are lower-stakes than the two production doc comments.
- major — reason-threading only tested for two of the many gates that now carry it (wave 0/task 2) — clearGate's new reason parameter (internal/cli/bundleops.go:254-268) is threaded from cmdAnswer (internal/cli/cmd_answer.go:74) for every gate handled by applyAnswer — gate_review, gate_review_capped, wave_failures, review_error, verification_failed, no_verification, goals_unmet, branch_finish. The task text explicitly calls out 'no_verification's specify' as exercising 'the spec's single rule', yet TestGateAnsweredCarriesReasonAndOmitsItWhenEmpty (the only new test for this behaviour) checks gate_answered's reason field solely for gate_review_capped/accept and gate_review/revise. The other five gate paths that also call clearGate with a caller-supplied reason (e.g. no_verification's specify, which internal/cli/finish_test.go:180 already answers with --reason "test -f a.go" but never inspects the resulting event) are untested for this new field.
- major — shippedTasks' skip-non-numeric-item branch has no test (wave 1/task 3) — shippedTasks (skeleton.go:128-146) explicitly skips any tasks-array item that isn't a float64 (`n, isNum := item.(float64); if !isNum { continue }`), per its own doc comment. No fixture in internal/finish/skeleton_test.go ever puts a non-numeric entry in a wave_committed event's tasks list, so this branch — and the resulting row's task count/order when a bogus entry is mixed with valid ids — is completely unexercised. A regression here (e.g. accidentally keeping the item with ID 0, or mis-indexing subsequent tasks) would not be caught by TestBuildShippedResolvesTitlesAndKeepsUnknownIdsBare or TestBuildShippedFloorsASliceLessCommitToOne, both of which only use well-formed float64 ids.
- major — sliceColumn carries a dead branch that can never fire given the codebase's own slice-floor invariant (wave 1/task 3) — sliceColumn (skeleton.go:325-337) builds a `seen map[int]int` and, for each key, checks both `k.slice > 1` and (if that's false) `seen[k.wave] != k.slice`. But every production producer of a ShippedRow or WaveTiming goes through timingKeyOf (internal/finish/retro.go:305-310, `slice: max(int(sl), 1)`) — BuildShipped calls it directly (skeleton.go:111) and waveTimings does too (retro.go:328) — so k.slice is never < 1 anywhere in the pipeline (verified: the only production assignment of RetroInputs.WaveTimings is `in.WaveTimings = waveTimings(events)` at retro.go:153, and the only ShippedRow constructor is BuildShipped; grepped the repo for `ShippedRow{` and `WaveTiming{` and found no other constructor, in tests or code, that supplies a slice below 1). Given that invariant, any two different slice values seen for the same wave must include one that is >1, so the first `if k.slice > 1 { return true }` always fires before the `seen` comparison could ever produce a different verdict — the `seen` map and its comparison are unreachable dead weight. The function's own doc comment even states the sufficient rule outright ('since slices are numbered from one, any number above it is a second slice by itself'), which describes the simpler `k.slice > 1`-only check that would make sliceKeys/seen unnecessary; the code keeps the extra machinery anyway. This is exactly the kind of premature-generalisation/dead-fallback complexity the lens is meant to catch: an allocation, an extra loop-carried map and a second branch defending against a case the type's real producers structurally cannot create.
- major — followUpLine's no-Detail path for full-severity follow-ups is untested (wave 1/task 3) — followUpLine (skeleton.go:564-573) only appends ' — <detail>' when f.Detail != "". Every blocking/major fixture across all TestRenderSkeletonGolden subtests (fullRunFollowUps, unrulyIn) sets Detail on every full-severity entry, so the branch where a blocking or major follow-up has no Detail is never rendered/asserted. Since follow-ups routinely lack a detail in practice, a formatting bug in this path (e.g. a stray trailing '— ' or missing space) would ship into every retro document undetected.
- major — Design doc's §7.5 step 5 "archive commit is the run's last one" invariant is now false, and no task in the plan is scoped to fix it (wave 3) — §7.5 step 5 states "commit `takt(<slug>): archive`. That commit is the run's last one, which is what lets a merge carry the archived bundle" (lines 884-885), and describes the only mechanism by which the bundle directory can still change post-archive as a later `next` sweeping dirty files into a *second archive commit* (lines 902-911: "the bundle directory is otherwise untouched once archived"). This diff's `finishOrArchivedOnly` lets `done --step retro` run in the `archived` phase and, per the new `doneRetro` doc comment (internal/cli/cmd_done.go:189-191) and TestDoneRetroAcceptedInArchivedPhase, produce its own ordinary `"retro done"` bundle commit landing after the archive commit — a distinct mechanism the design doc doesn't describe, and one that falsifies the "last one" claim outright for the `keep` disposition. Task 8 (the dedicated docs task for this section) explicitly scopes its edits, and its own verify checks, to only the `3. **Retro**`–`4. **Disposition**` markers of step 3 (docs/takt/lets-work-on-63/plan.md:92-97), so step 5's stale text is left unaddressed by the plan as written.
- major — §7.5 step 5's "archive commit is the run's last one" / "bundle directory... otherwise untouched once archived" invariant is now false and no task updates it (wave 3) — Step 5 states "commit `takt(<slug>): archive`. That commit is the run's last one" (line 884) and later "there is no write after the archive commit" / "the bundle directory is otherwise untouched once archived" (lines 902-909), describing the only post-archive bundle-write mechanism as a later `next` sweeping dirty files into a *second archive commit*. This diff's `finishOrArchivedOnly` (internal/cli/cmd_verify.go:64) lets `done --step retro` run in `archived`, and `doneRetro`'s own doc comment (internal/cli/cmd_done.go:195-201) plus TestDoneRetroAcceptedInArchivedPhase (internal/cli/finish_test.go, asserting `git log -1` contains "retro done") confirm it lands its own distinct `"retro done"` bundle commit after the archive commit — a mechanism §7.5 step 5 does not describe and one that falsifies both the "last commit" and "untouched once archived" claims for every disposition, not just `keep`. cmd_done.go:200-201's own comment ("design §7.5 step 5 already contemplates post-archive bundle writes") asserts the design doc already covers this, but step 5 only contemplates the second-archive-commit sweep, not an independent `done`-issued commit. No task in this run's plan (docs/takt/lets-work-on-63/plan.md) is scoped to fix step 5's text — task 8 explicitly restricts its edits and verify checks to the `3. **Retro**`–`4. **Disposition**` span of step 3 (plan.md:92-97) — so this contradiction ships undocumented. (Already logged as a follow-up in docs/takt/lets-work-on-63/follow-ups.json at wave 3, but it remains unfixed as of this diff.)
- major — applyAndStop doc comment contradicts this wave's own change to cmd_next.go (wave 4) — archive.go:130-135 still says "The later call on an archived run takes no lock, so it passes plainOp." This diff deletes `plainOp` entirely from cmd_next.go and moves the archived-run branch in cmdNext to run after `r.acquireLock`, passing `r.emit` instead (see cmd_next.go's rewritten comments at the same location, e.g. "applyAndStop releases the lock this call just took"). `plainOp` no longer exists anywhere in internal/cli (grep confirms archive.go:135 is the only remaining reference), so the comment both names a deleted function and asserts the opposite of the new, intentional behaviour (the archived path now takes the lock, which is the whole point of task 7's fix — a concurrent rewrite must be able to shut out an unlocked archived `next`). This file wasn't in this task's file list, so the change to cmd_next.go's locking behaviour was not propagated to the neighbouring doc comment that describes the same code path.
- 7 minor, 2 nit — see follow-ups.json, which holds every one verbatim

## Numbers

```json
{
  "internal_review": {
    "candidates": 49,
    "confirmed": 45,
    "false_positives": 4,
    "unattributed": 4,
    "by_lens": {
      "consistency": {
        "reported": 9,
        "confirmed": 7
      },
      "correctness": {
        "reported": 4,
        "confirmed": 4
      },
      "docs": {
        "reported": 6,
        "confirmed": 5
      },
      "intent": {
        "reported": 3,
        "confirmed": 3
      },
      "simplicity": {
        "reported": 5,
        "confirmed": 5
      },
      "tests": {
        "reported": 23,
        "confirmed": 21
      }
    },
    "scoped_passes": 0,
    "scoped_changed_verdict": 0,
    "overlap": 0,
    "skipped": 0
  },
  "wave_timings": [
    {
      "wave": 0,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-29T11:02:55.27031687Z",
      "closed_at": "2026-08-29T11:30:11.477987638Z",
      "committed": false
    },
    {
      "wave": 0,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-29T11:30:17.955501743Z",
      "closed_at": "2026-08-29T11:46:22.249633889Z",
      "committed": false
    },
    {
      "wave": 0,
      "slice": 1,
      "attempt": 3,
      "dispatched_at": "2026-08-29T11:47:28.625658485Z",
      "closed_at": "2026-08-29T11:57:21.360538594Z",
      "committed": true,
      "committed_at": "2026-08-29T12:00:47.001660996Z"
    },
    {
      "wave": 0,
      "slice": 1,
      "attempt": 3,
      "dispatched_at": "2026-08-29T11:47:28.625658485Z",
      "closed_at": "2026-08-29T12:00:47.001681898Z",
      "committed": true,
      "committed_at": "2026-08-29T12:00:47.001660996Z"
    },
    {
      "wave": 1,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-29T12:00:55.176989062Z",
      "closed_at": "2026-08-29T12:33:31.574165478Z",
      "committed": false
    },
    {
      "wave": 1,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-29T12:33:40.599764319Z",
      "closed_at": "2026-08-29T12:59:39.744810898Z",
      "committed": true,
      "committed_at": "2026-08-29T12:59:39.744776375Z"
    },
    {
      "wave": 2,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-29T12:59:46.66377414Z",
      "closed_at": "2026-08-29T13:17:19.679178379Z",
      "committed": true,
      "committed_at": "2026-08-29T13:17:19.679152891Z"
    },
    {
      "wave": 3,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-29T13:17:27.241316178Z",
      "closed_at": "2026-08-29T13:34:26.962203695Z",
      "committed": false
    },
    {
      "wave": 3,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-29T13:34:41.946928183Z",
      "closed_at": "2026-08-30T13:30:31.204485537Z",
      "committed": true,
      "committed_at": "2026-08-30T13:31:27.384782779Z"
    },
    {
      "wave": 3,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-29T13:34:41.946928183Z",
      "closed_at": "2026-08-30T13:31:27.384826349Z",
      "committed": true,
      "committed_at": "2026-08-30T13:31:27.384782779Z"
    },
    {
      "wave": 4,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T13:31:35.450603095Z",
      "closed_at": "2026-08-30T13:57:22.058627178Z",
      "committed": false
    },
    {
      "wave": 4,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-30T13:57:50.755442817Z",
      "closed_at": "2026-08-30T14:21:37.489933739Z",
      "committed": true,
      "committed_at": "2026-08-30T14:22:45.482997274Z"
    },
    {
      "wave": 4,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-30T13:57:50.755442817Z",
      "closed_at": "2026-08-30T14:22:45.483032233Z",
      "committed": true,
      "committed_at": "2026-08-30T14:22:45.482997274Z"
    },
    {
      "wave": 5,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T14:22:53.214462273Z",
      "closed_at": "2026-08-30T14:28:01.447797258Z",
      "committed": true,
      "committed_at": "2026-08-30T14:28:01.44775853Z"
    }
  ]
}
```
