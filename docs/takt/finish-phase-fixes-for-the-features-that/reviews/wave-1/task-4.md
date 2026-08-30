# Review: finish-phase-fixes-for-the-features-that task 4 — rework

Most requested prose is correct, but one required archive-step guarantee is not actually stated, and the design-doc diff includes substantial unrelated host-generation documentation.

- **major** docs/superpowers/specs/2026-08-24-takt-design.md:939 — Archive-step no-write guarantee was weakened to a commit-only claim: The task explicitly requires narrowing the old statement to say that the archive step writes nothing after its commit. The replacement only says its commit is the last one it takes, which does not exclude non-commit writes by that step. State the no-write-after-commit guarantee directly, then distinguish the later retro-done and re-taken archive operations as separate command invocations.
- **major** docs/superpowers/specs/2026-08-24-takt-design.md:111 — Design document contains unrelated host-generation changes: The changes at lines 111-130, 607-622, and 1252 describe generated Copilot skills, hostgen behavior, and prompt tests. None belongs to the four design-document regions specified by this prose-retirement task. Revert or separate these unrelated changes so this task contains only the requested archive, retro-lock, and doneRetro documentation updates.

_copilot / gpt-5.6-sol_
