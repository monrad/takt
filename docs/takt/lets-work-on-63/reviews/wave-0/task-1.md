# Review: lets-work-on-63 task 1 — rework

The core parser works, but it can return rows from a later table after encountering the explicitly malformed header/separator shape that must invalidate the section.

- **major** internal/spec/assumptions.go:69 — Invalid separator does not invalidate the parse: When a pipe-separated header is not followed by a valid separator, the parser continues scanning. If a valid table appears later in the same section, its rows are returned, despite the contract requiring a header not followed by a valid markdown separator to yield an empty slice. Return empty once an apparent assumptions header has an invalid separator, and add a regression case containing a later valid table so the existing end-of-input tests cannot pass accidentally.

_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests] major internal/spec/assumptions.go:74 — No test distinguishes 'stop at first table' from 'skip to a later valid table' when a table's headers are malformed: ParseAssumptions breaks out of the scan entirely (line 76: `if !ok { break }`) once it finds a header row with a valid separator whose column names don't match — per the doc comment this is intentional: the parser reads only the 'first markdown table' under the heading, even if that table is malformed, and must not fall through to a later well-formed table in the same section. The existing 'missing source header' subtest in TestParseAssumptionsTolerant (assumptions_test.go:431) only has one candidate table in its fixture, so it exercises the `!ok` branch but cannot distinguish `break` from `continue`: both produce the same empty result there. No fixture anywhere in the suite places a second, genuinely well-formed table (valid header names) after a first table with a bad header name in the same section, so this documented 'first table wins, even when malformed' contract is unverified — a regression that made the parser fall through to the next table would pass every existing test.
