# Review: spec — rework

The design is close, but its test oracle and marker grammar contain contradictions that must be resolved before planning.

- **blocking** spec.md:0 — Cross-product expectation contradicts lone-closer semantics: The cross-product test requires every decoration combination to yield a value with all decoration removed. But combinations containing only Dc have no opener, so step 4.3 and the explicit `STATUS: done**` case require Dc to remain. Exclude closer-only combinations from that expectation or define their expected decorated values.
- **major** spec.md:0 — Optional-space claim contradicts marker grammar: The accepted-shape section says every space is optional, while the marker definition requires whitespace after each marker and the must-not-match table rejects `-STATUS: done`. State explicitly that only non-marker spacing is optional.

_copilot / gpt-5.6-sol_
