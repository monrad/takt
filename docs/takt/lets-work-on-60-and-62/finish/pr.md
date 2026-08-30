Two independent defects in takt's own tooling, both surfaced by the PR #56 run.

## Goals

- G1 — The shipped backend review deadline is 15m for both `copilot` and `claude`, and `internal/backend`'s unset-`Timeout` fallback is asserted equal to it so the two constants cannot drift. — achieved
- G2 — The deadlines wrapping a backend call are derived from the run's actual config and plan, not fixed constants: `internal/deadline` owns `Close`/`Verify`/`GateReview`/`Session` and the `Grace` constant moved out of `internal/cli`, and `closeWaveTimeout`, `reviewTimeoutS`, `closeTimeoutS` and `verifyTimeoutS` are gone. — achieved
- G3 — The containment invariants hold as properties of one pure function: one uniform saturation domain applied to every function alike — below saturation the strict bounds `Session(x) > x` and `GateReview(bt) > bt` hold, at or above it each returns exactly `MaxDuration` and only the non-strict form is claimed; `Close(b) >= VerifyTimeout×VerifyCommands`; `Close(b) >= 2×BackendTimeout` when `ReviewTasks >= 1`; `Verify(per,n) >= per×n`; `Close(b) >= Floor` always; and all of them monotonically non-decreasing in every input that adds work (`VerifyTimeout`, `VerifyCommands`, `BackendTimeout`, `ReviewTasks`), with `MaxParallel` the documented exception — a divisor, so `Close` is non-increasing in it. — achieved
- G4 — A close-wave's budget counts verify serially (per command and per task, undivided by `max_parallel`) and reviews concurrently at `2 × backend_timeout × ceil(tasks / max_parallel)`. — achieved
- G5 — The `review_error` gate's retry option names `backends.<name>.timeout` and its current deadline for each configured reviewer backend that has a config key, skips entries that have none (`fake`, unknown names), and degrades to the literal key when that leaves nothing — with no health probe added to `gatherFacts`. — achieved
- G6 — A `takt next` that emits the `push_pr` op leaves `finish/pr.md` committed in HEAD, so the body the PR is created from is in the branch; an immediate replay adds no commit. — achieved
- G7 — Archiving a `pr`-disposition run hands the push back as `cleanup` exactly when git says the branch holds commits the remote-tracking ref does not, and never fails the archived stop: commits not in `origin/<branch>` → `git push origin <branch>`; fully pushed → no cleanup; no tracking ref → the `-u` form; the git read itself failing → the push is still offered and the stop still succeeds. — achieved
- G8 — The docs no longer contradict the code. — achieved
- G9 — The whole repository gate passes on the finished branch. — achieved

## Run

Bundle: docs/takt/lets-work-on-60-and-62/ — spec.md, plan.md, reviews/, retro.md
