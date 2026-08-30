# Review: spec — rework

The behavior change is coherent, but planning must wait until the documentation scope resolves an authoritative design contradiction. Two test-evidence gaps should also be tightened.

- **blocking** docs/superpowers/specs/2026-08-26-spec-gate-fixed-point-design.md:63 — The documentation plan leaves the fixed-point design contradicting the new cap: The proposed edits cover the base design but omit the referenced fixed-point design, whose D3 says the plan gate remains uncapped and whose A4 repeats that assumption. The base design continues to cite this document as authoritative. Amend or explicitly supersede D3, D5, §8, and A4 so G6 can be true and planners have one consistent decision record.
- **major** goals.md:12 — G4 requires three plan-gate answer paths but names evidence for only retry: G4 requires accept, retry, and stop to work for a capped plan gate, while its cited cmd_answer precedent at line 84 and spec §7 cover only retry. Existing accept/stop integration tests exercise the spec gate, not plan hashing and plan-finding carry-forward. Specify plan-gate integration coverage for accept and stop, or narrow G4's test claim.
- **minor** internal/decide/questions.go:188 — Implementation scope leaves a spec-only comment false: questionGateReviewCapped is documented as applying to the spec review. The runtime text is already gate-agnostic, but extending the question to plans without updating this comment leaves the implementation contradicting its own behavior. Add this comment to the documentation cleanup scope.

_copilot / gpt-5.6-sol_
