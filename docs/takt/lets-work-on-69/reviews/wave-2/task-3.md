# Review: lets-work-on-69 task 3 — rework

The plan-cap scenarios are covered, but three tests omit an explicitly required assertion about the capped question's context.

- **major** internal/cli/cmd_answer_plan_test.go:177 — Three answer tests do not verify the plan-gate context and attempt count: The task requires each of the four tests to assert that the initial `next` result has context `gate == "plan"` and `attempts == float64(3)`. Only TestPlanReviewRoundCapAsksThenRetryReviewsAgain checks these fields. The accept, stop, and spec-independence tests at lines 177, 234, and 262 check only `op` and `gate`, so they could proceed despite an incorrectly scoped or incorrectly counted capped question. Reuse a helper that validates the complete ask shape in every test.

_copilot / gpt-5.6-sol_
