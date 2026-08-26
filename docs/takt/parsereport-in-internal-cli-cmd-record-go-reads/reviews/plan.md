# Review: plan — rework

The decomposition covers the specification, but the documentation tasks have verification that can pass without making the required edits.

- **major** plan.md:0 — Task 3 verification does not prove the contract sentence changed: The unrestricted greps can match “decorated” anywhere, while hostgen --check proves only generated-file parity. Verify the specific report-contract sentence and its marker/emphasis tolerance.
- **major** plan.md:0 — Task 4 verification does not prove the command-table row: Whole-file greps for two words do not establish that the takt record row changed, retained the stale-attempt text, stayed one row, or documented anchoring and the opener rule. Add a row-targeted verification.

_copilot / gpt-5.6-sol_
