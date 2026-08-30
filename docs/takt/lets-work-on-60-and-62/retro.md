# Retro — lets-work-on-60-and-62

## What was built

Issues #60 and #62, both defects in takt's own tooling surfaced by the PR #56 run. #60
raised the shipped backend review deadline from 5m to 15m and made the `review_error`
gate name `backends.<name>.timeout` and its current deadline instead of leaving the user
to read the source. #62 made `next` commit `finish/pr.md` before the session pushes, so
the pull request carries the body it was created from, and made the `pr` disposition's
archived stop hand back `git push [-u] origin <branch>` as `cleanup` exactly when git
says the branch holds commits the remote-tracking ref does not.

The spec review turned #60 from a one-constant change into something larger, and
rightly: the three deadlines wrapping a backend call were fixed constants, and two were
already unsound independently of #60 — `closeWaveTimeout` budgeted nothing at all
(`verify_timeout` is per command and serial across tasks, so an 8-task wave with two
commands each can spend 160m in verify before any review), and `reviewTimeoutS` would
have equalled the new backend deadline exactly. A new stdlib-only `internal/deadline`
package now derives every one of them from the run's real config and plan. All nine
goals were assessed `achieved` at `55fcddd`, and `task check` passes on the branch.

## What went well / what did not

**Went well**

- Both issues are fixed and each demonstrated itself during its own run. Task 7's review
  timed out at exactly `5m0s` — #60's reported defect, live — and the same diff was
  approved after a rebuild put the new 15m default in the driving binary. Later waves
  emitted `timeout_s: 8220` and `46530` where the deleted constants would have said 1800
  and 900: the derivation visibly working on the run that wrote it.
- The implementers did unusually rigorous work unprompted. Task 2 ran a 12-mutant
  campaign against `internal/deadline` (all caught, 100% statement coverage) and deleted
  the one redundant guard a surviving mutant exposed. Task 7 mutation-tested the ancestry
  logic — an ahead-only variant and a strictly-ahead variant both fail its tests. Task 4's
  rework and task 5's both mutation-checked their fixes rather than trusting coverage.
- The internal lens layer earned its place: 26 candidates, 22 confirmed, 4 refuted. Every
  one of the six lenses confirmed at least one finding (tests 9/9, consistency 6/8,
  docs 6/8, correctness 2/2, intent 1/1, simplicity 1/1), so none is a candidate for
  removal from `review.lenses`.
- **Overlap with the cross-vendor reviewer was 0.** Not one confirmed internal finding was
  also raised by the backend review. The two layers found entirely disjoint defects — the
  strongest evidence in this run that both are worth running.
- The verifier refuted 4 of 26 candidates on evidence rather than opinion, including one
  that claimed `golangci-lint` would flag a missing `//nolint:testpackage`: it read the
  linter's own source and found `testpackage` skips `*_internal_test.go` by default.

**Did not go well**

- **The plan gate took 9 review rounds and never converged** — findings went 8 → 4 → 3 →
  3 → 3 → 2 → 3 → 4 → 3 and was closed by override. Several rounds were the reviewer
  catching contradictions the *previous round's fix* had introduced: round 4's blocking
  finding came from round 3's fix, and round 5's from round 4's. The cause was
  surgical patching of a spec with many interlocking claims. Round 6's fix — restating
  the saturation domain once for all four functions instead of exempting one — was the
  first that addressed a class rather than an instance, and findings dropped after it.
- **Task 4 was waived, not fixed.** Its own code was clean (five of six lenses returned
  zero on attempt 2, and the blocking nil-index defect was fixed and mutation-checked),
  but the wave was held open by a confirmed finding about
  `docs/superpowers/specs/2026-08-24-takt-design.md:462` — a file outside task 4's
  declared scope. No retry could have fixed it. The finding was routed to task 8, which
  owns that file, and task 8 made the edit.
- **The backend reviewer misread takt's own bundle churn as a scope violation.** It failed
  task 5 attempt 1 for "undeclared workflow artifacts", naming `events.jsonl`,
  `state.json`, `waves/2/close.s1.json` and `waves/3/` — all written by `takt record`,
  `close-wave` and `next` as the run executed, and all forbidden to implementers. That
  cost a full rework round on a task whose real findings were two minors. It did not
  recur on attempt 2.
- **G8 shipped `partial` on the first goal assessment.** Task 8's sweep fixed exactly the
  four items its own verify greps checked and missed four more the docs lens found —
  including the design doc never stating that `finish/pr.md` is committed before the push,
  which is #62's first half. The `goals_unmet` gate's *fix* path closed them and the
  re-assessment returned `achieved`. A verify command that greps for a phrase proves the
  phrase is present; it cannot prove the document is now consistent.
- Two capacity interruptions: the planner (pinned to `fable`) hit its model limit mid-
  revision, and two wave-2 lenses died on the weekly limit. Wave 2 attempt 2's timing
  spans ~24h almost entirely as that pause. The planner's writes had landed before it
  died, so its output was verified and recorded rather than re-run.

## Follow-ups

- **Task 4 waived** (wave 2). Reason: its own work passed five of six lenses with the
  blocking defect fixed; the wave was held open by a confirmed finding in a file outside
  its declared scope, which was routed to task 8 and fixed there.
- **Plan gate closed by override** at round 9, carrying three findings (below).
- Items 11–15 below were subsequently **fixed** — 11 by task 8's routed fifth edit, and
  12–15 by the `goals_unmet` *fix* commit `55fcddd` after the goal assessor found G8
  partial. They remain listed because they closed with their gate rather than being acted
  on at the time.

