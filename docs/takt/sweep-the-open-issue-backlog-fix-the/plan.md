# Plan — sweep the open-issue backlog (eighteen issues, one run)

## Approach

Thirteen tasks, grouped by the files they must edit rather than by issue number,
because the spec's eighteen issues land on a handful of hot files:
`internal/cli/cmd_review.go` is wanted by #44, #43 and #45; `internal/cli/cmd_close_wave.go`
by #53, #23 and #51; `internal/cli/cmd_next.go` by #36 and #51; `internal/decide/questions.go`
by #43, #26, #45 and #36; `internal/brief/brief.go` by #31, #45 and #36. A per-issue split would
serialise the sweep on those files, so each hot file has exactly one owner per wave and the
issues that touch it ride along. Waves are computed by takt from `depends_on`; the file sets of
tasks without an ordering edge are disjoint (takt's own `plan-disjoint` rule). The graph gives
two waves:

- **Wave 1** — T1, T2, T3, T4, T5, T6, T7, T8, T9. Nine independent tasks, no shared files
  (`max_parallel` is 8, so takt will run them as two slices).
- **Wave 2** — T10 (after T1 and T2), T11 (after T1), T12 (after T6), T13 (after T1).

Every task carries cheap tripwire verifies (a `grep` for a symbol, test name or phrase the task
introduces, each failing on the current tree) plus the package tests the spec names, and a
scoped `golangci-lint run` on the packages it edits — the repo's lint config is strict (funlen
100/50, gocognit 20, mnd, godot, paralleltest, testpackage, nolintlint with required
explanations), and catching it per task is cheaper than at finish. T10 and T12 also carry the
repo-wide gates (`go test -race ./...`, `golangci-lint run ./...`, `task hosts:check`) as fast
feedback for G13; what proves G13 is `takt verify` at finish, which runs the union of every
task's verify commands on the assembled tree after the last wave has committed.

Everything below was verified against the tree at `55e4431` (this branch's HEAD at planning
time); line numbers are where the code stands today, not a contract.

Two naming corrections to the spec, applied throughout: the brief data struct is
`brief.ImplementerData` (the spec says `brief.TaskData`), and every constructor of a wave
follow-up lives in `record_reviewer.go` (`carryUnattributed`) and `cmd_close_wave.go`
(`carryInternalOnly`, `carryTaskFindings`) — three sites, as the spec counts.

## Tasks

### T1 — follow-ups.json: an honest wave, an identity, and single-write findings files (#53, #44 identity, #51 `writeTaskFindings`) — `implement`

`gate.FollowUp.Wave` becomes `*int` (still `json:"wave,omitempty"`): nil is a gate
follow-up, `&n` a wave-`n` one, so wave 0 serialises as `"wave": 0` instead of vanishing.
`FollowUp.Key()` is the JSON encoding of `[gate, wave, task, severity, file, line, title]` —
`wave` as `null` when nil, strings trimmed — which is injective because JSON escapes every
delimiter a file name or title could smuggle in. `AppendFollowUps` keeps its read-modify-write
shape but becomes idempotent on that key, with the one upgrade the spec allows: a stored
`approve` item met by an `override` repeat has its `source` rewritten in place, `ts` kept;
nothing else is ever rewritten and nothing is removed. The three wave-follow-up constructors set
the pointer (`new(rec.Wave)`, `new(waveN)` — `new(expr)` is already used in this tree). This task
also owns the two findings-file writers because both files are already its own: `writeFindings`
(cmd_review.go) is split into `renderFindings` + one `bundle.WriteFileAtomic` (which creates
the directory, so its `MkdirAll` goes), and `writeTaskFindings` (cmd_close_wave.go) builds the
whole document from `renderFindings` plus its two sections and writes it once through
`bundle.WriteFileAtomic` — no more write-then-`O_APPEND`. Two test files must change for the
pointer to compile (`assertApproveFollowUps`, `TestRecordVerifyWritesInternalRecordAndCarriesUnattributed`);
T13 later strengthens them, which is why T13 depends on this task. Seven files.

### T2 — decide: the error-verdict question, a recommendation the user can choose, "twelve" (#43.2 question half, #26, #45, #36 option text) — `implement`

