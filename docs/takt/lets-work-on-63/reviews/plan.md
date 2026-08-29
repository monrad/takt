# Review: plan — rework

The decomposition covers the specification, but several declared verification paths can pass while load-bearing requirements are violated. Strengthen Tasks 5, 7, and 8 before execution.

- **major** plan.md:0 — Task 5 does not verify its sole-caller and exactly-once ownership rule: Task 5 makes retroRunOp the sole caller of writeRetroArtifacts and explicitly forbids either command path from deriving the pair twice, but its checks only remove writeRetroInputs and establish byte-identical replay. Because derivation is deterministic, calling writeRetroArtifacts twice would still pass every listed test. Add a static call-site assertion or an instrumented test proving one derivation per command; Task 7 should also assert cmdRetro has no direct call.
- **major** plan.md:0 — Task 8's verification does not prove the required design-document rewrite: Task 8 claims its checks scope the edit to §7.5 step 3, but the sed range covers all of §7.5. The checks only require retro-skeleton.md, not yet chosen, and archived somewhere in that section; they do not prove that step 3 names the seven sections, shipped-event semantics, decision sources, Not-proven seed, follow-up bucketing, Numbers, copying/filling prose slots, or the done guard. Scope checks to step 3 and assert the load-bearing content so an incomplete documentation edit cannot pass.
- **minor** plan.md:0 — Task 7 leaves the required --dir path untested: Task 7 requires the standard --slug/--dir pair, but every described command test uses --slug and the verification commands add no coverage for --dir. Add a successful rewrite test targeting a run through --dir so omission or incorrect wiring of that required flag is detected.

_copilot / gpt-5.6-sol_
