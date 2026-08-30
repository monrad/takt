The spec gate stops asking the backend after three rounds. The plan gate never stops.

## Goals

- G1 — Once the plan gate has taken three review rounds since the newest `gate_rounds_reset` without closing, `decidePlan` asks `gate_review_capped` with `gate: "plan"` and the round count, instead of emitting a fourth `exec takt review plan`. — achieved
- G2 — A plan `rework`/`reject`/`error` verdict waiting to be answered outranks the round cap: with both conditions true the user is shown `gate_review`, never `gate_review_capped`. — achieved
- G3 — `Facts.PlanRounds` is filled from `gate.Rounds(events, gate.Plan)` in the existing plan branch of `gatherGateFacts`, so the cap counts only plan reviews and only since that gate's newest reset. — achieved
- G4 — Answering the capped plan gate works through the existing gate-agnostic paths, and touches only that gate: *accept* records `gate_overridden` for the plan gate at the plan hash with the reason and carries the plan findings forward, *retry* appends `gate_rounds_reset{gate: "plan"}`, *stop* leaves the gate open. — achieved
- G5 — Both prompts describe `gate_review_capped` as a spec **or plan** review, identically, and every existing prompt-parity test still passes. — achieved
- G6 — Neither design document contradicts the behaviour. Base design: §7.2's "the plan gate keeps today's behaviour entirely, including its uncapped rounds" is gone, §5.3 row 9 carries the cap the way row 6 does, §5.4 and §5.2 describe the cap as a review gate's rather than the spec gate's. Fixed-point design: D3, D5, §3, §8, §11, A4 and §13 each carry an amendment naming #69, with their original text kept. — achieved
- G7 — The spec gate's own capped-gate behaviour is unchanged: its cap, its precedence and its fixed-point *revise* semantics still hold. — achieved
- G8 — The branch is green on the repository's own checks. — achieved

## Run

Bundle: docs/takt/lets-work-on-69/ — spec.md, plan.md, reviews/, retro.md
