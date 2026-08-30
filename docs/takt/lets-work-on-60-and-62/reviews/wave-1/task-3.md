# Review: lets-work-on-60-and-62 task 3 — approve

The implementation matches the specified deadline derivation and fast-path behavior. It reads and threads the index once on the working path, propagates index errors, preserves landed-close replay without an index, derives verify and review deadlines from internal/deadline, and adds adequate behavioral and budget-counting tests.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests,simplicity] minor internal/cli/cmd_close_wave.go:121 — closeBudget's nil-ActiveWave guard is untested and unreachable: closeBudget's `if st.ActiveWave == nil { return b }` branch (lines 121-123) has no test in close_budget_test.go. It's also unreachable in production: closeBudget is only called from closeWaveBudgeted, which cmdCloseWave only reaches after already checking tgt.st.ActiveWave != nil. Low risk since it's dead code on the real call path, but as written it's a new branch a regression in could go uncaught.
- [lens:consistency] minor internal/cli/cmd_close_wave.go:126 — The 'counted set' predicate is duplicated inline instead of shared: closeBudget's task filter `t.Wave != st.ActiveWave.N || t.Status != bundle.StatusPending` (line 126) is a verbatim copy of resolveTaskResults' filter `t.Wave != aw.N || t.Status != bundle.StatusPending` (line 280). Both doc comments explicitly assert the two sets must stay identical (closeBudget's comment: 'the set resolveTaskResults grades'), and task-4's cross-check integration test that would guard this invariant doesn't exist yet in this wave. With the predicate spelled out twice rather than factored into one helper (e.g. a shared `pendingWaveTasks(st)` iterator), a future edit to one loop's condition can silently desync the deadline's counted work from the work actually graded, with nothing but human review catching it until the task-4 test lands.
