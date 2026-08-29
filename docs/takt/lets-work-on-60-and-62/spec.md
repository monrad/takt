# Spec — issues #60 and #62

Two independent defects in takt's own tooling, both surfaced by the PR #56 run.

- **#60** — the backend review deadline defaults to 5 minutes, which is too short for a
  large task diff; a timeout costs a `review_error` gate round-trip, and the question
  never says what the deadline was or which key changes it.
- **#62** — for the `pr` disposition, `finish/pr.md` and the run's last two commits
  (`push_pr done`, `archive`) are never pushed, so the pull request is missing the very
  body it was created from and the archived state.

They share no files and can be implemented in either order.

*Revised after the spec review (`reviews/spec.md`, verdict rework, 8 findings). The
substantive change is Part A: the deadlines that wrap a backend call become derived
budgets rather than fixed constants. Finding-by-finding disposition is in the last
section.*

## Part A — #60: the backend review deadline

### A1. Raise the shipped default to 15m

`internal/config/config.go:141` — `defaultBackendTimeout` 5m → 15m. It is the shipped
value for both `backends.copilot.timeout` and `backends.claude.timeout`; per-backend
override through config stays exactly as it is.

`internal/backend/run.go:19` — `defaultTimeout` (the fallback applied when a
`ReviewRequest` leaves `Timeout` unset) 5m → 15m.

`internal/backend` deliberately imports no other takt package — its `waitDelay`
constant carries the comment "mirrors gitx.WaitDelay without importing it" — so the two
defaults stay two constants in the house style, and a test in `internal/cli` (which
imports both) asserts they are equal, so they cannot drift.

10m is the floor that would have covered the observed run; 15m is chosen for headroom
on a diff larger than the one that failed.

### A2. Derive the deadlines that wrap a backend call

The three deadlines that bound work containing backend calls are fixed constants today,
and two of them are already unsound *independently of #60*:

- `closeWaveTimeout` (30m, `internal/cli/cmd_close_wave.go:30`) budgets nothing.
  `verify_timeout` is **per command** (`internal/wave/verify.go:28-34` loops commands
  serially) and `taskOutcome` runs **serially across every task in the wave**
  (`internal/cli/cmd_close_wave.go:215-227`). A wave of 8 tasks with 2 verify commands
  each can spend 8 × 2 × 10m = 160m in verify alone, before any review.
- `reviewTimeoutS` (900s = *exactly* 15m, `internal/decide/decide.go:265`) would equal
  the new backend deadline, so the session would abandon `takt review spec|plan` at the
  instant the backend hits its own.
- `closeTimeoutS` (1800s, `internal/decide/decide.go:266`) is the session's deadline for
  a command whose own cap is `closeWaveTimeout`; equal or smaller means the session
  kills the binary before it can report its own timeout as a result.

The codebase already has the right idiom in two places and simply never applied it to
the close: `cmd_verify.go:111` bounds a run at `per × len(cmds) + verifyMargin`, and
`cmd_review.go:176` bounds a gate review at `be.Timeout + reviewGrace` — whose comment
even says it mirrors "how `closeWaveTimeout` bounds the per-task reviews: takt's
deadline must not fire before the backend's". `closeWaveTimeout` does not do that. This
section finishes the pattern rather than inventing one.

#### A2.1 New package `internal/deadline`

Standard library only, importing no takt package — which is what lets both
`internal/decide` (which imports only `bundle` and `op`) and `internal/cli` use it.

```go
// Budget is the work one close-wave has to fit into.
type Budget struct {
	VerifyTimeout  time.Duration // per verify command (config.verify_timeout)
	VerifyCommands int           // verify commands across the wave's done tasks
	BackendTimeout time.Duration // per backend call (backends.<name>.timeout)
	ReviewTasks    int           // tasks that get a backend review
	MaxParallel    int
}

func Close(b Budget) time.Duration                       // the binary's cap for close-wave
func Verify(per time.Duration, cmds int) time.Duration   // the binary's cap for takt verify
func GateReview(backend time.Duration) time.Duration     // the binary's cap for takt review
func Session(inner time.Duration) time.Duration          // what the session honours for any of them
```

