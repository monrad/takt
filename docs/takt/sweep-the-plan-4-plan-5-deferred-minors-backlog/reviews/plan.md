# Review: plan — rework

The executable tasks cover the code changes, but the plan is internally stale, omits #9’s required bookkeeping closure, and lacks a reliable post-join verification gate.

- **major** plan.md:5 — Task count, ownership, and waves contradict plan.index.json: The narrative declares eight tasks and puts T2 in wave 1, while the index has nine tasks and T2 depends on T9. The actual graph is wave 1: T1/T4/T5/T6/T7/T9; wave 2: T2; wave 3: T3/T8. Lines 43-46 also assign the warnings contract to T2 before lines 69-80 assign it to T9. Update the narrative, risks, and class accounting to match the executable graph.
- **major** plan.md:87 — The required #9 bookkeeping closure is explicitly dropped: Spec.md says #9 is closed as part of this run’s bookkeeping, but the plan states that closing the GitHub issue is not performed. T3’s grep only proves local prose exists; it cannot perform or verify the closure. Add an explicit bookkeeping task or reconcile the specification before execution.
- **major** plan.md:16 — Concurrent repo-wide gates do not prove the assembled result: T3 and T8 can execute concurrently in the final wave. Each may run its full tests and lint before the other task’s edits are complete, so neither command necessarily validates the final combined tree. The accepted-risk claim at lines 184-189 is therefore unsound. Add a dependent final verification task or another post-wave gate that runs after both T3 and T8.
- **nit** plan.md:190 — T2 file-count risk is stale: The risk says T2 has twelve files, while line 69 and plan.index.json declare ten. Correct the count and avoid claiming that all four subchanges share the same call sites.

_copilot / gpt-5.6-sol_
