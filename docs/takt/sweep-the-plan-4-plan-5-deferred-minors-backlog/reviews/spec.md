# Review: spec — rework

The approach is viable, but several requirements cannot be implemented correctly as written.

- **blocking** spec.md:87 — `--force` would log non-takeovers: Grading `r.force` first regardless of `Acquire` outcome records `lock_taken` when no holder exists or the caller already holds the lock. Require a different existing holder; otherwise this creates false audit events and tracked-file churn.
- **blocking** spec.md:126 — Atomic-write scope omits task briefs: Issue #5 and G2 cover all agent briefs, but T2 omits `internal/cli/launch.go`, where `renderTaskBrief` uses `os.WriteFile`. Include that call site; there are four relevant call sites, not three.
- **blocking** spec.md:137 — Degradation JSON contract is undefined: Neither ruling names the shared field, its value shape, nor its location. This is especially blocking for `next`, whose typed `op.Op` has no such top-level field. Define the exact wire contract and required files/tests.
- **blocking** spec.md:131 — Gitignore escaping omits backslashes: A backslash is itself gitignore syntax and is legal in a Unix directory name. The specified escaper would fail to match such a custom bundle path; it must escape literal backslashes before other metacharacters.
- **blocking** spec.md:187 — Hostgen task omits the source-path caller: `hosts.RenderCopilotAgent` currently receives only a name and bytes; the real path exists only in `internal/tools/hostgen/main.go`. T5 must include that caller and its tests, and its verify command must run `internal/tools/hostgen`.
- **blocking** spec.md:203 — Stamped build still would not create `./takt`: Adding ldflags to the existing `go build ./...` compiles multiple packages but emits no `takt` binary, so the stated verification cannot pass. Specify an output and main package, such as `go build -o takt ... ./cmd/takt`.
- **major** spec.md:232 — Wave rationale is applied inconsistently: T7 is deferred because package compilation can race in the shared worktree, yet T1 and T2 both edit and verify `internal/cli` concurrently in wave 1. The same hazard applies and can expose either test run to another task's partial edits.
- **major** goals.md:18 — `go install` success criterion is factually overbroad: Tagged `go install github.com/monrad/takt/cmd/takt@vX.Y.Z` reports the module release version via build info, not the dev version. Qualify this as a local unstamped `go install`.
- **minor** goals.md:5 — Anchor miscounts fixes and rulings: It calls the fourteen listed issues “fourteen fixes and two rulings,” although #4 and #16 are among those fourteen and #9 requires no fix. State the intended eleven code/doc fixes, two rulings, and one already-fixed closure.

_copilot / gpt-5.6-sol_