- `Close(b)` = `max(Floor, VerifyTimeout×VerifyCommands + 2×BackendTimeout×ceil(ReviewTasks/MaxParallel) + Overhead)`.
  Verify is not divided by `MaxParallel` because it is serial; reviews are, because
  `reviewTasks` fans out to `MaxParallel` goroutines
  (`internal/cli/cmd_close_wave.go:672-680`). The `2×` is the blind pass plus the
  possible `scopedTaskReview` second pass a single task can trigger.
- `Session(inner)` = `inner + SessionMargin`, strictly greater for every input, so the
  binary always reports its own timeout as a result rather than being cut off.
- Constants: `Overhead` 2m (scope, git, result serialization, process start),
  `SessionMargin` 5m, `Floor` 10m (a one-task wave still gets slack),
  `Bootstrap` 2m (see A2.2), and `Grace` 30s — the value `internal/cli`'s `reviewGrace`
  holds today, moved here and that constant deleted. It has to move: `internal/decide`
  computes the session deadline for `exec takt review` as
  `Session(GateReview(backendTimeout))`, and it cannot see an unexported constant in
  `internal/cli`. One owner for the arithmetic means one owner for its terms.
- **Saturating arithmetic over a declared domain.** `time.Duration` is an int64 of
  nanoseconds, so `x + SessionMargin` and `VerifyTimeout × VerifyCommands` can overflow
  and wrap negative — which would make `Session(x) > x` and the `Close` work bounds
  false for inputs near the maximum. Every function therefore computes with saturating
  add and multiply: a sum or product that would exceed `MaxDuration`
  (`time.Duration(math.MaxInt64)`, ~292 years) yields `MaxDuration` instead of wrapping,
  and negative inputs (a `verify_timeout` of `-1h`, a count below zero) are clamped to
  zero before anything else. The contract the invariants are stated over is then:
  **`Session(x) > x` for every `x` in `[0, MaxDuration - SessionMargin)`, and
  `Session(x) == MaxDuration` at or above it** — the one point where strict containment
  is unrepresentable rather than merely unmet, and where a deadline that large is
  indistinguishable from no deadline anyway. The same shape applies to `Close` and
  `Verify`: their lower bounds hold across the representable domain and saturate at
  `MaxDuration` beyond it.

- **No ceiling.** Every inner unit is already individually bounded — each verify command
  by `verify_timeout`, each backend call by `backends.<name>.timeout` — so `Close` is a
  sum of bounded parts plus overhead, tight by construction. Its job is a backstop
  against a hang (design §12: "a timeout is a result, never a hang"), and it still is
  one.

#### A2.2 Call sites

| site | now | after |
| --- | --- | --- |
| `internal/cli/cmd_close_wave.go:50` | `context.WithTimeout(…, closeWaveTimeout)` before `openTarget` | `openTarget` under `deadline.Bootstrap`; then `closeWave` under `deadline.Close(budget)`, built once state and the plan index are known |
| `internal/cli/cmd_verify.go:111` | `per*len(cmds) + verifyMargin` inline | `deadline.Verify(per, len(cmds))` — same arithmetic, one owner |
| `internal/cli/cmd_review.go:176` | `be.Timeout + reviewGrace` inline | `deadline.GateReview(be.Timeout)`; `reviewGrace` is deleted and its value becomes `deadline.Grace` |
| `internal/decide/decide.go:265-266`, `internal/decide/finish.go:38` | `reviewTimeoutS`, `closeTimeoutS`, `verifyTimeoutS` constants | deleted; each `exec` op emits `deadline.Session(<the matching cap>).Seconds()` |

`internal/decide` gets the inputs through `Facts`, the established pattern for
config-derived durations (`LockTTL`, `WaveStaleAfter`, `internal/decide/decide.go:153`):

- `Facts.BackendTimeout`, `Facts.VerifyTimeout` — filled in `gatherFacts`
  (`internal/cli/facts.go:53`) from `ws.Cfg`.