1. major — Task 1 tests the fake seam, not the real runCLI fallback (plan gate, source:
   override). Task 1's cross-package test drives `fakeReviewer.Review`, which itself calls
   `resolveTimeout`; it can pass even if `runCLI` — the production path A1 names — applies
   a different unset-timeout fallback. G1's no-drift evidence is therefore indirect and
   defeatable. Mitigated in practice by `task check` and the live backend tests, but a
   direct pin on `runCLI` would be better.
2. minor — Task 1 disagrees about the explicit-timeout cancellation row (plan gate,
   source: override). plan.md required a second test row driving a short explicit
   `Timeout` against a longer sleep; plan.index.json did not, and its verify selector did
   not require it. The index is authoritative for execution, so the row was never written.
   `TAKT_FAKE_REVIEW_SLEEP` already existed in `fake.go`, so the grep that was meant to
   force it passed trivially.
3. minor — Boundary tests weaken a representable strict-containment case (plan gate,
   source: override). `MaxDuration - SessionMargin` and `MaxDuration - Grace` are exactly
   representable, so the strict bound holds at equality; the plan classified them as
   saturation points where only the non-strict form is asserted. The strict relation
   should be asserted at equality, with the non-strict-only rule reserved for inputs
   strictly above the threshold.
4. blocking — Prompt invariant "never push except push_pr" is now false: archived pr
   cleanup pushes too (wave 0, source: internal). `commands/takt.md:45` and
   `hosts/copilot/skills/takt/SKILL.md:46` both read "Never commit or push except where an
   op says so (`push_pr`)". Task 7's `prCleanup` now puts `git push origin <branch>` in an
   archived stop's `cleanup`, which the op table tells the session to run after
   confirmation — a second sanctioned push. A session honouring the invariant literally
   could refuse the very cleanup #62 exists to hand back.
   `TestPromptInvariantsReadTheSameOnEveryHost` would not catch it: its anchor list covers
   only the `git add -A` clause of that bullet. **Still open — no task in this run owned
   those files.**
5. minor — recordReviewDeadline's failure path is never exercised (wave 0 / task 1,
   source: internal). `internal/backend/fake.go:45`'s `errorResult` branch on a failed
   write is untested; the only test uses a writable `t.TempDir()` path.
6. nit — Added assertion re-invokes commitBundle rather than checking preparePushPR's own
   call (wave 0 / task 6, source: internal). `brief_stable_test.go:188` calls
   `commitBundle` a second, independent time to assert `committed=false`, documenting the
   helper's no-op behaviour rather than observing what `preparePushPR` actually did.
7. minor — preparePushPR's new commitBundle error path is untested (wave 0 / task 6,
   source: internal). No test forces the `pr body` commit to fail; the repo has an
   existing `.git/index.lock` injection pattern that would fit.
8. minor — Cleanup-empty checkpoints skip op/reason assertions (wave 0 / task 7, source:
   internal). `archive_test.go:696` and `:710` read `o["cleanup"]` without first asserting
   `op == "stop"` and `reason == stopArchived`; `cleanupOf` returns empty for a missing
   key, so a regression returning a different op entirely would still pass those rows.
9. minor — closeBudget's nil-ActiveWave guard is untested and unreachable (wave 1 / task
   3, source: internal). `cmd_close_wave.go:121`'s guard cannot fire: `cmdCloseWave`
   returns early on a nil `ActiveWave` before `closeBudget` is reached.
10. minor — The 'counted set' predicate is duplicated inline instead of shared (wave 1 /
    task 3, source: internal). `closeBudget` (`cmd_close_wave.go:126`) and
    `resolveTaskResults` (`:280`) encode the same wave/pending filter twice; the doc
    comments assert they must stay identical. Task 4's integration test now guards the
    invariant, but a shared iterator would remove the drift outright. The same shape
    recurs between `reviewerBackends` (`internal/cli/facts.go`) and
    `config.Backends.ReviewBudgetTimeout` — that one needs `internal/config` in scope to
    fix, which no task in this run had.
11. major — Design spec still shows the deleted fixed close-wave timeout as the canonical
    exec example (wave 2, source: internal). `2026-08-24-takt-design.md:462` hard-coded
    `"timeout_s": 1800`, the value of the deleted `closeTimeoutS`. **Fixed** by task 8's
    routed fifth edit.
12. major — §4.7 commit-message catalog omits the new "pr body" commit (wave 4 / task 8,
    source: internal). **Fixed** in `55fcddd`.
13. minor — §5.2's "exactly when" wording for pr cleanup is looser than §7.5 step 5's own
    case list (wave 4 / task 8, source: internal). The two sections of one document
    disagreed about the failed-git-read case. **Fixed** in `55fcddd`.
14. minor — review_error gate's dynamic retry text (`backends.<name>.timeout`) is
    undocumented (wave 4 / task 8, source: internal). **Fixed** in `55fcddd`.
15. major — §7.5 step 4's `pr` disposition prose doesn't mention finish/pr.md is committed
    before the push (wave 4 / task 8, source: internal). The design doc did not describe
    #62's first half at all. **Fixed** in `55fcddd`.

### Worth opening as issues

- **Item 4** is the only follow-up still open with nothing tracking it: the prompt
  invariant in `commands/takt.md` and the generated `hosts/copilot/skills/takt/SKILL.md`
  contradicts this run's own new behaviour, and the cross-host parity test cannot catch
  it. It is a one-line prose fix plus, ideally, extending `crossHostInvariants`' anchors.
- **The backend reviewer sees the whole worktree**, including takt's bundle bookkeeping,
  and can misread it as an implementer scope violation (task 5, attempt 1). Scoping what
  it is shown to the task's declared-file diff would remove a class of false rework.
- **Plan-gate convergence.** Nine rounds with no cap is a lot of wall-clock for an 8-task
  change. `gate_review_capped` exists for the spec gate after three rounds; the plan gate
  has no equivalent.
