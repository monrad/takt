# Goals — lets-work-on-60-and-62

## Anchor
```text
lets work on #60 and #62
```

## Goals
- G1 — The shipped backend review deadline is 15m for both `copilot` and `claude`, and the unset-`Timeout` fallback in `internal/backend/run.go` agrees with it. · signal: test · evidence: `internal/config` defaults test asserts 15m for both backends; `go test ./internal/config/... ./internal/backend/...` passes.
- G2 — Every deadline that wraps a backend call strictly exceeds its worst case: `reviewTimeoutS > backend timeout`, `closeWaveTimeout >= 2×backend timeout + verify_timeout`, `closeTimeoutS >= closeWaveTimeout`. · signal: test · evidence: a new envelope test in `internal/decide` asserts the three relations against the config defaults and fails if any one constant is edited alone.
- G3 — The `review_error` gate's retry option names `backends.<name>.timeout` and its current deadline for each configured reviewer backend, degrading to the literal key when none are configured, with no health probe added to `gatherFacts`. · signal: test · evidence: `internal/decide` question test asserts the key and deadline appear in the retry option for a configured backend set and for an empty one.
- G4 — A `takt next` that emits the `push_pr` op leaves `finish/pr.md` committed in HEAD, so the body the PR is created from is in the branch; an immediate replay adds no commit. · signal: test · evidence: `internal/cli` test drives `next` to the `push_pr` op, asserts `finish/pr.md` is in HEAD, replays and asserts the HEAD sha is unchanged.
- G5 — Archiving a `pr`-disposition run hands the unpushed commits back as `cleanup` only when git says the branch is ahead of `origin/<branch>`: ahead → `git push origin <branch>`, fully pushed → no cleanup, no tracking ref → the `-u` form. · signal: test · evidence: `internal/cli` archive tests cover all three git states.
- G6 — The docs no longer contradict the code: the `5m` timeout in `README.md` and the design doc's config example reads `15m`, and design §7.5 step 5 no longer claims `pr` asks git for nothing. · signal: docs · evidence: `grep '"timeout": "5m"' README.md docs/` returns nothing; §7.5 step 5 describes the `pr` remote-tracking check.
- G7 — The whole repository gate passes on the finished branch. · signal: command · evidence: `task check` (build + `go test ./... -race -count=1` + lint + host parity) exits 0.
