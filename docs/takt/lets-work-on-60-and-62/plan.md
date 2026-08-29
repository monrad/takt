# Plan — lets-work-on-60-and-62

Two independent defects (#60, #62) plus the derived-deadline rework the spec review forced.
Part A (tasks 1–5) raises the shipped backend review deadline to 15m and replaces the three
fixed session/binary deadlines with budgets derived from the run's config and plan, owned by
a new stdlib-only package `internal/deadline`. Part B (tasks 6–7) commits `finish/pr.md`
before the session pushes and makes the archived `pr` stop hand the missing push back as
`cleanup`. Task 8 sweeps the documentation and carries the whole-repository gate (G9).

Dependency shape: tasks 1, 2, 6 and 7 are independent and can run together. Tasks 3 and 4
both need the deadline package (task 2) and the config accessors (task 1) but touch disjoint
files, so they can run together after those. Task 5 shares `internal/decide/decide.go`,
`internal/decide/decide_test.go` and `internal/cli/facts.go` with task 4 and runs after it.
Task 8 runs last so its `task check` verifies the assembled branch.

Everything is test-first; `task check` (build + `go test ./... -race -count=1` + lint +
host parity) is the finish gate. No task commits — takt owns every commit.

## Corrections found while surveying (spec vs. tree)

- The spec says `cmd_doctor.go:54` "builds `Facts` too". It does not: it builds
  `doctor.Options` (a different type in `internal/doctor` that never feeds `decide.Decide`).
  No doctor change is needed; task 4 confines the new fields to `decide.Facts` and
  `internal/cli/facts.go`.
- The spec keeps `reviewGrace` as a constant in `cmd_review.go` passed to
  `deadline.GateReview`. The decide side needs the same grace to compute a session deadline
  that provably contains the binary's cap, so the constant moves into `internal/deadline`
  as exported `Grace` (30s, value unchanged) and `cmd_review.go` passes `deadline.Grace`.
  This serves the spec's own stated goal — the containment relation as a property of one
  package — better than a mirrored constant would.
- G3 says the functions are "monotonically non-decreasing in every input". For
  `Budget.MaxParallel` that is the wrong direction by construction: more parallelism can
  only shrink the review term. The invariant test asserts non-decreasing in the four work
  inputs (`VerifyTimeout`, `VerifyCommands`, `BackendTimeout`, `ReviewTasks`) and
  non-increasing in `MaxParallel`, which is the sound reading.
- G1's evidence wants the unset-`Timeout` fallback observed "through the fake reviewer".
  Today only `runCLI` applies the fallback and the fake never calls it. Task 1 adds a
  shared `resolveTimeout` helper (used by `runCLI` unchanged in behaviour) and a small
  env-gated seam on the fake (`TAKT_FAKE_REVIEW_TIMEOUT_FILE`: the fake resolves
  `req.Timeout` through the same helper, applies it to its context, and records the
  resolved value), so an `internal/cli` test can drive a `ReviewRequest` with no `Timeout`
  and read back the deadline that was actually applied.
- "Which backend's timeout budgets a close" needs one owner used by three tasks (3, 4, 5).
  Task 1 adds two small accessors to `internal/config`: `Backends.Timeout(name)` (the
  config key for `copilot`/`claude`, reported absent for `fake` or unknown names — exactly
  the A3 skip rule) and `Backends.ReviewBudgetTimeout()` (the largest timeout among the
  configured `backends.reviewer` entries that have keys; when none qualifies, the larger of
  the two shipped fields, so a fake-only chain still budgets something sane).

## Tasks

### 1. Raise the shipped backend deadline to 15m; pin the mirrored fallback (implement, G1)

`internal/config/config.go:141` `defaultBackendTimeout` 5m→15m and
`internal/backend/run.go:19` `defaultTimeout` 5m→15m — two constants in the house style
(`internal/backend` imports no takt package, per its `waitDelay` comment). To keep them
from drifting: `config.TestDefaults` grows the 15m assertions for both backends, and a new
`internal/cli/backend_timeout_test.go` (the one package importing both sides) drives a
`ReviewRequest` with no `Timeout` through the fake reviewer using the new
`TAKT_FAKE_REVIEW_TIMEOUT_FILE` seam and asserts the applied deadline equals
`config.Defaults().Backends.Copilot.Timeout` (and Claude's). The task also adds the two
config accessors described above, with table tests, because tasks 3–5 all need "which
reviewer entries have a real key" answered in one place. Scoped to exactly the two
constants, the seam, and the accessors — no call-site changes.

### 2. New package internal/deadline (implement, G2 G3 G4)

`internal/deadline/deadline.go` + `deadline_test.go`, stdlib only, importing no takt
package (which is what lets both `internal/decide` and `internal/cli` use it). The spec's
API verbatim: `Budget`, `Close`, `Verify`, `GateReview`, `Session`; constants `Overhead`
2m, `SessionMargin` 5m, `Floor` 10m, `Bootstrap` 2m, plus `Grace` 30s (moved here, see
above) and an unexported 30s verify margin inside `Verify` (the arithmetic
`cmd_verify.go:111` owns today). `Close(b) = max(Floor, VerifyTimeout×VerifyCommands +
2×BackendTimeout×ceil(ReviewTasks/MaxParallel) + Overhead)` with `MaxParallel < 1` treated
as 1; verify is deliberately undivided (per-command, serial across tasks —
`wave/verify.go:28-34`, `cmd_close_wave.go:215-227`), reviews divide because
`reviewTasks` fans out to `MaxParallel` goroutines, and the 2× covers the scoped second
pass. No ceiling: every inner unit is separately bounded. The A2.3 invariants land here as
table tests, including the zero-value budget (floors at 10m), a 1-task no-work wave
(floors), and the 8-task × 2-command worked example that exceeds today's 30m. This is a
whole task because the invariants are the load-bearing artifact: three call-site tasks
lean on them instead of re-proving containment each.

### 3. Binary-side call sites derive their caps (implement, G2; after 1, 2)

`cmd_close_wave.go`: the single 30m `closeWaveTimeout` context splits into `openTarget`
under `deadline.Bootstrap`, then `closeWave` under `deadline.Close(budget)` built once
state and the plan index are known; the constant and its comment go. The budget is built
by a pure helper `closeBudget(cfg, st, idx) deadline.Budget` — `VerifyTimeout` from
config, `VerifyCommands` = Σ `len(idx.Task(id).Verify)` over the active wave's pending
tasks (`t.Wave == aw.N && Status == pending`, the set `resolveTaskResults` actually
grades), `BackendTimeout` = `cfg.Backends.ReviewBudgetTimeout()`, `ReviewTasks` = that
same task count when `st.Config.Review.Tasks` else 0, `MaxParallel` from `st.Config` —
unit-tested in a new package-internal `internal/cli/close_budget_test.go` (an 8×2 wave
exceeds 30m; review off zeroes the term). `cmd_verify.go:111` becomes
`deadline.Verify(per, len(cmds))` and the local `verifyMargin` goes; `cmd_review.go:176`
becomes `deadline.GateReview(time.Duration(be.Timeout), deadline.Grace)` and the local
`reviewGrace` goes (its doc comment's cross-reference moves with it). Task 4 must count
the same wave set, which is why both descriptions pin "pending tasks of the active wave"
— the session's `Session(Close(b))` strictly contains the binary's `Close(b)` only when
both compute the same `b`.

### 4. Decide-side session deadlines derived from facts (implement, G2 G4; after 1, 2)

Deletes `reviewTimeoutS`/`closeTimeoutS` (`decide.go:264-267`) and `verifyTimeoutS`
(`finish.go:38`). Each `exec` op emits `int(deadline.Session(cap).Seconds())` where cap is
the matching binary cap: `GateReview(f.BackendTimeout, deadline.Grace)` for
`takt review spec|plan`, `Close(budget)` for `takt close-wave` (budget from
`f.VerifyTimeout`, `f.Wave.VerifyCommands`, `f.BackendTimeout`, `f.Wave.ReviewTasks`,
`st.Config.MaxParallel`), `Verify(f.VerifyTimeout, f.Finish.VerifyCommands)` for
`takt verify`. Inputs arrive through `Facts`, the established pattern for config-derived
durations (`LockTTL`, `WaveStaleAfter`): `Facts.BackendTimeout` (via
`ReviewBudgetTimeout()`) and `Facts.VerifyTimeout` filled in `gatherFacts`;
`WaveFacts.VerifyCommands`/`WaveFacts.ReviewTasks` counted in `gatherWaveFacts` over the
active wave's pending tasks from the plan index `gatherIndexFacts` already parses (it now
hands the parsed index along instead of discarding it); `FinishFacts.VerifyCommands` =
`len(finish.UnionCommands(idx, extra))` in `gatherFinishFacts`, the same union
`verifyAtHead` runs. Tests in `internal/decide` assert each emitted `timeout_s` equals the
`Session` of the matching cap and strictly exceeds the cap itself (G4's evidence). No
doctor change (see corrections).

### 5. review_error names the key and the deadline (implement, G5; after 4)

`Facts` gains `ReviewerBackends []ReviewerBackend` (`{Name, Timeout}`), filled in
`gatherFacts` from `ws.Cfg.Backends.Reviewer` in preference order through
`Backends.Timeout(name)` — entries with no config key (`fake`, unknown names) are skipped,
no health probe, no shelling out. `decideActiveWave`'s `review_error` ask (decide.go:451)
adds a pre-rendered, JSON-round-trip-stable `"backends"` context entry;
`questionReviewError` (questions.go:321) renders it into the retry option's description
only: each backend's `backends.<name>.timeout` key with its current deadline and that
raising it in `.takt.json` is the fix when the cause was a timeout; when the list is empty
it falls back to the literal `backends.<name>.timeout` with no deadline. Question text,
option set and answer commands unchanged. Tests cover the three shapes G5 names:
a configured set, a set containing a keyless backend (skipped), and an empty set
(literal fallback).

### 6. Commit finish/pr.md when next writes it (bounded, G6)

`preparePushPR` (`cmd_next.go:1033`) calls
`commitBundle(ctx, r.ws, r.bdir, r.slug, "pr body")` after `finish.WritePR`; `run`
(`cmd_next.go:981`) takes a `ctx` and its single call site (`cmd_next.go:269`, already
inside `loop(ctx)`) passes it. Replay-safe by construction: `commitBundle` stages and then
reports `committed=false` when nothing is staged, so identical bytes make no commit and a
genuinely changed body makes a correct second `pr body` commit. The test extends
`finish_test.go`'s existing push_pr drivers: drive to the op, assert `finish/pr.md` is in
HEAD and the subject is `takt(demo): pr body`, replay `next` and assert HEAD unchanged.
Class bounded: the exact call, message, plumbing route and test are fully specified by the
spec; no design decisions remain.

### 7. pr archive hands the push back as cleanup (implement, G7)

`applyDisposition` (`archive.go:183`) gains a `dispositionPR` case that follows the
function's stated rule — every question is put to git, never to state:
`refs/remotes/origin/<branch>` missing (`Repo.CommitExists`) → `git push -u origin
<branch>`; present and `<branch>` an ancestor (`Repo.IsAncestor`) → no cleanup; present
and not an ancestor (ahead or diverged — the local commits are genuinely absent remotely)
→ `git push origin <branch>`; either git read erroring → the push is offered and the stop
still succeeds (the archive already landed; a redundant confirmed suggestion costs
nothing). The function's doc comment and archive.go's "pr and keep ask for nothing" prose
are rewritten. Tests: `archive_test.go` gets a pr-disposition flow over a local bare
`origin` walking three states in sequence (no remote → the `-u` form; after running the
push → no cleanup on the next `next`; a fresh commit on the branch → the plain push, run
verbatim via `runShell` and then gone again); a new package-internal
`archive_internal_test.go` injects the git-read failure by calling `applyDisposition` with
an already-cancelled context (a non-ExitError, the only kind `CommitExists`/`IsAncestor`
surface as errors) and asserts the push is still offered and no error fails the stop.
Session side needs no change (the op table already confirms `cleanup` with the user).

### 8. Documentation and the whole-repository gate (docs, G8 G9; after everything)

`README.md:151-152` and the design doc's §12 config example (`:1133-1134`): `"timeout":
"5m"` → `"15m"`. Design §12's timeout bullet gains the derived-budget rule, naming
`internal/deadline`. Design §7.5 step 5 (`:886`) — "`pr` and `keep` ask git for nothing at
this step" — becomes: `keep` asks git for nothing; `pr` asks whether the branch holds
commits the remote-tracking ref does not, and hands the push back as `cleanup` when it
does ("that commit is the run's last one" stands — the push is a cleanup command, not a
commit). The §5.2 sentence at `:480-481` ("a `keep` or a `pr` archive asks git for nothing
and carries neither") is corrected the same way — it contradicts task 7 exactly as §7.5
did. Class docs: prose only. This task runs last and its verify includes `task check`,
which is G9's evidence on the finished branch; the greps are the commands that fail before
this task's own work.

## Risks

- **Deadline containment depends on both sides computing the same budget.** Mitigated by
  pinning the counted set ("pending tasks of the active wave", the plan-index `Verify`
  lists, `ReviewBudgetTimeout()`) identically in tasks 3 and 4, and by `SessionMargin`
  being strictly positive so a small counting divergence still leaves the session outside
  the binary.
- **Long-running cli test package.** Tasks 3, 6, 7 all exercise `./internal/cli/` with
  `-run` filters to keep verify fast; the full suite runs once in task 8's `task check`.
- **The fake-reviewer seam.** It must not disturb the existing `TAKT_FAKE_REVIEW_CALLS`
  line format (`strings.Cut` parsing in oploop_test.go); the resolved deadline therefore
  goes to its own env-gated file, not onto the call-log line.
- **`git push` in tests.** Only ever against a local bare repository created by the test;
  no network. The `-u` form is asserted as a string (no remote exists in that state); the
  plain form is run verbatim through `runShell`, house style.

## Class justifications (below implement)

- **Task 6 (bounded):** the spec names the exact function, call site, commit message,
  plumbing route and replay property, and the test is specified; nothing is left to
  design.
- **Task 8 (docs):** prose-only edits to README and the design doc; the code it describes
  is fixed by tasks 1–7. It additionally carries `task check` as the last task so G9 is
  evidenced on the assembled branch.
