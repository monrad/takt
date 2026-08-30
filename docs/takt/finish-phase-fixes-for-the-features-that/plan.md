# Plan — finish-phase-fixes-for-the-features-that

## Approach

The spec names four independent defects with disjoint blast radii: the retro renderers in
`internal/finish` (#71), the pull-request body builder in `internal/finish/pr.go` (#74), the two
host prompts plus a new skill generator (#66), and stale prose in `internal/cli` and the design
document (#72). They share no files, so the run is three parallel implementation tasks and one
closing docs task that doubles as the whole-branch gate (`task test`, `task lint`,
`task hosts:check` — G9). Nothing in the plan touches the `wave_committed` emitters,
`commitWaveOnce`, `recordCloseOutcome`, `waveSubject` or any archived-path behaviour: #71 is fixed
where the retro is rendered so bundles already on disk repair themselves on the next
`takt retro --rewrite`, and #72 changes no executable line at all.

Task boundaries follow package seams. Task 1 owns the whole of #71 — both renderer fixes, the
one-line caller change in `internal/cli/retro.go`, the eighth golden and the focused tests — because
the golden proves both halves through one fixture and splitting it would put two tasks in
`skeleton_test.go`. Task 2 owns `pr.go`/`pr_test.go` alone. Task 3 owns everything #66 touches:
the reworded invariant in `commands/takt.md`, the new `internal/hosts` substitution profile and
renderer, the `hostgen` wiring, the regenerated `SKILL.md`, the new `crossHostInvariants` anchor
and the render-equals-committed parity test. Task 4 owns the #72 prose and, running last, carries
the suite-wide verification.

## Tasks

### Task 1 — `BuildShipped` derives an empty commit's tasks; `waveTimings` de-duplicates (implement; G1, G2, G3)

`BuildShipped` gains the close records: `BuildShipped(events []bundle.Event,
closes []wave.CloseResult, idx plan.Index) []ShippedRow` (`internal/finish` already imports
`internal/wave`). A `wave_committed` event with a non-empty `tasks` list is untouched — the event
wins. An empty list falls back, in order, to every task id in the close record with the same
`(wave, slice, attempt)` — **unfiltered by status**, because `takt waive` writes only
`state.Tasks[i].Status` and the record keeps each task's pre-waiver verdict (the `lets-work-on-69`
record for wave 2 slice 1 is `attempt 3, committed: true, tasks: [{task 3, status: rework}]`) —
then to the `wave_dispatched` event with that key, then to today's `—`. `waveTimings` collapses to
one span per `(wave, slice, attempt)`, the last `wave_closed` in log order winning, which keeps a
reworked wave's two attempts and a sliced wave's two slices apart (different keys) while a
waived-then-re-closed wave appears once. The one caller, `internal/cli/retro.go:105`, already has
`closes` in scope and just passes it. The eighth golden is built from a
dispatched-failed-waived-re-closed wave with its inputs derived through `BuildRetroInputs` and
`BuildShipped` — not hand-written — and its close record carries the waived task at `rework` with
`committed: true`, so a status filter reintroduced later fails it. The seven existing goldens pass
`nil` closes and are byte-unchanged. Risk: the golden is a byte-for-byte document; deriving it
through the builders rather than writing `WaveTimings` by hand is what keeps it honest and is
required by G3.

### Task 2 — `BuildPR` renders `## Issues` (implement; G4)

A self-contained change to `pr.go` and its tests. The references come from the `topic` parameter
`BuildPR` already receives (never the slug — `deriveSlug` destroys the `#`), in three forms tried
as one ordered alternation: `owner/repo#N`, `https?://…/issues/N`, bare `#N` with boundary rules.
Go's RE2 has no lookbehind, so the bare form's "not preceded by `/` or a word character" is checked
against the byte before each candidate match rather than in the pattern — a named risk, covered by
the `#71b`, `owner/repo#N` and `/issues/12` rows of the table test. De-duplication is by rendered
token in topic order; the keyword repeats per reference (`Closes #66, closes #71, …`) because
GitHub links only the first issue of a bare comma list — the test asserts keyword count equals
reference count, which is the assertion that fails if that regresses. No section at all when the
topic names no issue; the sentence drops its `## Goals` clause when `gs == nil`. Existing `BuildPR`
tests use topics that name no issue, so they pass unchanged.

### Task 3 — the invariant, the anchor, and the skill generator (implement; G5, G6)

The invariant is reworded in `commands/takt.md` so the exception is the op — the `push_pr` run op
and an `archived` stop's confirmed `cleanup` — keeping the `` never run `git add -A` `` substring
both existing tests anchor on. `crossHostInvariants` gains the push-clause anchor.
`hosts/copilot/skills/takt/SKILL.md` becomes generated: `internal/hosts` gains an ordered
substitution profile (exact `from → to` strings with a declared multiplicity each) covering exactly
the 11 host-specific regions of spec §2.3's table, and `RenderCopilotSkill` errors when a `from`
matches a different number of times than declared, naming the substitution and both counts — the
single drift-alarm contract. Two ordering facts the implementer must respect: `commands/takt.md`
holds **three** `AskUserQuestion` occurrences, so the ask-bullet opening-clause substitution must
run before the `AskUserQuestion → ask_user` swap that declares 2; and the profile strings must be
byte-exact copies of the committed files, so a full render equals the committed `SKILL.md` (the
only diff after regeneration is the reworded invariant flowing through as shared text). `hostgen`
renders and `--check`s the skill exactly as it does the agents; its existing throwaway-tree tests
are seeded with the repository's real `commands/takt.md` and manifest so hostgen can stay strict
(a missing source is `exitFailure`, never a skip). `internal/tools/setversion` already rewrites the
skill's `--expect` line on a version bump and stays compatible — both it and hostgen derive the
same version from `.claude-plugin/plugin.json` — so it is deliberately not touched. Risk: the
whole task is byte-exact string work; the count-mismatch error naming the substitution is what
makes a slip diagnosable rather than silent, and the render-equals-committed parity test plus
`task hosts:check` gate the result. Both halves of hostgen's declared failure contract are
tested, not one: a root missing `commands/takt.md` and a root missing `.claude-plugin/plugin.json`
each return `exitFailure` with the missing path named in the message, asserted against `run`'s
writers rather than the process.

### Task 4 — the stale prose, and the whole-branch gate (docs; G7, G8, G9)

Comments and design prose only — G9 requires the `archive.go`/`cmd_done.go` diff to be comments
only, and the spec states no executable code changes for #72. `applyAndStop`'s comment stops
naming the deleted `plainOp` and stops calling the archive commit the run's last; §4.7's Commits
bullet, §7.5 step 5 (three sentences) and §5.1's `takt retro --rewrite` row are restated against
the code as it stands — step 5 now names the post-archive `takt(<slug>): retro done` commit, which
is where `doneRetro`'s repointed citation lands, and the §5.1 row describes `cmdRetro`'s
`lockBlocked` failure (holder, heartbeat, `takt unlock` hint) instead of `next`'s `ask: owner`.
The new prose avoids the phrase "the run's last" entirely so the absence greps are meaningful.
This run's own bundle under `docs/takt/` quotes the old text and is out of scope per G7 — the
tree-wide `plainOp` sweep uses `--include='*.go'`, which excludes it by construction.

Three checks close the plan review's findings on this task. G9's comments-only constraint is
*proved*, not assumed: a scoped filter over `git diff main -- internal/cli/archive.go
internal/cli/cmd_done.go` drops the file headers and every `+`/`-` line that is a comment or blank,
and requires nothing to be left — an executable edit slipped into either file fails the task even
though the build, the suite, the lint and every content grep still pass. `go vet ./...` is run
explicitly rather than assumed inside `task lint`. And each absence grep is paired with a positive
one, because deleting a clause satisfies an absence check on its own: `applyAndStop`'s rewritten
comment must contain "holding the lock" — the fact that replaces the retired `plainOp` sentence,
that every caller reaches it holding the lock and prints through the caller's `emit` — and
`doneRetro`'s comment must still cite "design §7.5 step 5" once "already contemplates" is gone.

The task runs last (depends on 1–3) and its verify closes the run: `task test`, `task lint`,
`task hosts:check`.

Class justification: task 4 is `docs` because every change it makes is a comment or a design-doc
paragraph — the spec forbids it executable changes — and the suite commands it carries verify the
assembled branch, not new logic of its own. No task is `mechanical` or `bounded`: tasks 1–3 each
add new logic (a derivation order, a parser, a generator) that needs judgement beyond rote edits.

## Waves and risks

Tasks 1, 2 and 3 share no files and can run as one wave; task 4 depends on all three so the suite
gate sees the finished branch. The main cross-task risk is prompt-text coupling in task 3 (three
committed artifacts — `commands/takt.md`, `SKILL.md`, the anchors in `prompt_test.go` — must agree
byte for byte), which is exactly the class of defect the generator ends; its own tests, the parity
tests and `task hosts:check` all gate it. Task 1's risk is the golden's bytes; task 2's is RE2
boundary handling; task 4's is prose that satisfies the greps without matching the code — mitigated
by naming the exact functions (`cmdRetro`'s `lockBlocked`, `doneRetroChecks`) each paragraph must
be read against, and by the comments-only diff filter, which fails the task if prose work spills
into code.
