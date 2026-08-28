# Review: sweep-the-open-issue-backlog-fix-the task 6 — approve

The change implements the requested spec-path behavior, flattens finding details to one line, updates the follow-up verdict semantics, and adds appropriate unit and integration coverage. No blocking issues found.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:consistency] major internal/brief/brief.go:202 — Newline-flattening fix applied to PriorFindingLines but not to its twin CandidateLines: This diff adds `lineSafe` to collapse newlines in PriorFindingLines' free-text `f.Detail` before rendering one-finding-per-line (brief.go:244), with a doc comment explaining that an unflattened multi-line Detail would 'arrive as several [findings]' to the reviewing agent. CandidateLines (brief.go:202-208), a few lines above in the same file, renders VerifyCandidate.Detail — the same kind of free-form, agent-authored prose (from lens-review findings, wired through cmd_next.go:884) — in the exact same one-per-line format, with the exact same unflattened `c.Detail`. The identical hazard this diff explicitly fixes in one place is left live in the structurally identical sibling function it did not touch.
