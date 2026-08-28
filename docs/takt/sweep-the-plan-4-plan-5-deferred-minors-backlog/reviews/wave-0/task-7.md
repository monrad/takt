# Review: sweep-the-plan-4-plan-5-deferred-minors-backlog task 7 — approve

The change correctly pins all requested boundary, cross-host invariant, and backslash-parity cases without production changes or secrets.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests] minor internal/prompt/copilot_test.go:290 — Backslash-run test's boundary case is mislabeled "zero" when the actual run is two: The case named "a run at the very start of the body is even (zero)" uses value `"\\"b"` (body `\\"b`), where the backslash run preceding the quote at body index 2 is 2, not 0 — it exercises the loop's j>=0 boundary (the run extends to the start of body) rather than a zero-length run. The assertion itself is correct (byte 3), so this is a naming/comment nit, not a functional gap — but the parenthetical is confusing for a future maintainer reading it as documentation of what's being pinned.
