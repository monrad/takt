# Review: finish-phase-fixes-for-the-features-that task 4 — rework

The requested stale prose is largely replaced correctly, but the design now contradicts the implemented lock ordering and the diff contains unrelated design-document changes outside the four specified regions.

- **major** docs/superpowers/specs/2026-08-24-takt-design.md:909 — Archive step still claims the lock is released before the commit: Step 5 retains `lock released; commit`, while the newly added prose at line 913 says the session sidecar is cleared after the commit. The implementation and the task's required invariant have archive() reach applyAndStop holding the lock; applyAndStop clears it only after commitBundle returns. Restate the sequence as archive commit, then lock release, then disposition git work.
- **major** docs/superpowers/specs/2026-08-24-takt-design.md:111 — Design diff includes unrelated host-generation documentation changes: The task limits this document to four regions (§4.7, §5.1, and two regions in §7.5), but the diff also changes §3.3, §6.1, and §14 to document Copilot skill generation. Those unrelated hunks should be excluded from this task's change set.

_copilot / gpt-5.6-sol_