- `WaveFacts.VerifyCommands`, `WaveFacts.ReviewTasks` — counted for the active wave's
  pending tasks from the plan index `gatherIndexFacts` already parses
  (`internal/cli/facts.go:98`).
- `MaxParallel` is already on `st.Config`.

`cmd_doctor.go:54` needs nothing: it builds `doctor.Options`, not `decide.Facts`, and
`doctor` never calls `Decide` — an earlier draft of this spec cited it in error.

#### A2.3 The invariants, testable in one place

Deleting the constants makes the containment relation a property of one pure function,
so it is a table test in `internal/deadline` rather than three unexported constants in
three packages that no single test can observe:

- `Session(x) > x` for every `x` in `[0, MaxDuration - SessionMargin)`;
  `Session(x) == MaxDuration` at or above that bound (saturation, never a wrap).
- `Close(b) >= b.VerifyTimeout*b.VerifyCommands` and, when `b.ReviewTasks >= 1`,
  `Close(b) >= 2*b.BackendTimeout`.
- `GateReview(bt) > bt`; `Verify(per, n) >= per*n`.
- `Close`, `Verify` and `GateReview` are monotonically non-decreasing in every input
  that adds work — `VerifyTimeout`, `VerifyCommands`, `BackendTimeout`, `ReviewTasks`,
  and `GateReview`'s argument. `MaxParallel` is the one exception and goes the
  other way: it is a divisor (reviews fan out across it), so `Close` is monotonically
  non-*increasing* in it. Requiring non-decreasing in every input without this
  exemption would be unsatisfiable for the stated formula.
- `Close(b) >= Floor` for every `b`, including the zero value and every negative
  field (clamped to zero first).
- Boundary cases are their own rows: a `Budget` whose terms saturate returns
  `MaxDuration` rather than a negative or wrapped duration, and every lower bound
  above still holds there.

### A3. Name the key and the deadline on the `review_error` gate

Today the gate reads `The reviewer failed for task(s) [12]: see waves/3/close.s1.json`
and its retry option says only "Re-run `takt close-wave`." A user who hits it twice has
to read the source to learn what the deadline was and which key changes it.

- `gatherFacts` fills a list of `{name, timeout}` from `ws.Cfg.Backends.Reviewer`, in
  preference order.
- `questionReviewError` (`internal/decide/questions.go:321`) renders them into the
  *retry* option's description: each backend's `backends.<name>.timeout` key with its
  current deadline, and that raising it in `.takt.json` is the fix when the cause was a
  timeout.
- **Only backends that have a real config key are rendered.** `backends.reviewer` is not
  validated against a closed set — the registry also holds `fake`, and unknown names are
  tolerated until selection — and `config.Backends` has `Timeout` fields for `copilot`
  and `claude` only. An entry with no corresponding field is skipped rather than
  rendered as a key that does not exist. If that leaves nothing to name, the option
  falls back to the literal `backends.<name>.timeout` with no deadline.
- No health probe: `gatherFacts` must not shell out, so it cannot know which backend
  would actually run. Naming every configured backend that has a key is accurate without
  one.
- The question text, the option set and the answer commands are unchanged — only the
  retry option's description grows.

### A4. Documentation

`README.md:151-152` and `docs/superpowers/specs/2026-08-24-takt-design.md:1133-1134`
show `"timeout": "5m"` in the config example; both become `"15m"`. Design §12's
timeout/deadline description gains the derived-budget rule.

### Out of scope for #60

The issue's optional third bullet — scaling the *backend* deadline with diff size. The
deadline is already configurable per backend, and one observed timeout does not pin the
shape of a scaling curve. (A2 scales the *enclosing* deadlines with the wave's work,
which is a different thing: it removes a defect rather than adding a heuristic.)
Confirmed with the user.

## Part B — #62: the PR misses the run's final state

### B1. Commit `finish/pr.md` when `next` writes it

