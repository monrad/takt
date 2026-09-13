# Review: finish-phase-fixes-for-the-features-that task 2 — approve

The change matches the specification: it extracts all three reference forms from the original topic with the required boundaries, preserves verbatim topic order, de-duplicates exact tokens, repeats the closing keyword per reference, and places the conditional Issues section correctly for both goals modes. The tests cover the required positive, mixed, duplicate, ordering, placement, sentence, and boundary-negative cases.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests] minor internal/finish/pr.go:222 — No test proves isWordByte's non-ASCII boundary claim: isWordByte's doc comment (pr.go:220-221) states 'A byte of a multi-byte rune is not one, so a topic written in another script still names its issue' — an explicit, testable claim about UTF-8 continuation bytes never matching the word-byte check. No case in TestBuildPRIssuesSection (pr_test.go:192) puts a non-ASCII character immediately before or after a bare '#N' reference to verify that claim.
- [lens:tests] minor internal/finish/pr_test.go:192 — Bare-#N leading-boundary negatives cover only letters and slash, not digits or underscore: isWordByte (internal/finish/pr.go:222) treats '_', digits and ASCII letters alike as word bytes, and bareRefIsBounded (pr.go:210) uses it to reject a preceding word byte. TestBuildPRIssuesSection's leading-boundary negatives are 'abc#71', 'takt#71' (letters), '/#71' and 'owner/#71' (slash) — no case precedes '#' with a digit or '_', so those two branches of isWordByte are exercised only redundantly through the letter cases, never distinguished from them.