`questionGateReview` gains an `error` branch: narration `<g> review errored`, question "The
<g> review errored: <reason>. reviews/<g>.md still describes the previous pass. How do you want
to proceed?", options `retry` (Recommended: "Re-run the reviewer: `takt review <g> --slug
<slug>`, then `takt next`"), `accept`, `stop` — no `revise`, since nothing was reviewed. The
rework/reject wording is untouched. `GateStatus` gains `Reason` and both `gate_review` asks
(`decideBrainstorm`, `decidePlan`) carry `"reason"` in their context. `questionBranchFinish`
orders `pr` (Recommended), `keep`, `merge` (disabled, with its reason), `discard` when merge is
blocked, and leaves the allowed order alone; exactly one option ever carries "(Recommended)"
and it is first and enabled — the `pr` description now names `--title '<title>' --body-file
<path>` instead of `--fill`. The doc comment's "eleven ids" becomes "twelve" in both places.
Tests in the two decide test files; the existing error case of
`TestGateReviewTellsTheUserWhatReviseWillActuallyDo` moves to the new test. The scripted
op-loop driver already picks the first *enabled* option, so the reorder changes no existing
loop test. Four files; T10 depends on it for `GateStatus.Reason`.

### T3 — doctor `review-record` check (#43.3) — `bounded`

A new per-bundle check WARNs when a gate's receipt is a reviewer's answer (not `error`, not
skipped) and `reviews/<gate>.json` carries a `hash` that differs from the receipt's; a findings
file with no `hash` (written before T10) is skipped, PASS. It reads the two files itself — a
minimal `{hash}` struct beside `gate.ReadReceipt` — so it needs nothing from T10 and can run in
wave 1: the key names are fixed by the spec. Added to `doctor.Default`; three files. Class
`bounded`: the message, the fix line and the three test cases are all given.

### T4 — status in the plan phase, and the hint when the bundle lives on another branch (#33, #8) — `implement`

`statusInfo` gains `TasksPlanned` (the index's task count when no task is materialised and
`plan.index.json` parses), the text line becomes `tasks: N planned (not yet materialised)` in
that case and the JSON carries `tasks.planned`; `alignmentDigest` gains `Clauses`, `Skipped`,
`VerdictsPresent` and `alignmentLine` renders `skipped` / `N clauses awaiting confirmation` /
`N clauses confirmed, verdicts pending` / the counts, so `alignment:` is never bare.
`loadStatus` opens through `openTarget`, and `openTarget`'s `loadBundle` failure carries one
of three hints — the branch hint when the error is `fs.ErrNotExist` and `takt/<slug>` exists
(`gitx.Repo.BranchExists`), the no-run hint otherwise for `ErrNotExist`, the doctor hint for
anything else. Four files; the unlock test lives in a new `cmd_unlock_test.go` so nothing else
in this wave needs `cmd_next_test.go`.

### T5 — goal-assessor citations are checked against the tree (#24) — `implement`

`finish.CheckCitations(vs, root)` returns one problem per bad citation — grammar
(`path:line` / `path:start-end`), repo-relative path (no leading `/`, no `..` segment),
symlink-resolved containment (`filepath.EvalSymlinks` on both the joined path and the root),
regular file, `1 ≤ start ≤ end ≤` line count — and `readVerdicts` appends them to
`ParseVerdicts`'s problems so a bad citation is rejected like any unusable reply: no record, one
`goals_invalid` event. The brief template and the agent definition state the grammar and that
citations are checked; the host file is regenerated. The CLI test is a new file so
`finish_test.go` stays free for T12. Seven files.

### T6 — the task brief names the spec by path; brief-package polish (#31, #45 brief items) — `implement`

`ImplementerData.SpecExcerpt` becomes `SpecPath`; `renderImplementer` passes the bundle's
absolute `spec.md`; `implementer.md`'s Context section says to read it as data and the
`spec-excerpt` quote block is gone; `agents/implementer.md` loses its "spec excerpt" mention
and the Copilot file is regenerated. Same package, so this task also lands `PriorFindingLines`
flattening newlines inside a `Detail`, and the `review-spec-followup.md` reject clause. A new
cli test dispatches a wave and asserts the brief holds the path and not the spec's body. Eight
files; T12 depends on it because both need `brief.go` (`RunData`).

