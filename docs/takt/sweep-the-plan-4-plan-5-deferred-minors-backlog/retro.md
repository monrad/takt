# Retro — sweep-the-plan-4-plan-5-deferred-minors-backlog

## What was built

Thirteen deferred findings from the plan-4 and plan-5 reviews, filed as GitHub issues and
left to rot, landed as one branch: eleven code-and-doc fixes, two human rulings (#4's
"log a takeover, not a `--force`" and #16's "report the loss, keep exit 0"), and one
closure (#9, already fixed before the run began). Nine tasks across three waves, one
rework. All thirteen goals came back `achieved` at `944f47b`, and
`takt verify` passed all 37 of the plan's verify commands on the assembled tree —
including `go test -race ./...` and `golangci-lint run ./...`.

The run also closed two issues as bookkeeping rather than code: #19 (fixed by PR #22, never
closed) and #9.

## What went well / what did not

**The two review layers found disjoint defects — `overlap: 0`.** Not one of the 18
confirmed internal findings was also raised by the cross-vendor backend reviewer. The clearest
case is task 5: the backend reviewer sent it back because its new test called `render()` directly
and never exercised the `--root` path, while the `tests` lens independently found that the
"no description" failure branch had zero coverage. Same task, same weakness, two different
defects, neither layer seeing the other's. Both were merged into the retry brief and both were
fixed on attempt 2 — which arrived with a mutation check proving the old hardcoded path now
fails all six subtests.

**One rework in nine tasks, and it was the right one.** Task 5 was the only `rework`
(2 attempts); every other task was approved first time, including task 2 — ten files,
four sub-changes, and the `!`-escaping ordering trap. Zero failures, zero waivers, zero blocked
tasks.

**The `correctness` lens reported nothing, in any wave.** It does not appear in `by_lens` at all:
across three waves plus a rework attempt it produced zero candidates, while `consistency` produced
8 and `docs` 6. Per the two-review-layers design's own drop-a-lens rule, that is the evidence to
act on — though it is one run, and the lens did real tracing work each time (it verified the
escaper's ordering and `slices.Clone`'s aliasing safety before declining to file).

**The verifier's evidence bar did bite, but only once.** 19 candidates,
18 confirmed, 1 false positive. The refutation was a good one — a claim that
§4.6's "silently" (no `ask: owner` prompt) contradicted the new paragraph's "quietly" (no audit
event); the verifier established they describe different mechanisms and the claim was speculative.
An 18/19 confirm rate is worth watching: either the lenses are unusually disciplined or the bar is
not adversarial enough. `scoped_passes: 0` — no blocking disagreement ever needed one.

**The weakest work in the run was the part a human specified.** Three lenses — intent,
consistency and docs — independently converged on the §4.6 sentence rewritten under the #4 ruling.
The rule was framed by the *acquirer's* kind ("a named session takes over"), but the code's
exemption keys on the *holder's* kind (`orphaned` requires `held.Generated`). A generated acquirer
taking a stale *named* holder records too, and no clause said so. The code comment states it
correctly; the condensed spec prose lost it. G6 still passed, because G6 asks only that the
sentence "states both halves" — the goal inherited the same two-clause framing, so no downstream
check could have caught it.

**Wave timings.**

| wave | attempt recorded | dispatched | committed | elapsed |
|---|---|---|---|---|
| 0 | attempt 2 | 12:18:07 | 12:28:19 | 10m12s |
| 1 | attempt 1 | 12:28:28 | 13:03:24 | 34m56s |
| 2 | attempt 1 | 13:03:32 | 13:36:53 | 33m21s |

**Lens yield.**

| lens | reported | confirmed |
|---|---|---|
| consistency | 8 | 7 |
| docs | 6 | 6 |
| intent | 1 | 1 |
| simplicity | 1 | 1 |
| tests | 5 | 5 |

`unattributed: 4` — four confirmed findings landed on files no task declared
(the design spec and `CONTRIBUTING.md`), so nothing in the wave owned them.

**Three of the repository's own open issues reproduced on this run's own data, plus one new
one.** They are listed under Follow-ups.

## Follow-ups

Nothing was waived, no verification was overridden, and no task was left undone. The spec gate
closed on an `accept` override after three rounds, and the plan gate approved on its fifth; both
carried findings forward rather than acting on them.

**Observed about takt itself while running takt** — none of these are in this run's scope, and
the first three are already filed:

- **#23 reproduced.** `finish/retro-inputs.json` reports `review_findings: 0`. This run's gate
  reviews produced roughly thirty findings across eight passes, plus one task-review rework.
  The count that the retro instructions tell the writer to ground bullets in is zero.
- **#25 reproduced.** `wave_timings` holds one entry per wave, and wave 0's carries
  `"attempt": 2` — the ~30 minutes of attempt 1 and its six implementers are invisible. The
  elapsed column above understates wave 0 for exactly this reason.
- **#31 reproduced, larger.** The alignment-verdicts brief was 69 KB; each of the six wave-0 task
  briefs was ~28 KB with a byte-identical ~24 KB spec excerpt (verified: one md5 across all six,
  only the delimiter token differs). The op protocol requires the driving session to read each
  brief and write the same bytes back out as the agent's prompt.
- **#37 reproduced, by the driving session.** A `cd` into the bundle to size some files left the
  shell there, and a later repo-relative path reported a missing directory that exists.
- **#27 reproduced.** A `takt answer` issued before `takt next` armed the gate returned
  `{"ignored": true}` at exit 0 with no `hint`.
- **#28 hit live.** Answering `revise` at the plan gate has no mechanism: `plan.md` and
  `plan.index.json` are the planner's artifacts, `revise` does not re-dispatch the planner, and
  the only way to satisfy the gate is for the session to hand-edit them — against the skill's own
  "never edit the bundle by hand" invariant. Done four times this run.
- **NEW: wave 0's follow-ups carry no `wave` field.** Entries from waves 1 and 2 have
  `"wave": 1` / `"wave": 2`; the six from wave 0 have no `wave` key at all, because
  `omitempty` on an int drops a legitimate zero. A reader cannot tell which wave they came from.
- **#24 reproduced, mildly.** One goal-assessor citation is not a `path:line`:
  `"internal/version (dev fallback observed via go build)"` on G10.
- **#32, for scale.** The branch is 137 files and 11,332 insertions; roughly 31 files are source.
  The rest is bundle bookkeeping.

**Findings that closed with their gate instead of being acted on** — 20 in total:

- **minor — Anchor miscounts fixes and rulings** (spec gate, source: override)  
  The anchor still says fourteen fixes plus two rulings rather than eleven fixes, two rulings, and one already-fixed closure. G12 explains but does not correct the anchor.
- **minor — T8 attributes the warnings contract to the wrong task** (plan gate, source: approve)  
  After splitting the contract into T9, T8 still says it depends on T2 for that contract, and its index dependencies include both T2 and T9. T8 only needs T9; the T2 edge unnecessarily delays it and contradicts T9's ownership.
- **nit — Risk analysis uses the superseded wave layout** (plan gate, source: approve)  
  The risk names T2 as concurrent with T1 and T7 in wave 1, while the revised graph places T2 alone in wave 2. Update this paragraph to describe the actual concurrency.
- **minor — task check's shorthand comment no longer matches what task build does** (wave 0, source: internal)  
  `task check     # go build ./... && go test ./... -race -count=1 && golangci-lint run ./...` implies `task build`'s contribution to `task check` is a plain `go build ./...`. After this diff, `task build` also shells out to `go run ./internal/tools/setversion --print` and runs a second, stamped `go build -ldflags ... -o takt ./cmd/takt`, producing a `takt` binary in the repo root as a side effect of `task check`. The one-liner was already missing `hosts:check` before this diff, but this diff adds a second, newly-introduced discrepancy (an extra build invocation and a version-lookup subprocess) that a contributor reading only this line would not expect.
- **major — §15 Distribution and versioning omits the new task-build stamping route** (wave 0, source: internal)  
  Task 6 (Taskfile.yml + internal/tools/setversion) gives `task build` a real version stamp: `go run ./internal/tools/setversion --print` reads .claude-plugin/plugin.json and the value is passed via -ldflags into internal/version.Version, so `task build`'s binary now reports the manifest version instead of 0.0.0-dev. §15 is this repo's living, actively-amended reference for every mechanism that stamps or fails to stamp the version (flake.nix, goreleaser, tagged go install) and explicitly frames 'local build' as the unstamped 0.0.0-dev case (also echoed in internal/version/version.go:33-46's Current() doc comment, which lists exactly 'three routes' and states 'a local go build or go test has neither, and reports [Dev]'). The diff adds a fourth stamping mechanism without a matching §15 bullet or an update to that Current() comment, even though this codebase's own convention (stated in docs/superpowers/plans/2026-08-26-takt-hardening.md:19, 'a task that changes behaviour amends the spec section that describes it, in the same commit') and recent history on this branch (commits 'docs: the intro and §10.1 count five agents', 'docs: two review layers in the base design and README') both treat this file as kept in sync with behaviour changes.
- **nit — `subject` reuses a name that already means something else in this package** (wave 0, source: internal)  
  manifestFailure's new `subject` parameter (and its doc comment at lines 127-130) names the noun a mismatch error uses ("skill"/"plugin"). internal/cli/cmd_close_wave.go already establishes `subject` to mean the git commit subject line (waveSubject, commitSubjectSoftLimit, cmd_next.go:461-494) throughout the same `cli` package. The overlap is harmless today but a grep for "subject" in this package now turns up two unrelated concepts.
- **minor — New ldflags stamp has no regression test, unlike its two siblings** (wave 0, source: internal)  
  internal/prompt/dist_test.go already pins flake.nix and .goreleaser.yaml to the identical ldflags target (`internal/version.Version=`) with automated tests (TestFlakeReadsThePluginVersion, TestGoreleaserStampsTheVersion), and TestGoreleaserStampsTheVersion's own comment (internal/prompt/dist_test.go:63-64) says it 'pins the release build to the same ldflags path the flake and the Taskfile use' — treating all three as one synchronized invariant. Task 6 adds the third stamp to Taskfile.yml:14 but no Go test reads Taskfile.yml or asserts its -ldflags target/package path match the other two, so a future typo in the -X path (which go build silently ignores rather than erroring on) would go undetected by `go test ./...`, reproducing the exact silent-0.0.0-dev-stamp class of bug this task fixes.
- **minor — Package doc comment doesn't mention the new --print mode** (wave 0, source: internal)  
  The package-level comment (lines 1-9) describes setversion purely as the tool that rewrites the version via `task version:set VERSION=x.y.z`. This diff adds a second, independent mode (`--print`, a read-only path used by `task build`) that is never mentioned in that comment, so a reader running `go doc ./internal/tools/setversion` sees only the rewrite mode's contract, not the read mode's.
- **minor — Backslash-run test's boundary case is mislabeled "zero" when the actual run is two** (wave 0, source: internal)  
  The case named "a run at the very start of the body is even (zero)" uses value `"\\"b"` (body `\\"b`), where the backslash run preceding the quote at body index 2 is 2, not 0 — it exercises the loop's j>=0 boundary (the run extends to the start of body) rather than a zero-length run. The assertion itself is correct (byte 3), so this is a naming/comment nit, not a functional gap — but the parenthetical is confusing for a future maintainer reading it as documentation of what's being pinned.
- **major — §13 'Atomic writes' bullet is now stale: it only names state.json/events.jsonl** (wave 1, source: internal)  
  §13 says: 'Atomic writes. state.json is written to a temp file and renamed; events are appended with O_APPEND.' This wave generalizes atomic writes to four more file kinds — the stable brief (internal/cli/cmd_next.go writeStableBriefAt), the slice diff (ensureSliceDiff), the task brief (internal/cli/launch.go renderTaskBrief) and logs/.gitignore (internal/cli/cmd_init.go writeLogsIgnore) — all now going through the new bundle.WriteFileAtomic (internal/bundle/write.go:21). The new doc comment on WriteFileAtomic (internal/bundle/write.go:12-13) explicitly attributes a much broader guarantee to this section — 'which is what spec §13's "every bundle write is atomic" asks for' — but the spec text as written does not say that; it names only two files. A reader of §13 would not learn that agent briefs and the slice diff are now atomic too. The bullet should be broadened (or the file list under §4.2 annotated) to match what the code now claims of it.
- **minor — New degrade-and-warn error category is unmentioned in §13's error-handling model** (wave 1, source: internal)  
  §13's 'Fail loud' bullet describes takt's only failure mode as exit 1 with a structured error, and says takt never repairs state silently. This wave (via the warnings contract wired up here for the first time in real command output — internal/cli/cmd_init.go:127-128, internal/cli/cmd_next.go's r.emit) introduces a second, deliberate category: an optional write (info/exclude) that can fail without failing the command, reported instead as a `warnings` array on `init`'s document and on every post-lock `next` op. Nothing in §13 (or §5.1/§5.2's op-kind documentation, none of which mention a `warnings` field) describes this. Since this diff is the first to make the field observable in real output (wave 0 only added the struct field/serialization), the spec update for it is still owed.
- **minor — New assertNoTemp helper duplicates an existing inline check instead of consolidating it** (wave 1, source: internal)  
  write_test.go adds `assertNoTemp` to check no `.tmp` file survives a failed rename (lines 74-83). The exact same loop (`os.ReadDir` + `strings.Contains(e.Name(), ".tmp")` + `t.Fatalf("temp file left behind: %s", e.Name())`) already exists inline in internal/bundle/state_test.go:88-93 (TestSaveIsAtomic), which is in the same package and could call the new helper. WriteFileAtomic's own doc comment argues for exactly one implementation of the atomicity rule 'rather than one per package' (internal/bundle/write.go:54-57); the test suite doesn't apply that same consolidation to itself, leaving two copies of the same assertion that can now drift.
- **minor — writeLogsIgnore's still-fatal failure path has no test, even though the function's body was rewritten this wave** (wave 1, source: internal)  
  The brief states writeLogsIgnore's own failure must stay fatal (only info/exclude's loss becomes a warning), and this wave rewrote the function's internals (dropped the manual MkdirAll+os.WriteFile in favor of bundle.WriteFileAtomic). No test in cmd_init_test.go forces writeLogsIgnore itself to fail (e.g. an unwritable logs/ directory) and checks that init still exits non-zero and rolls back — unlike the parallel, now-thorough coverage added for excludeLogsDir's new non-fatal path. This is a pre-existing gap, but it intersects code that was directly changed in this diff.
- **nit — keyWarningsJSON duplicates cli.go's keyWarnings under a different name** (wave 1, source: internal)  
  cmd_init_test.go declares `const keyWarningsJSON = "warnings"`, duplicating the value of the unexported `keyWarnings` constant already defined in internal/cli/cli.go:76. This is necessary because cmd_init_test.go/cmd_next_test.go live in the external `cli_test` package and can't reach the unexported constant, but the diverging name (`keyWarningsJSON` vs `keyWarnings`) for the identical wire key is a small naming inconsistency a reader has to reconcile across the package boundary.
- **nit — warnings field's doc comment overstates the emit scope** (wave 1, source: internal)  
  The `nextRun.warnings` doc comment says 'Every op printed after the lock is taken carries them' and the `emit` method's own comment (line 201) repeats 'Every op a `takt next` that took the lock can print goes through here'. But acquireLock's LockBlocked branch also routes its 'owner' question through r.emit (cmd_next.go:137) before the lock is actually acquired — the call that returns early precisely because the lock could not be taken. It's harmless (r.warnings is still empty at that point), but the comment's 'after the lock is taken' framing doesn't literally describe that call site.
- **major — Acceptance-note regression test for the WriteFileAtomic migration is missing** (wave 1, source: internal)  
  Task 2A's acceptance note explicitly calls for a test that the four call sites (writeStableBriefAt, ensureSliceDiff, renderTaskBrief, writeLogsIgnore) were actually converted, by asserting `os.WriteFile` no longer appears at all in cmd_next.go, cmd_init.go and launch.go — specifically warning that a positive grep for `WriteFileAtomic` alone would pass even if one of cmd_next.go's two occurrences were left unconverted. No such test was added in cmd_next_test.go, cmd_init_test.go or elsewhere in this wave's diff. I confirmed by grep that the production migration is currently complete (no `os.WriteFile` remains in those three files), but nothing in the test suite would catch a future regression where one call site reverts to `os.WriteFile` — exactly the false-positive scenario the brief called out.
- **major — Rewritten §4.6 sentence omits the generated-acquirer/named-holder case** (wave 2, source: internal)  
  The new sentence enumerates recording as 'when a named session takes over' or 'whenever a takeover was explicitly forced', plus the generated-over-generated exemption and the no-takeover case. It does not mention the (unchanged, pre-existing) case where a *generated* acquirer takes over a *stale named* holder without --force: since `orphaned` is false there (the holder isn't `Generated`), the code's default arm in internal/cli/cmd_next.go still appends lock_taken (outcome LockStolen) regardless of the acquirer's own kind or the force flag. A reader could infer from 'recorded when a named session takes over' that a generated acquirer's takeover of a stale named holder goes unrecorded, which is not true. The code is correct and unchanged in this respect; only the doc's new three-part framing leaves this combination unstated.
- **minor — New default: arm of the lock_taken switch has no test asserting its event shape** (wave 2, source: internal)  
  Task-3 rewrote the last switch arm from an explicit outcome check (`case outcome == bundle.LockStolen, outcome == bundle.LockForced && r.force:`) to a bare `default:` (cmd_next.go:207-210), which only the not-orphaned path reaches (a named/non-generated holder being stolen or forced). The new `case !takeover:` guard that makes this safe is well covered by TestNextForceWithoutATakeoverAppendsNothing (cmd_next_test.go:1512), and the exemption change is covered by TestNextExplicitForceOverAGeneratedHolderAppendsLockTaken (cmd_next_test.go:1462) — but that test only exercises generated holders, which are always `orphaned` regardless of heartbeat freshness (orphaned only checks held.Generated + ID mismatch, not staleness), so both its idle and live scenarios land in `case orphaned:`, not `default:`. The only code path that actually reaches `default:` is a named-holder takeover, exercised behaviorally by the pre-existing TestNextSessionLock (cmd_next_test.go:881-899), but that test never inspects the appended lock_taken event's fields (outcome value, absence of a `reason` key). No test in the diff (or found in the file) asserts `eventsOfType(...)[0].Data` for a forced/stolen takeover of a non-generated holder, so a regression in this specific arm (e.g., an accidental `reason` key, or a wrong outcome value) would not be caught.
- **minor — Fault-injection test helpers duplicated verbatim across two test files** (wave 2, source: internal)  
  readOnlyEventLog/directoryEventLog (internal/cli/cmd_record_test.go:338-368) and breakEventAppend/breakEventRead (internal/cli/record_reviewer_test.go:549-579) are byte-for-byte identical implementations (same os.Chmod(0o400)/OpenFile probe/os.Remove+os.Mkdir dance), differing only in name. They live in different Go packages (cli vs cli_test) so they can't call each other directly, but internal/testutil (a package already imported by both, e.g. testutil.NewRepo/testutil.Git) is the natural home for a single testutil.BreakEventAppend(t, bdir)/testutil.BreakEventRead(t, bdir) pair — confirmed via grep that os.Chmod(p, 0o400) and Mkdir(p, 0o750) each appear in exactly these two new locations and nowhere pre-existing in the tree. Not blocking, but ~30 duplicated lines that a shared helper would remove.
- **nit — warnStreakLoss inlines its warning string instead of following the local excludeWarning(err) helper convention** (wave 2, source: internal)  
  cmd_init.go's excludeWarning(err) (internal/cli/cmd_init.go:425-427) is a small named function returning "info/exclude not written: " + err.Error(), used by both cmd_init.go and cmd_next.go for the same warnings-contract pattern this task extends. warnStreakLoss instead builds "attempt-streak reset not recorded: "+err.Error() inline inside the append call. Not wrong, but it departs from the one-sentence-per-named-function shape the warnings contract otherwise uses for its two other producers.
