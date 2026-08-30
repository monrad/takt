# Review: plan — rework

The decomposition covers the specification comprehensively, but the primary G1 verification does not prove that the real backend execution path uses the mirrored fallback. The plan should strengthen that test before execution; the remaining issues are minor consistency gaps.

- **major** :0 — Task 1 tests the fake seam, not the real runCLI fallback: Task 1’s cross-package test drives fakeReviewer.Review, which newly calls resolveTimeout itself. It can pass even if runCLI—the production path named by A1—does not call resolveTimeout or applies a different unset-timeout fallback. The grep checks only that resolveTimeout exists. Add verification that exercises or otherwise directly pins runCLI’s use of the shared resolver; otherwise G1’s no-drift evidence is indirect and defeatable.
- **minor** :0 — Task 1 disagrees about the explicit-timeout cancellation row: The plan.md Task 1 section requires a second row with a short explicit Timeout and longer fake sleep, but plan.index.json Task 1 specifies only the unset-timeout fallback test and its verify selector does not clearly require the cancellation row. Either include the row explicitly in the executable task or remove it from plan.md.
- **minor** :0 — Boundary tests weaken a representable strict-containment case: Task 2 classifies MaxDuration-SessionMargin and MaxDuration-Grace as saturation points where only non-strict bounds are asserted. At exactly those inputs, adding the margin produces representable MaxDuration and is still strictly greater than the input. Assert the strict relation at equality, reserving the non-strict-only rule for inputs above the threshold.

_copilot / gpt-5.6-sol_
