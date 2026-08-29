# Spec — issues #60 and #62

Two independent defects in takt's own tooling, both surfaced by the PR #56 run.

- **#60** — the backend review deadline defaults to 5 minutes, which is too short for a
  large task diff; a timeout costs a `review_error` gate round-trip, and the question
  never says what the deadline was or which key changes it.
- **#62** — for the `pr` disposition, `finish/pr.md` and the run's last two commits
  (`push_pr done`, `archive`) are never pushed, so the pull request is missing the very
  body it was created from and the archived state.

They share no files and can be implemented in either order.

## Part A — #60: the backend review deadline

### A1. Raise the shipped default to 15m

`internal/config/config.go:141` — `defaultBackendTimeout` 5m → 15m. It is the shipped
value for both `backends.copilot.timeout` and `backends.claude.timeout`; per-backend
override through config stays exactly as it is.

`internal/backend/run.go:19` — `defaultTimeout` (the fallback applied when a
`ReviewRequest` leaves `Timeout` unset) 5m → 15m, so the fallback and the shipped
default do not drift apart.

10m is the floor that would have covered the observed run; 15m is chosen for headroom
on a diff larger than the one that failed.

### A2. Raise the deadlines that wrap it

Three constants bound work that makes backend calls. Each must strictly exceed the
worst case of what it wraps, or raising A1 alone converts a backend timeout into an
outer timeout — a strictly worse failure, because the outer one kills a close mid-flight
rather than recording a per-task result.

| constant | file | now | after | what it wraps |
| --- | --- | --- | --- | --- |
| `reviewTimeoutS` | `internal/decide/decide.go:265` | 900 (15m) | 1200 (20m) | the session's deadline for `exec takt review spec\|plan`, which makes **one** backend call |
| `closeTimeoutS` | `internal/decide/decide.go:266` | 1800 (30m) | 3600 (60m) | the session's deadline for `exec takt close-wave` |
| `closeWaveTimeout` | `internal/cli/cmd_close_wave.go:30` | 30m | 60m | the binary's own cap on the whole close — scope, verify commands and every review |

Why the close needs 60m: reviews run concurrently up to `max_parallel`, but a **single**
task can make two sequential backend calls — the blind pass, then `scopedTaskReview`
when the blind verdict is `approve` while blocking internal findings stand. That is
2 × 15m = 30m for one task, and the close runs verify commands (`verify_timeout`, 10m)
before any of it.

Two invariants to encode as a test, so a future edit to one constant without the others
fails loudly:

- `reviewTimeoutS > defaultBackendTimeout`
- `closeWaveTimeout >= 2*defaultBackendTimeout + defaultVerifyTimeout`, and
  `closeTimeoutS >= closeWaveTimeout`

### A3. Name the key and the deadline on the `review_error` gate

Today the gate reads `The reviewer failed for task(s) [12]: see waves/3/close.s1.json`
and its retry option says only "Re-run `takt close-wave`." A user who hits it twice has
to read the source to learn that 5m was the deadline and that `backends.<name>.timeout`
is what changes it.

- `decide.Facts` gains the configured reviewer backends and their deadlines — the same
  pattern as the existing config-derived `LockTTL` / `WaveStaleAfter` fields
  (`internal/decide/decide.go:153`), filled in `gatherFacts`
  (`internal/cli/facts.go:53`) from `ws.Cfg.Backends.Reviewer` in preference order.
- `questionReviewError` (`internal/decide/questions.go:321`) renders them into the
  *retry* option's description: each configured backend's `backends.<name>.timeout` key
  with its current deadline, and that raising it in `.takt.json` is the fix when the
  cause was a timeout.
- No health probe: `gatherFacts` must not shell out to decide which backend would
  actually run, so the gate names every configured reviewer backend rather than
  guessing the one that failed. With none configured it falls back to the literal
  `backends.<name>.timeout` and no deadline.
- The question text, the option set and the answer commands are unchanged — only the
  retry option's description grows.

### A4. Documentation

`README.md:151-152` and `docs/superpowers/specs/2026-08-24-takt-design.md:1133-1134`
show `"timeout": "5m"` in the config example; both become `"15m"`.

