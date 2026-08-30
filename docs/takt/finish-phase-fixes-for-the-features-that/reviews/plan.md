# Review: plan — rework

The decomposition covers the four feature areas, but the execution gate does not prove the spec’s explicit prose-only constraint for #72, and two declared failure contracts lack direct verification.

- **major** plan.index.json:0 — Task 4 does not verify that its Go changes are comments only: Task 4 / G9 explicitly requires the diffs in internal/cli/archive.go and internal/cli/cmd_done.go to contain no executable-line changes. go build, tests, lint, hosts:check, and content greps can all pass after an unintended executable change, so they do not prove this requirement. Add a scoped diff check or equivalent structural verification that rejects changed non-comment Go lines in those two files.
- **minor** plan.index.json:0 — Task 4 drops the spec’s explicit vet and identifier-scope checks: Spec §5 calls for `go vet ./...` and greps establishing that the deleted `plainOp` identifier is named nowhere in the applicable source. Task 4 substitutes `task lint` without establishing that it runs the required vet command and checks `plainOp` only in internal/cli/archive.go. Add the explicit vet command and broaden the identifier check across the in-scope source tree while excluding the explicitly out-of-scope docs/takt bundle if necessary.
- **minor** plan.index.json:0 — Task 3 declares but does not test the missing-manifest failure path: Task 3 states that a root missing either commands/takt.md or .claude-plugin/plugin.json must return exitFailure naming the path, but its planned tests cover only the missing commands/takt.md case. Add a missing-manifest test that verifies both exitFailure and the reported path.

_copilot / gpt-5.6-sol_
