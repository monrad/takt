# Review: sweep-the-open-issue-backlog-fix-the task 13 — rework

Most requested coverage is implemented, but the blind-review regression test deliberately weakens an explicit required assertion and would miss some lens-name leaks.

- **major** internal/cli/close_internal_test.go:350 — Blind prompt test does not enforce the required `correctness` exclusion: The task explicitly requires asserting that the prompt contains no `correctness`, but the test only rejects `[lens:correctness]`. It would therefore pass if the internal lens name leaked as `Lens: correctness` or any other untagged form. The nearby comment acknowledges the deviation because the generic task rubric itself contains the word; that conflict needs to be resolved while preserving an effective tripwire rather than silently narrowing the assertion.

_copilot / gpt-5.6-sol_
