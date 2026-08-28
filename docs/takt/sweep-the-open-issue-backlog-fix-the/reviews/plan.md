# Review: plan — rework

The decomposition broadly covers the spec, but several execution and verification defects must be corrected before starting.

- **blocking** plan.md:0 — T5 and T6 are not actually file-disjoint: Both run `task hosts:gen`, which may rewrite both generated agent files while the tasks execute concurrently in wave 1. This violates the claimed disjoint scopes and can produce out-of-scope edits or races. Serialize them or assign all generated outputs to one task.
- **major** plan.md:0 — T2 exposes an unsupported retry choice for an entire wave: T2 adds `retry` to the question, but handling it is deferred to T10. The plan explicitly acknowledges that the wave-1 tree offers a choice `answerGateReview` rejects. Move the handler into T2 or restructure the dependency so no committed wave has contradictory behavior.
- **major** plan.md:0 — T13 does not address the scoped log by LogID: T13 replaces `ReadDir` with a broad `review-spec-*.prompt` glob and selects the newest file. That is still a directory-wide heuristic, not lookup using the second call's LogID as required, and stale files can satisfy the assertion. Capture the call's actual LogID and read its exact prompt path.
- **major** plan.md:0 — T5's invalid-citation test conflicts with the attempt cap: The proposed test submits six invalid replies against one run while expecting one `goals_invalid` event for each. The spec says invalid replies reach `agent_invalid` at the cap, so later cases cannot reliably exercise citation validation. Use a fresh run per malformed citation or otherwise reset the attempt state.
- **major** plan.md:0 — T12 silently treats goals-record read failures as missing verdicts: T12 says any `finish.ReadGoals` error may become nil and therefore `not assessed`. The spec permits `not assessed` when no verdict exists, not when an existing record is unreadable or malformed. Ignore only an explicit not-found result and propagate other errors.
- **minor** plan.md:0 — T5's containment predicate rejects valid repository paths: Testing whether `filepath.Rel` merely starts with `..` rejects contained names such as `..foo`. Check `rel == ".."` or a `..` plus path-separator prefix instead, and add a regression case.

_copilot / gpt-5.6-sol_
