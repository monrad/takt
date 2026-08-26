# Review: plan — rework

The decomposition covers the requested files and behavior, but verification leaves central grammar and documentation requirements unproven.

- **major** plan.md:42 — Task 1 omits decisive marker-boundary tests: Task 1 does not test that `--`, `>>`, and whitespace-followed `**` are not markers, that tabs satisfy mandatory marker whitespace, or that non-ASCII digits/signs are rejected for ordered markers. Implementations violating those explicit grammar rules could pass every listed verify command.
- **major** plan.md:80 — Tasks 3 and 4 use semantic-insufficient greps: Their checks only require a few keywords. Task 3 can pass without stating the plain-line contract or supported decoration forms; Task 4 can pass without preserving uppercase/colon anchoring or the opener-dependent trailing-run rule. `hostgen --check` proves generation parity, not contract content.

_copilot / gpt-5.6-sol_
