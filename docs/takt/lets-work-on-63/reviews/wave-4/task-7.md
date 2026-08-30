# Review: lets-work-on-63 task 7 — rework

The command behavior and coverage largely match the task, but the central concurrency guarantee is not actually enforced because lock acquisition is non-atomic.

- **major** internal/cli/cmd_next.go:138 — Concurrent callers can both acquire an initially free run lock: acquireLock performs ReadSession, decides that the lock is free, and later calls WriteSession without an atomic create, compare-and-swap, ownership recheck, or surrounding OS lock. An archived next and retro --rewrite can therefore both read a missing session file, both proceed as LockAcquired, and overwrite each other's holder record while entering their critical sections. That permits recommitArchive to capture the pair between retroRunOp's two file replacements, defeating the task's motivating guarantee. TestRetroRewriteLockShutsOutAnArchivedNext only tests an already-present holder and cannot detect simultaneous acquisition from an unlocked state; acquisition must be serialized and covered by a contention test.

_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests] major internal/cli/cmd_next.go:106 — Archived-run warning propagation fix has no regression test: This diff changes `takt next` on an archived run to take the lock and print through r.emit (line 106) instead of the old plainOp (removed), specifically so a lost optional write (e.g. excludeWarning at line 169) is no longer dropped on an archived-run replay. No test in this wave or in the existing suite (internal/cli/cmd_next_test.go, internal/cli/archive_test.go) exercises the archived-replay path with a triggered warning to confirm it now appears in the reprinted op. Every existing test that calls `next` twice on an archived run (e.g. TestArchiveKeepStaysClean, TestArchivedMergeWaitsForThePrimaryToComeBack) runs in conditions where r.warnings is always empty, so a regression that silently drops the warning again (or double-appends it) would pass the whole suite undetected.
