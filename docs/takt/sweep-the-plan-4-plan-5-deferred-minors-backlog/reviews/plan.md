# Review: plan — rework

The decomposition is broadly sound, but execution should not start: several acceptance commands can pass with required fixes missing, and #9’s mandated bookkeeping closure has no task.

- **major** :0 — T6 can validate a stale binary: T6 runs `task build` without first removing `./takt`. Because that artifact is gitignored and may preexist, the version checks can pass even if the build task emits no binary. Remove the artifact before building or otherwise prove it was recreated.
- **major** :0 — T2 does not prove all four atomic-write migrations: T2 requires two migrations in `cmd_next.go`, but its grep proves only one occurrence. Normal-path tests and helper tests cannot detect a remaining `os.WriteFile`. Verify both call sites explicitly.
- **major** :0 — T1 does not prove single manifest evaluation: T1’s grep and behavioral tests can pass while `ManifestMatches` is still called twice, leaving issue #10 unfixed. Add an acceptance check or test that establishes one evaluation per handshake.
- **major** :0 — T8 tests can leave two callers swallowing errors: T8 requires all four callers to propagate `endAttemptStreak` errors, but explicitly tests only one caller in each source file. The signature grep and package tests can pass with the other two callers still discarding the result.
- **major** :0 — No task performs the required #9 closure: The spec says #9 is closed as part of this run’s bookkeeping. T3 claims G12 without any corresponding action, while plan.md explicitly leaves issue closing outside the tasks. Add an explicit bookkeeping task or make the closure an enforceable completion step.

_copilot / gpt-5.6-sol_
