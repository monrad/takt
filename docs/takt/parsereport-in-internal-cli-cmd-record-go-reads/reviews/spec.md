# Review: spec — rework

The intended tolerance is well scoped, but the normative grammar, parsing algorithm, and tests disagree about the exact accepted language.

- **major** spec.md:77 — Unbalanced leading decoration is incorrectly accepted: Step 2 removes any leading decoration run, but Step 4 never requires that run to close. Inputs such as `**STATUS: done` therefore parse successfully despite the matrix declaring that a line matches exactly one of the balanced placements. Require a valid closing placement or explicitly include unbalanced forms in the grammar.
- **major** spec.md:137 — Negative tests do not enforce the exact decoration grammar: The must-not-match cases cover anchoring, key casing, colons, and markers, but not malformed decoration. Add cases for missing, mismatched, and misplaced closing runs so the implementation cannot silently accept inputs outside the stated exact grammar.
- **minor** spec.md:91 — Finite marker matrix contradicts the marker grammar: The grammar accepts arbitrary stacked markers, headings from one through six `#`, and any ordered marker, while the matrix says the accepted set is exactly a cross-product using a finite subset. Clarify that matrix values are representative test classes, or expand the test construction to cover the grammar's boundaries and stacking rules.
- **minor** goals.md:12 — Override behavior lacks explicit evidence: G2 requires all three CLI overrides to keep winning, but its named new cases and the spec's test plan do not explicitly exercise those overrides. Cite existing test cases that prove this or add targeted cases.

_copilot / gpt-5.6-sol_
