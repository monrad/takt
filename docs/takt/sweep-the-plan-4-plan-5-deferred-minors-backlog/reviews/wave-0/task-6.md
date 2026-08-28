# Review: sweep-the-plan-4-plan-5-deferred-minors-backlog task 6 — approve

The change correctly adds read-only manifest version printing, stamps the emitted binary while retaining the full compile check, and includes the required failure and no-write tests.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:consistency] minor Taskfile.yml:14 — New ldflags stamp has no regression test, unlike its two siblings: internal/prompt/dist_test.go already pins flake.nix and .goreleaser.yaml to the identical ldflags target (`internal/version.Version=`) with automated tests (TestFlakeReadsThePluginVersion, TestGoreleaserStampsTheVersion), and TestGoreleaserStampsTheVersion's own comment (internal/prompt/dist_test.go:63-64) says it 'pins the release build to the same ldflags path the flake and the Taskfile use' — treating all three as one synchronized invariant. Task 6 adds the third stamp to Taskfile.yml:14 but no Go test reads Taskfile.yml or asserts its -ldflags target/package path match the other two, so a future typo in the -X path (which go build silently ignores rather than erroring on) would go undetected by `go test ./...`, reproducing the exact silent-0.0.0-dev-stamp class of bug this task fixes.
- [lens:docs] minor internal/tools/setversion/main.go:1 — Package doc comment doesn't mention the new --print mode: The package-level comment (lines 1-9) describes setversion purely as the tool that rewrites the version via `task version:set VERSION=x.y.z`. This diff adds a second, independent mode (`--print`, a read-only path used by `task build`) that is never mentioned in that comment, so a reader running `go doc ./internal/tools/setversion` sees only the rewrite mode's contract, not the read mode's.