`preparePushPR` (`internal/cli/cmd_next.go:1033`) writes `finish/pr.md` and then hands
the session a `push_pr` op pointing at it — but does not commit it. The file is only
swept up one step later, by `takt done --step push_pr`'s own `commitBundle`, which runs
*after* the session has already pushed. Hence `gh pr create`'s "1 uncommitted change"
warning on PR #56, and a pull request whose body is not in the branch it describes.

Fix: `preparePushPR` calls `commitBundle(ctx, r.ws, r.bdir, r.slug, "pr body")` after
`finish.WritePR`. `run` (`internal/cli/cmd_next.go:981`) takes a `ctx` and passes it
through; its only call site (`internal/cli/cmd_next.go:269`) is already inside
`loop(ctx)`.

Replay-safe by construction: the body is re-derived on every `next` that emits this op,
and `commitBundle` stages the bundle then returns `committed=false` when `HasStagedIn`
reports nothing — so identical bytes produce no commit. A body that genuinely changed
(goals assessed in between) produces a second `pr body` commit, which is correct.

### B2. Hand back the push as `cleanup` on the archived stop

`applyDisposition` (`internal/cli/archive.go:183`) returns an empty cleanup for `pr` and
`keep`. `pr` gains a case that reports the commits made after the session's push —
`push_pr done` and `archive`.

Following the rule the function already states — *every question it asks is put to git,
never to state* — the case reads git and emits nothing when there is nothing to push, so
a later `takt next` on the archived run stops offering a push the user has already run:

| git says | cleanup |
| --- | --- |
| `refs/remotes/origin/<branch>` does not exist (`Repo.CommitExists`) | `git push -u origin <branch>` |
| it exists and `<branch>` is an ancestor of `origin/<branch>` (`Repo.IsAncestor`) | none — the remote-tracking ref already contains every local commit |
| it exists and `<branch>` is **not** an ancestor | `git push origin <branch>` |
| the git read itself errors | `git push origin <branch>` |

The condition is "the branch holds commits the remote-tracking ref does not", not
"strictly ahead": a *diverged* branch also fails the ancestor test, and it should still
be offered the push — the local commits are genuinely absent remotely, the session
confirms every cleanup command with the user before running it, and a push git refuses
as non-fast-forward tells the user something true about their branch.

Both helpers already exist in `internal/gitx/git.go` (`CommitExists:262`,
`IsAncestor:277`); no new gitx method is needed. A git read that errors falls back to
emitting the push rather than failing the stop: the archive has already succeeded, and a
redundant suggestion the user is asked about costs nothing next to the missing push this
issue is about.

The session side needs no change: the op table already says an `archived` stop's
`cleanup` is shown to the user and confirmed before anything runs, which keeps network
git in the session (D6, §4.7).

### B3. Documentation

`docs/superpowers/specs/2026-08-24-takt-design.md` §7.5 step 5 ends "`pr` and `keep` ask
git for nothing at this step" — no longer true. It becomes: `keep` asks git for nothing;
`pr` asks whether the branch holds commits the remote-tracking ref does not, and hands
back the push when it does. "That commit is the run's last one" is unaffected — the push
is a cleanup command, not a commit.

## Testing

Test-first per task; `task check` (build + `go test ./... -race -count=1` + lint + host
parity) is the gate.