### T7 — copilot `--no-custom-instructions` (#49 item 1) — `bounded`

One flag in `copilotArgs`, pinned by `TestCopilotArgs`, with the comment saying why: the
cross-vendor reviewer must not read the project instructions the implementer followed. The
design-doc sentence is T8's. Two files.

### T8 — documentation sweep (#54, #35, #26 plan doc, #18, #49 §8.2, #45 §6, #36 §7.5) — `docs`

Four prose files, each edit fixed by the spec: design §4.6 restated by the holder (the rule the
code keys on), §8.2's command line gaining the copilot flag and its one-sentence reason, and
§7.5's `--fill` becoming the title/body-file form so the design does not contradict T12; the
hardening plan's Task 8 choosing `pr` and naming `docs/takt/<slug>/retro.md`; the fixed-point
design's §6 sentence after the table; the README's macOS quarantine paragraph under "The
binary". No code.

### T9 — the two skill files: the `push_pr` command and the absolute-path invariant (#36 op table, #37) — `bounded`

`commands/takt.md` and `hosts/copilot/skills/takt/SKILL.md` get the `push_pr` row rewritten to
`gh pr create --base <base> --title '<title>' --body-file <path>` (from `inputs.pr_title` and
`inputs.pr_body_path`) and one new Invariants bullet beside "never edit the bundle by hand":
inspect bundle files by absolute path — never `cd` into the bundle. Both sentences are added to
`crossHostInvariants` in `prompt_test.go`, so the parity test fails if either host's copy
drifts. Independent of T12's template change: the prose describes the op T12 emits, and the
spec fixes the wording. Three files.

### T10 — `runReview`'s write order, the reason, and the hash on the findings file (#44 item 3, #43.1–3 code half, #45 review items) — `implement`

Depends on T1 (owns `cmd_review.go`'s `renderFindings` and the idempotent carry the reorder
relies on) and T2 (`GateStatus.Reason`). `runReview` writes findings → carry (on approve) →
`gate_reviewed` event (now checked; exits 1 on failure) → receipt → commit, so any failure
leaves no receipt and the next call re-runs instead of returning `cachedReceipt` with the carry
lost; the function comment states the order and why. `gate.Receipt` and `gate.Status` gain
`Reason`; the event carries `reason` when non-empty; `gatherGateFacts` copies it.
`reviews/<gate>.json` gains `hash` and `round` (`gate.Rounds` before the pass, plus one);
`readReviewResult` returns them; `priorFindingsForScopedPass` is unchanged. `answerGateReview`
accepts `retry` (writes nothing; `cmdAnswer` clears and commits). `writeResultJSON` drops its
`MkdirAll` (with T1's change, `cmd_review.go` then holds exactly one — `preserveEvidence`'s),
and every verdict comparison uses `gate.Verdict*`. The `overrideGate` comment is rewritten for
the idempotent carry. Seven files. Carries the repo-wide gates.

### T11 — retro inputs: count every review once, one timing per dispatched attempt (#23, #25, #45 fixture) — `implement`

Depends on T1 (`cmd_close_wave.go`). `wave.CloseResult` gains `ReviewFindings`, computed in
`closeWave` right after `resolveTaskResults` — before `persistClose` runs `carryForward` — so
each attempt counts exactly its own graded reviews; `wave_closed` carries `review_findings` and
`slice`. `BuildRetroInputs` stops summing the close records: `ReviewFindings` is
Σ `gate_reviewed.findings` + Σ `wave_closed.review_findings` over the event log, split as
`gate_review_findings` / `task_review_findings`. `WaveTiming` gains `closed_at`, `committed`
and `committed_at` (`omitzero`), `waveTimings` pairs `wave_dispatched` with `wave_closed` by
(wave, slice, attempt) and fills the commit half from `wave_committed`, ordered by wave, slice,
attempt. `run-retro.md` names the split. `TestBuildRetroInputsCarriesFollowUps` gets its
minimal fixture; the existing timing fixtures gain `wave_closed` events. Six files.