### Out of scope for #60

The issue's optional third bullet — scaling the deadline with diff size (files or
hunks). The deadline is already configurable per backend, and one observed timeout does
not pin the shape of a scaling curve. Confirmed with the user.

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
`keep`. `pr` gains a case that reports the unpushed commits — `push_pr done` and
`archive`, both made after the session's push.

Following the rule the function already states — *every question it asks is put to git,
never to state* — the case reads git and emits nothing when there is nothing to push, so
a later `takt next` on the archived run stops offering a push the user has already run:

| git says | cleanup |
| --- | --- |
| `refs/remotes/origin/<branch>` does not exist (`Repo.CommitExists`) | `git push -u origin <branch>` |
| it exists and `<branch>` is an ancestor of `origin/<branch>` (`Repo.IsAncestor`) | none — everything is pushed |
| it exists and `<branch>` is not an ancestor | `git push origin <branch>` |
| the git read itself errors | `git push origin <branch>` |

Both helpers already exist in `internal/gitx/git.go` (`CommitExists:262`,
`IsAncestor:277`); no new gitx method is needed. A git read that errors falls back to
emitting the push rather than failing the stop: the archive has already succeeded, the
session asks the user before running anything from `cleanup`, and a redundant suggestion
costs nothing next to the missing push this issue is about.

The session side needs no change: the op table already says an `archived` stop's
`cleanup` is shown to the user and confirmed before anything runs, which keeps network
git in the session (D6, §4.7).

### B3. Documentation

`docs/superpowers/specs/2026-08-24-takt-design.md` §7.5 step 5 ends "`pr` and `keep` ask
git for nothing at this step" — no longer true. It becomes: `keep` asks git for nothing;
`pr` asks whether the branch is ahead of its remote-tracking ref and hands back the push
when it is. "That commit is the run's last one" is unaffected — the push is a cleanup
command, not a commit.

## Testing

Test-first per task; `task check` (build + `go test ./... -race -count=1` + lint + host
parity) is the gate.

- `internal/config` — defaults assert 15m for both backends.
- `internal/decide` — a new envelope test asserting the A2 invariants against the config
  defaults; `review_error`'s retry option names `backends.<name>.timeout` and the
  deadline for each configured backend, and degrades to the literal key with none
  configured.
- `internal/cli` — a `next` that emits `push_pr` leaves `finish/pr.md` in HEAD; an
  immediate replay adds no commit.
- `internal/cli` — archive under the `pr` disposition: unpushed commits → cleanup
  carries `git push origin <branch>`; fully pushed → no cleanup; no tracking ref → the
  `-u` form.

## Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| Scale the review deadline with diff size (#60 bullet 3)? | No | Already per-backend configurable; one data point does not pin a curve. YAGNI. | user-confirmed |
| 15m default, or 10m to stay inside the existing envelopes? | 15m, and raise the envelopes with it | The issue asks for 15m; the containment relation between the deadlines is then explicit and tested rather than accidental. | user-confirmed |
| Always emit the `pr` push cleanup, or only when the branch is ahead? | Only when ahead | Matches `applyDisposition`'s "ask git, never state" rule; a replayed archived `next` stops offering a push already done. | user-confirmed |
| New `reviewTimeoutS` / `closeTimeoutS` / `closeWaveTimeout` values | 20m / 60m / 60m | Smallest values that satisfy the A2 invariants with a 15m backend deadline and a 10m verify timeout. | assumed |
| Which backend does the `review_error` gate name? | Every backend in `backends.reviewer`, in preference order | `gatherFacts` must not shell out for a health probe, so it cannot know which backend actually ran; naming all configured ones is accurate without one. | assumed |
| `internal/backend/run.go`'s unset-`Timeout` fallback | Raised to 15m alongside the config default | It exists so an unset request is not unbounded; leaving it at 5m would make the two "defaults" disagree. | assumed |
| Commit message for the `finish/pr.md` commit | `takt(<slug>): pr body` | Matches the existing short-phrase style (`archive`, `goals amended`, `<step> done`). | assumed |
| Order of implementation | Either; the two parts share no files | A touches config/decide/close-wave, B touches cmd_next/archive. | assumed |
