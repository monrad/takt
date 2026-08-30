# Review: lets-work-on-69 task 1 — approve

The change implements the plan-review round cap exactly as specified, preserves verdict precedence, updates the gate-agnostic documentation, and adds focused tests without modifying the existing spec-gate tests.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:consistency] nit internal/decide/decide.go:345 — Order-is-load-bearing comment added only on the plan side: decidePlan's new cap check (decide.go:384-391) gets a four-line comment explaining why the needsRework check must precede the round-cap check. The identical shape in decideBrainstorm (decide.go:345-349), which decidePlan explicitly mirrors, carries no such comment. A future reader diffing the two blocks may wonder whether the spec-gate ordering is equally load-bearing but undocumented, or whether the two blocks have diverged. This matches what the task brief asked for (the spec branch's order is only 'implied' by shape), so it is not a defect, just a minor asymmetry worth being aware of if the two blocks are touched again.
- [lens:tests] nit internal/decide/decide_plan_cap_test.go:124 — Option-count assertion doesn't verify option identity: TestPlanReviewRoundsAreCapped checks `len(choices) != 3` but never checks the actual Choice values/order (e.g. accept/retry/stop), so a bug in questionGateReviewCapped that returned three wrong options (or the wrong order) would still pass this test. This exactly mirrors the same weak assertion in the precedent TestSpecReviewRoundsAreCapped (internal/decide/decide_test.go:1099), so it's a pre-existing pattern rather than a new regression, but since decide_plan_cap_test.go is new code in this diff and free of the G7 no-edit constraint that applies to decide_test.go, it could have asserted `slices.Equal(choices, []string{"accept", "retry", "stop"})` (as another test does at decide_test.go:987 for a related gate) for a stronger guarantee.
