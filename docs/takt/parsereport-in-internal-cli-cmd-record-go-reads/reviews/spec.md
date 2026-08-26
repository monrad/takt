# Review: spec — rework

The intended tolerance is sound, but the specified algorithm contradicts the accepted grammar and examples, so planning would produce ambiguous behavior.

- **major** spec.md:0 — Value-cleaning algorithm cannot produce required malformed-decoration results: Step 4 strips only a prefix matching the decoration before the key, then one balanced value wrapper. Thus `STATUS: done**` remains `done**`, and `*STATUS:** done` becomes `* done`, although both are explicitly required to yield `done`. Define deterministic stripping rules consistent with the accepted grammar and examples.
- **major** spec.md:0 — Tests do not cover independently varying decoration runs: The grammar says every decoration slot is independent, but the proposed cross-product has only one D axis, apparently reusing the same run across all slots. It therefore cannot establish acceptance of combinations such as `*STATUS:** done` or differing key/value wrappers. Add independent axes or narrow the grammar.
- **nit** spec.md:0 — Balance-rule cross-reference is incorrect: The sentence referring to “Step 3's balance requirement” appears under Step 4 and describes Step 4 item 3.

_copilot / gpt-5.6-sol_
