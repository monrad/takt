# Review: plan — rework

The decomposition and scopes are plausible, but several verify sets can pass while explicit spec requirements remain unmet. Tasks 3–5 need stronger assertions before execution.

- **major** plan.md:78 — Task 3 does not verify that the override reason is recorded: Spec §§5 and 7 require the plan-gate `gate_overridden` event to contain the user's reason. Task 3 checks that an empty reason fails and that the resulting event has the plan gate and current hash, but never asserts `Data["reason"] == "known gap"`. The tests could pass if the reason were accepted by the CLI and then discarded.
- **major** plan.md:91 — Task 4's greps do not prove the prescribed sentence replacement: Each verification only searches for the new substring, while the prompt parity tests do not anchor this sentence. Both commands would pass if the new phrase were appended elsewhere and the false “the spec review” sentence remained, or if the options were changed. Require the complete expected gate sentence in both files and assert the obsolete wording is absent.
- **major** plan.md:100 — Task 5's base-design checks omit the cap threshold and precedence semantics: The row-9 check proves only that `ask gate_review(plan)` and `gate_review_capped` occur on one line; it does not require `PlanRounds ≥ maxAgentAttempts (3)`, that pending rework/reject/error comes first, or that normal review remains the fallback. Likewise, the §7.3 check only requires the gate id, not three rounds since the newest reset. The documented behavior could therefore be materially wrong while every verify command passes.
- **minor** plan.md:25 — Task 1 does not verify the required PlanRounds documentation: The spec gives an explicit two-line field comment, but Task 1 only greps for `PlanRounds int`. Tests and lint do not prove that the required comment exists or identifies the plan gate and newest `gate_rounds_reset`; add an anchored check for that documentation.

_copilot / gpt-5.6-sol_
