# Review: spec — rework

The design is close, but its grammar and compatibility claims contradict the specified value-cleaning behavior.

- **major** spec.md:0 — Independent closing decoration conflicts with interior-emphasis preservation: The accepted grammar says Dc is independently optional and removed, including decoration around the whole line. Step 4 instead preserves every trailing run whenever an interior run exists. For `**SUMMARY: fixed *parseReport***`, the parser cannot distinguish the inner emphasis closer from the whole-line closer and preserves all three trailing stars. This also contradicts G1. Define an unambiguous precedence or narrow the accepted-language claim and tests.
- **major** goals.md:0 — Unchanged exact-prefix behavior is not preserved: G2 says undecorated exact-prefix behavior remains unchanged, but Step 4 removes any trailing `*`, `_`, or backtick when no interior run exists. A previously valid line such as `SUMMARY: changed wildcard *` changes from `changed wildcard *` to `changed wildcard`. Either explicitly accept this compatibility change with testable boundaries or revise the algorithm.
- **minor** spec.md:0 — Whitespace grammar contradicts the parsing steps: The formal accepted-shape expression places a mandatory space after the key/key decoration, while Step 4 accepts zero whitespace and existing prefix parsing accepts `STATUS:done`. State whether whitespace is optional and add a no-space regression case so G2 is testable.

_copilot / gpt-5.6-sol_
