# Review: lets-work-on-60-and-62 task 6 — approve

The change matches the task: context is threaded through the sole production call path, finish/pr.md is committed with the required subject before push_pr is emitted, commit failures propagate, replay produces no additional commit, direct test callers are updated, and the requested repository/external-bundle behavior is covered.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:tests] nit internal/cli/brief_stable_test.go:188 — Added assertion re-invokes commitBundle rather than checking preparePushPR's own call: Lines 188-191 call `commitBundle(context.Background(), r.ws, r.bdir, r.slug, "pr body")` a second, separate time to assert `committed==false`. Since `r.ws` is the same zero-value workspace (Dir.InRepo false) that preparePushPR's own internal commitBundle call at line 171 already ran through without error, this second call is a fresh, independent invocation rather than an observation of what preparePushPR actually did — it documents commitBundle's no-op behaviour in isolation more than it verifies the integration point, though it does satisfy the brief's explicit request for 'one assertion making that explicit'.
- [lens:tests] minor internal/cli/cmd_next.go:1066 — preparePushPR's new commitBundle error path is untested: The new `if _, _, err = commitBundle(ctx, r.ws, r.bdir, r.slug, "pr body"); err != nil { return err }` propagates a commit failure exactly like the pre-existing read/write failures preparePushPR already returns, but no test forces commitBundle to fail here (e.g. a broken repo/lock) to confirm the error surfaces through preparePushPR and ultimately fails `run` the same way assertPreparePushPRRefuses covers for the spec/goals reads. The codebase already has a pattern for simulating git-commit failures (internal/cli/archive_test.go's TestArchiveCommitIsRetriedAfterASoftReset) that could have been reused here.
