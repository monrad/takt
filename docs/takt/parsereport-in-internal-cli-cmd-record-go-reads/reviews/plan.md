# Review: plan — rework

The decomposition is otherwise complete, but Task 1 adds a parser behavior that contradicts the specified grammar.

- **major** plan.md:62 — `** STATUS: done` incorrectly required to match: Step 2 removes only the decoration run, not following whitespace; this leaves ` STATUS: done`, which fails Step 3's anchored key match. The accepted shape also places Dk-open directly adjacent to KEY. Remove this test requirement; `**STATUS: done` is the valid decoration-path case.

_copilot / gpt-5.6-sol_
