# Review: lets-work-on-63 task 6 — rework

Core retro behavior is implemented correctly, but the change set includes files outside the declared scope and slightly alters the existing non-empty-file semantics.

- **major** docs/takt/lets-work-on-63/events.jsonl:96 — Change set modifies files outside the task's declared scope: The worktree also modifies events.jsonl, follow-ups.json, state.json, waves/2/close.s1.json, and adds reviews/wave-3 and waves/3 files. The task declares only internal/cli/cmd_done.go, internal/cli/cmd_verify.go, and internal/cli/finish_test.go; unrelated run-state artifacts must be excluded from the submitted patch.
- **minor** internal/cli/cmd_done.go:221 — Retro validation replaces rather than preserves fileNonEmpty semantics: The task requires retaining the fileNonEmpty check and then reading retro.md. Combining os.ReadFile with strings.TrimSpace newly rejects non-empty whitespace-only files and reports all read errors, including permission errors or a directory path, as “missing or empty.” Preserve fileNonEmpty, then read the file separately and surface a genuine read error consistently before scanning for the prose marker.

_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests] minor internal/cli/finish_test.go:306 — `got["ignored"] != nil` assertions are tautological on the failure path: In TestDoneRetroRefusesUnfilledProseSlot (line 306) and TestDoneRetroRefusesARecordedRetroInExecute (line 346), the refusal assertions check `code != 1 || got["ignored"] != nil`. `got` is unmarshalled only from stdout (cmd_init_test.go:17-33), while `fail()` writes exclusively to stderr (cli.go:158-161), so on any refusal (code 1) stdout is empty and `got` is nil — `got["ignored"]` can never be non-nil here. The check adds no real coverage of the 'must not be marked ignored' intent; the actual no-new-receipt guarantee is (correctly) covered separately via countEvents before/after.
