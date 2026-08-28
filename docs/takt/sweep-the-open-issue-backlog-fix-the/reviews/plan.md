# Review: plan — rework

Two tasks do not fully prove or implement their stated contracts.

- **blocking** plan.md:0 — T10 does not handle a pre-existing forced-review receipt: T10 claims every pre-receipt failure leaves no receipt and forces a rerun, but a failed `review --force` can leave the prior same-hash receipt intact. The next unforced review may return that cached receipt. Invalidate the old receipt before starting a forced pass and add a failure-injection test covering this case.
- **major** plan.md:0 — T5 drops citation problems when verdict parsing also reports problems: The spec and plan.md say citation violations are appended to ParseVerdicts problems, but T5's executable description runs CheckCitations only after ParseVerdicts succeeds and returns citation problems separately. Revise the task to aggregate both problem sets in order and test a reply containing both semantic and citation errors.

_copilot / gpt-5.6-sol_
