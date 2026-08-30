# Review: lets-work-on-60-and-62 task 4 — rework

The deadline calculations are mostly wired correctly, but finish fact gathering violates the required error-propagation contract and can bypass malformed index or extras errors during healing. The working tree also contains changes outside the declared files.

- **major** internal/cli/finish_facts.go:68 — gatherFinishFacts does not gather or validate the verify-command union: The task explicitly requires gatherFinishFacts to call readIndex and finish.ReadExtra, populate FinishFacts.VerifyCommands from finish.UnionCommands, and propagate errors. Instead, gatherFacts computes the count separately and only when Finish.Verified is false. cmd_next.go's healing path calls gatherFinishFacts directly, so it can heal a successful verification despite a malformed index or extras file; afterward the count is skipped because the run is marked verified. The new error-path test even requires gatherFinishFacts to succeed, contrary to the specified contract. Move union counting and error propagation into gatherFinishFacts and update the tests accordingly.
- **major** docs/takt/lets-work-on-60-and-62/events.jsonl:68 — Working tree contains changes outside the declared task files: The worktree includes modified and untracked files under docs/takt/lets-work-on-60-and-62, including events, state, follow-ups, close records, reviews, and wave artifacts. These are outside the seven declared internal/ files and must be excluded from the submitted change set.

_copilot / gpt-5.6-sol_
