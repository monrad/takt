# Review: sweep-the-open-issue-backlog-fix-the task 9 — approve

The change matches the task: both host prompts contain identical push_pr guidance and the absolute-path invariant, design §7.5 documents the new title/body inputs with no --fill, and the cross-host test pins both required clauses.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests] minor docs/superpowers/specs/2026-08-24-takt-design.md:855 — Design doc §7.5 change has zero automated test coverage: No test in the repository loads or asserts against docs/superpowers/specs/2026-08-24-takt-design.md (confirmed by search — only internal/prompt/prompt_test.go covers commands/takt.md and hosts/copilot/skills/takt/SKILL.md). Task 9 item (3) requires this file's `gh pr create --base <base> --fill` to become the title/body-file form, but that edit is verified only by manual/reviewer inspection, not by any test — so a stale re-edit of this file later would go undetected.
- [lens:tests] major internal/prompt/prompt_test.go:91 — No regression test asserts `--fill` is gone: Task 9 requires 'no `--fill` remains in either' (commands/takt.md, hosts/copilot/skills/takt/SKILL.md) and 'no `--fill` remains in the file' (docs/superpowers/specs/2026-08-24-takt-design.md). The new crossHostInvariants entries (lines 91-94) only assert the new text is present via `mustContain`; nothing asserts `--fill` is absent from the op-table `run` bullet in either prompt file or from design doc §7.5. A future edit (e.g. a bad merge that restores `gh pr create --base <base> --fill` alongside the new text) would pass every existing test. Compare internal/brief/brief_test.go:180 and internal/cli/finish_test.go:583,634-635, which do assert `--fill` absence for the Go-generated op instructions — the prose files got no equivalent negative check.
- [lens:tests] minor internal/prompt/prompt_test.go:94 — Invariant-bullet placement ("immediately after") is unverified: Task 9 item (2) specifies the new bundle-inspection bullet must be inserted 'immediately after' the `state.json`/`events.jsonl` bullet in both files' Invariants sections. `mustContain` (line 161) only checks substring presence anywhere in the section text, so the ordering requirement isn't pinned by TestPromptInvariantsReadTheSameOnEveryHost — a later reorder that leaves the bullet elsewhere in the section would still pass.
