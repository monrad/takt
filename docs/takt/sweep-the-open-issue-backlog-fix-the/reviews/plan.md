# Review: plan — rework

The decomposition is mostly comprehensive, but wave 1 publishes a push_pr contract that is not implemented until wave 2, and one explicit PR-note requirement is unassigned.

- **blocking** plan.md:0 — Wave 1 advertises push_pr inputs that do not exist until T12: T2 changes branch_finish text and T9 changes host instructions to require inputs.pr_title and inputs.pr_body_path, while T12—which creates those inputs, finish/pr.md, and the matching template—runs in wave 2. This contradicts plan.md's claim that every committed wave is self-consistent. Split those prose changes into T12 or add ordering that prevents publication before implementation.
- **major** plan.md:0 — The required issue #20 correction note is silently dropped: Spec section J says issue #20's incorrect retro path must be noted in the PR rather than edited. T8 only says it is not edited, and T12's generated PR body contains no such note. Assign this requirement to a task or explicitly define a PR-description operation that records it.
- **minor** plan.md:0 — The declared repository-wide test command is not actually verified: The spec requires `go test ./... -race -count=1`, but T10 and T12 run `go test -race ./...` without `-count=1`. Package-scoped commands do not prove the exact uncached repository-wide gate; add the specified command to a final task.

_copilot / gpt-5.6-sol_
