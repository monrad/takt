# Review: spec — approve

The spec is coherent, explicitly scoped, testable, aligned with goals.md, and includes the required decisions table. Minor clarification and coverage gaps remain.

- **minor** spec.md:0 — Whitespace wording is broader than the grammar: The accepted-shapes section says spacing “around the colon” is optional, which can imply `STATUS : done`; the normative steps only permit optional whitespace after the colon. State that whitespace before the colon is forbidden.
- **minor** spec.md:0 — Negative tests do not cover every key: G3 applies lowercase and missing-colon rejection to keys generally, but the must-not-match rows only exercise STATUS. Include equivalent SUMMARY and BLOCKERS cases so an inconsistent per-key implementation cannot pass.

_copilot / gpt-5.6-sol_