### T12 — the pull request is written from the run; `cmd_next.go` polish (#36, #51 `cmd_next.go` items) — `implement`

Depends on T6 (`brief.go`, `brief_test.go`). A pure `finish.BuildPR` derives the title (the
spec's H1, else the topic's first 72 runes) and body (first prose paragraph, `## Goals` with
each goal's verdict / `waived (<reason>)` / `not assessed`, `## Run` bundle pointer); the
`push_pr` `run` op writes `finish/pr.md` on every call and passes `inputs.pr_title` and
`inputs.pr_body_path`; `RunData` gains the two fields and a `PRTitleQuoted` method (`'` →
`'\''`); `run-push_pr.md` says `--title '<title>' --body-file <path>`. Same file, so the three
`cmd_next.go` polish items land here: `writeStableBrief` renders once and hands
`writeStableBriefAt` the text, `verifyBrief` calls `ensureSliceDiff` before building its
closure, `lensTasks` loses its dead parameter. Eight files. Carries the repo-wide gates.

### T13 — the polish tests (#45 and #51 test items) — `test`

Depends on T1 (two of its files). `gate_test.go`: a malformed `gate_revision_accepted` (non-string
`gate`/`hash`) neither panics nor satisfies; a receipt with `Severities == nil` at the current
hash computes `Blocking == false`. `oploop_test.go`: the scoped-pass test reads the prompt by
its `review-spec-` LogID prefix instead of scanning every file under `logs/`.
`close_internal_test.go`: a marker planted in a confirmed internal finding is absent from the
*blind* task-review prompt (the twin of the scoped-pass leak test). `record_reviewer_test.go`:
`Candidates` and `Verdicts` asserted on the on-disk record; a nothing-written assertion on the
"no verdict for c2" sub-case. `cmd_answer_test.go`: `internal_review_skipped` carries
`reason: agent_invalid`. Five files, no production code.

## Risks

- **Same-worktree wave concurrency.** Wave 1 has six tasks compiling `internal/cli` (T1, T4,
  T5, T6 directly; T3 and T9 through their cli/prompt tests), so one task's verify can observe
  another's half-written edit and fail transiently; takt re-attempts, and the wave is graded on
  the committed tree. This is the accepted residual risk of the previous sweep, adopted again.
  The two `task hosts:check` verifies (T5, T6) are the most exposed: both tasks edit an agent
  definition, so each is told to run `task hosts:gen` first, which regenerates every stale host
  file — including the other task's, which is in the wave's scope.
- **T2 lands before T10.** Between waves the `gate_review` question offers `retry` on an error
  verdict while `answerGateReview` does not yet accept it. No existing test drives an errored
  gate through `next` and `answer`, and the wave-1 commit is graded by each task's own verify;
  T10 closes the gap in wave 2.
- **Failure injection in T10's write-order test** relies on a read-only `events.jsonl`
  refusing `O_APPEND` — the seam the existing streak-loss tests already use — which does not
  hold as root; the test skips when `os.Geteuid() == 0`.
- **`wave_closed` becomes load-bearing for timings.** Bundles whose `wave_closed` events
  predate `slice`/`review_findings` are floored (slice 1) or count zero, the status quo the spec
  accepts; the retro fixtures that pair only through `wave_committed` must gain `wave_closed`
  events, which T11's description calls out.
- **Prose tripwires.** T8's and T9's greps anchor on phrases the spec itself uses; they are
  tripwires against the edit landing in the wrong file, not oracles for meaning — the wave
  review and G12's assessment judge the prose.

## Class justifications (below `implement`)

- **T3 `bounded`** — a new check in a package whose shape (`Check{Name, Run}`) every sibling
  file demonstrates; message, fix line and test cases are dictated by the spec.
- **T7 `bounded`** — one flag, one assertion, wording given.
- **T9 `bounded`** — two prose files plus two string literals appended to an existing test
  table; the sentences are quoted in the spec.
- **T8 `docs`** — prose only, every passage named with its location.
- **T13 `test`** — tests against existing behaviour only; T1 owns the two shared files first.
