The spec gate stops asking the backend after three rounds. The plan gate never stops.

Closes #69.

## What changed

`decidePlan` gains one branch — `if f.PlanRounds >= maxAgentAttempts { ask(gateReviewCapped, {gate: plan, attempts}) }` — placed after the `needsRework` guard, so a `rework`/`reject`/`error` verdict waiting to be answered still outranks the cap. `Facts.PlanRounds` is filled by `gate.Rounds(events, gate.Plan)` inside the plan branch of `gatherGateFacts` that `Config.Review.Plan && HasIndex && IndexValid && plan.md` already guards.

Everything downstream was already gate-agnostic and is untouched: `gate.Rounds` counts per gate id, `questionGateReviewCapped` renders the gate name from context, and `answerGateReviewCapped` resolves the gate from the pending payload — so *accept* → `gate_overridden{plan}`, *retry* → `gate_rounds_reset{gate: "plan"}`, *stop* all work as they stand. No new gate id, event type, question or answer path.

Thirteen production and prose lines; the rest is tests (three new files) and documentation. Both prompts now say `gate_review_capped` is "a spec or plan review". Both design documents are corrected: the base design at five sites, and the fixed-point design **superseded in place** at seven — its D3 ("applies to the spec gate only … including its uncapped rounds") and A4 ("the plan gate's uncapped rounds are tolerable … if it becomes the next bottleneck, D5's cap is the cheapest thing to extend to it") keep their original text and gain an amendment naming this issue. A4 predicted exactly this change; it needed no semantic change, as predicted.

## Notes

- **Task 3 is waived.** Its four `cmd_answer` tests exist, pass, and were mutation-checked, and all six internal lenses cleared its final attempt — but the per-task reviewer's blocking finding named `docs/takt/lets-work-on-69/**` (takt's own bookkeeping, which every implementer brief forbids touching) as an out-of-scope edit, three rounds running. No attempt can satisfy it. The one real remaining item — the fixture asserts a dispatch happened, not that it is the planner — is recorded as a follow-up.
- This run's own plan gate took four rounds and closed by override, because the cap it builds did not exist yet. See `retro.md`.
- The branch also carries two cherry-picked bundle-only commits (`68835fb`, `ca12aec`) that archive the earlier `sweep-the-plan-4-plan-5-deferred-minors-backlog` run; its PR (#52) merged before those commits were written, so main never received them.

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
