# Goals — lets-work-on-69

## Anchor
```text
lets work on #69
```

## Goals
- G1 — Once the plan gate has taken three review rounds since the newest `gate_rounds_reset` without closing, `decidePlan` asks `gate_review_capped` with `gate: "plan"` and the round count, instead of emitting a fourth `exec takt review plan`. · signal: test · evidence: `TestPlanReviewRoundsAreCapped` in `internal/decide` — `PlanRounds = 2` still execs, `= 3` asks with three choices
- G2 — A plan `rework`/`reject`/`error` verdict waiting to be answered outranks the round cap: with both conditions true the user is shown `gate_review`, never `gate_review_capped`. · signal: test · evidence: `TestPendingPlanReworkVerdictOutranksTheRoundCap` in `internal/decide`, which fails if the two checks are swapped
- G3 — `Facts.PlanRounds` is filled from `gate.Rounds(events, gate.Plan)` in the existing plan branch of `gatherGateFacts`, so the cap counts only plan reviews and only since that gate's newest reset. · signal: test · evidence: a `internal/cli` facts test over an events log mixing spec and plan `gate_reviewed` and `gate_rounds_reset` entries
- G4 — Answering the capped plan gate works through the existing gate-agnostic paths, and touches only that gate: *accept* records `gate_overridden` for the plan gate at the plan hash with the reason and carries the plan findings forward, *retry* appends `gate_rounds_reset{gate: "plan"}`, *stop* leaves the gate open. · signal: test · evidence: three `cmd_answer` tests, one per choice — only *retry* has the spec-gate precedent at `cmd_answer_test.go:84` — plus a negative test that none of them touches the spec gate's receipt or round count
- G5 — Both prompts describe `gate_review_capped` as a spec **or plan** review, identically, and every existing prompt-parity test still passes. · signal: test · evidence: `commands/takt.md` and `hosts/copilot/skills/takt/SKILL.md` diff to the same sentence; `go test ./internal/prompt/...`
- G6 — Neither design document contradicts the behaviour. Base design: §7.2's "the plan gate keeps today's behaviour entirely, including its uncapped rounds" is gone, §5.3 row 9 carries the cap the way row 6 does, §5.4 and §5.2 describe the cap as a review gate's rather than the spec gate's. Fixed-point design: D3, D5, §3, §8, §11, A4 and §13 each carry an amendment naming #69, with their original text kept. · signal: docs · evidence: the eleven passages read against the shipped `decidePlan`; no unamended sentence in either document asserts the plan gate is uncapped
- G7 — The spec gate's own capped-gate behaviour is unchanged: its cap, its precedence and its fixed-point *revise* semantics still hold. · signal: test · evidence: `TestSpecReviewRoundsAreCapped` and `TestPendingReworkVerdictOutranksTheRoundCap` pass untouched
- G8 — The branch is green on the repository's own checks. · signal: command · evidence: the run's verify commands pass at HEAD
