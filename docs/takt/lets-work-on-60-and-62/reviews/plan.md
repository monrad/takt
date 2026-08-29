# Review: plan — rework

The decomposition covers the specification comprehensively, but Task 1 contains conflicting implementation contracts and Task 4's verification omits a required fail-closed test. Resolve these before execution.

- **major** plan.md:0 — Task 1 contradicts itself about what the fake reviewer records: Task 1 initially requires TAKT_FAKE_REVIEW_TIMEOUT_FILE to receive d.String(), while its round-5 correction requires the remaining duration obtained from the applied context's Deadline(). These values and evidentiary strength differ. plan.md also retains stale language saying the seam records the resolved value. Replace all superseded language so Task 1 consistently requires recording time remaining on the actual work context, with the unset-timeout tolerance assertion and explicit-timeout cancellation check.
- **minor** plan.md:0 — Task 4's verify filter does not execute its index-error test: Task 4 requires TestGatherFinishFactsPropagatesAnIndexReadError, but its CLI command uses `-run TestGathered`, which does not match that test name. The later whole-repository gate would eventually run it, but Task 4's own verification does not prove its fail-closed containment requirement. Expand the filter to include `TestGatherFinishFactsPropagatesAnIndexReadError` or rename the test consistently.

_copilot / gpt-5.6-sol_