- `internal/deadline` — the A2.3 invariants as a table test, plus the worked examples
  (a 1-task wave floors at `Floor`; an 8-task × 2-command wave exceeds today's 30m).
- `internal/config` — defaults assert 15m for both backends.
- `internal/cli` — `config.Defaults().Backends.Copilot.Timeout` equals
  `internal/backend`'s unset-`Timeout` fallback, asserted by driving a `ReviewRequest`
  with no `Timeout` through the fake reviewer, so the two constants cannot drift.
- `internal/decide` — the `exec` ops for close-wave, verify and gate review carry
  `timeout_s` derived from the facts, and each strictly exceeds the binary's own cap for
  the same work; `review_error`'s retry option names `backends.<name>.timeout` and the
  deadline for each configured backend that has a key, skips one that does not, and
  degrades to the literal key when that leaves none.
- `internal/cli` — a `next` that emits `push_pr` leaves `finish/pr.md` in HEAD; an
  immediate replay adds no commit.
- `internal/cli` — archive under the `pr` disposition, four cases: commits not in the
  remote-tracking ref → cleanup carries `git push origin <branch>`; fully pushed → no
  cleanup; no tracking ref → the `-u` form; the git read failing → the push is still
  offered and the stop does not fail.

## Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| Scale the backend deadline with diff size (#60 bullet 3)? | No | Already per-backend configurable; one data point does not pin a curve. YAGNI. | user-confirmed |
| 15m default, or 10m to stay inside the existing envelopes? | 15m | The issue asks for 15m; A2 removes the envelope constraint that made 10m attractive. | user-confirmed |
| Fixed constants with bigger numbers, or derived deadlines? | Derived | Two of the three constants are already unsound today, independent of #60; bigger numbers move the failure rather than removing it. | user-confirmed |
| Always emit the `pr` push cleanup, or only when git says there is something to push? | Only when there is | Matches `applyDisposition`'s "ask git, never state" rule; a replayed archived `next` stops offering a push already done. | user-confirmed |
| `Overhead` / `SessionMargin` / `Floor` / `Bootstrap` values | 2m / 5m / 10m / 2m | `SessionMargin` is generous enough to cover process start and result serialization on a loaded machine; the others are the smallest values that keep a trivial wave and `openTarget` comfortable. Each is one named constant in one package, so a bad guess is a one-line fix. | assumed |
| Should `Close` have an upper ceiling? | No | Every inner unit is separately bounded, so the sum is tight by construction; a ceiling would reintroduce exactly the arbitrary constant this section removes. | assumed |
| Which backends does the `review_error` gate name? | Every entry of `backends.reviewer` that has a `Timeout` field in `config.Backends` | `gatherFacts` must not shell out for a health probe, so it cannot know which backend ran; naming a key that does not exist would be worse than naming several that do. | assumed |
| `internal/backend`'s unset-`Timeout` fallback | Raised to 15m, kept as a mirrored constant, equality asserted by a test in `internal/cli` | `internal/backend` imports no takt package by house style (see its `waitDelay` comment); a test is how the mirror is enforced. | assumed |
| Commit message for the `finish/pr.md` commit | `takt(<slug>): pr body` | Matches the existing short-phrase style (`archive`, `goals amended`, `<step> done`). | assumed |
| Order of implementation | Either; the two parts share no files | A touches config/backend/deadline/decide/close-wave/verify/review, B touches cmd_next/archive. | assumed |

## Review findings — disposition

All 8 findings from `reviews/spec.md` were accepted; none was overridden.

| finding | disposition |
| --- | --- |
| blocking spec.md:42 — close envelope undercounts verify (per command, serial across tasks) | A2: verified in code (`wave/verify.go:28-34`, `cmd_close_wave.go:215-227`); the fixed cap is replaced by `deadline.Close`, which budgets `verify_timeout × commands` undivided |
| blocking spec.md:53 — equal outer and inner close deadlines violate strict containment | A2.1: `Session(inner) = inner + SessionMargin`, strictly greater for every input, asserted in A2.3 |
| blocking goals.md:14 — the grep success check can never pass | goals.md G6's evidence now names `README.md` and the design doc explicitly instead of `docs/` |
| major spec.md:149 — one `internal/decide` test cannot observe three unexported constants | A2.3: the constants are deleted; the invariants become a table test over one pure function in `internal/deadline` |
| major spec.md:67 — deadline rendering undefined for non-shipped reviewer names | A3: only entries with a `Timeout` field in `config.Backends` are rendered; others are skipped; an empty result falls back to the literal key |
| major goals.md:9 — the unset backend fallback is not actually tested | A1 + Testing: an equality test in `internal/cli`, the only package importing both |
| major goals.md:13 — goals omit the specified git-read error behavior | goals.md G5 and the testing section now cover the failing-git-read case |
| minor spec.md:122 — not-an-ancestor also covers diverged histories | B2: the condition is restated as "holds commits the remote-tracking ref does not", with divergence handled explicitly |
