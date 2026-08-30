# Cap the plan review gate's rounds (#69)

## 1. Problem

The spec gate stops asking the backend after three rounds. The plan gate never stops.

In the #60/#62 run (PR #64) the plan gate ran nine review rounds and closed only because
the user overrode it. Findings per round: 8, 4, 3, 3, 3, 2, 3, 4, 3 — not converging, and
the reason is visible in the content:

- Round 4's blocking finding ("Tasks 3 and 4 contradict Task 2's `GateReview` API") was
  created by round 3's fix, which changed `GateReview`'s arity in the spec and two places
  but not in all callers in the plan index.
- Round 5's blocking finding (`GateReview(bt) > bt` is impossible at `bt == MaxDuration`)
  was created by round 4's fix, which added saturating arithmetic and gave `Session` a
  domain exemption without extending it to its siblings.
- Round 6's findings were created by round 5's fix appending "CORRECTION" paragraphs
  instead of replacing the text they superseded.

Every finding was legitimate. What kept the loop alive was that each surgical patch to a
document of interlocking claims introduced a new inconsistency somewhere else. By round 8
the findings had drifted from spec-level contradictions to implementation detail (an `int`
ceil overflow, a test tolerance) — things the per-task review judges later against real
code rather than prose. Nine rounds is also nine full cross-vendor backend calls over
`spec.md` + `plan.md` + `plan.index.json`.

The exit already exists one phase earlier. `decideBrainstorm` asks `gate_review_capped` —
*accept* · *retry* · *stop* — once the spec gate has taken `maxAgentAttempts` (3) rounds
without closing, instead of reviewing a fourth time (design §7.2, fixed-point design §8).
`decidePlan` has no equivalent: it goes straight back to `exec takt review plan`, forever.

## 2. What changes

One branch in `decidePlan`, one fact that feeds it, and the **two** design documents that
currently say the cap is the spec gate's alone — the base design and the fixed-point design
it defers to (§6.1). No new gate id, no new event type, no new question, no new answer path.

## 3. The decision

`internal/decide/decide.go` — `Facts` gains `PlanRounds`, documented like its sibling:

```go
// PlanRounds is how many plan reviews have run since the newest
// gate_rounds_reset for the plan gate.
PlanRounds int
```

and `decidePlan` gains the cap, placed after the `needsRework` branch and before the
`exec`, mirroring `decideBrainstorm` line for line:

```go
if f.PlanRounds >= maxAgentAttempts {
    return ask(gateReviewCapped, map[string]any{
        ctxSlug: st.Slug, ctxGate: planGate, ctxAttempts: f.PlanRounds,
    })
}
```

**The order is load-bearing.** A `rework`/`reject`/`error` verdict waiting to be answered
outranks the cap, exactly as it does for the spec gate: while there is a verdict on the
table the user is shown `gate_review` with its *revise*, never *accept / retry / stop*.
The cap fires on the next `takt next` after the artifacts moved, when the receipt no
longer answers at the current hash and there is nothing left to answer.

`internal/cli/facts.go` — inside the existing plan branch of `gatherGateFacts` (already
guarded by `Config.Review.Plan && HasIndex && IndexValid && plan.md` non-empty):

```go
f.PlanRounds = gate.Rounds(events, gate.Plan)
```

## 4. What already works unchanged

Everything downstream of the ask is gate-agnostic today:

- `gate.Rounds(events, gate)` takes a gate id and counts `gate_reviewed` events for **that
  gate** since the newest `gate_rounds_reset` for it. Per-gate by construction.
- `questionGateReviewCapped` renders "The %s review has run %v times without closing the
  gate (findings in reviews/%s.md)" from `ctx["gate"]`. The plan question needs no new text.
- `answerGateReviewCapped` resolves the gate from the pending payload via
  `pendingGateName`: *accept* → `overrideGate(bdir, "plan", reason)`, *retry* →
  `gate_rounds_reset{gate: "plan"}`, *stop* → leaves the gate open.
- `gate_review_capped` is already in `decide.Vocab().Gates` and in both prompts' gate lists.
- `overrideGate` records `gate_overridden` at the current hash with the user's reason, and
  the retro's *Decisions* section already reports gate answers with their `--reason` —
  whichever gate.

## 5. What the user sees

| step | today | after |
|---|---|---|
| rounds 1–3 | review → `rework` → `gate_review` (*revise* · *accept* · *stop*) | unchanged |
| after the third round's edit | `exec takt review plan` — round 4 | `ask gate_review_capped` (gate `plan`, attempts 3) |
| *accept* | — | `gate_overridden` at the current hash with `--reason`; findings carried to the retro; the run proceeds to the alignment audit |
| *retry* | — | `gate_rounds_reset{gate: "plan"}`; the next `takt next` reviews once more, and the count starts over |
| *stop* | — | the gate stays open; the turn ends |

Three rounds and three *revise* offers before the cap — the same budget the spec gate has.

## 6. Documentation

| file | change |
|---|---|
| `commands/takt.md` §Gates | "`gate_review_capped` is **the spec review** after three review rounds without the gate closing" → "**a spec or plan review**"; the three choices stay as written |
| `hosts/copilot/skills/takt/SKILL.md` §Gates | the identical edit — the two prompts are cross-checked by `TestPromptInvariantsReadTheSameOnEveryHost` |
| design §5.3 table row 9 | mirror row 6: `exec takt review plan`; a rework/reject/error receipt → `ask gate_review(plan)`; else once `PlanRounds ≥ maxAgentAttempts` (3) → `ask gate_review_capped` |
| design §5.4 gate vocabulary | "`gate_review_capped` — **the spec gate's** review has run `maxAgentAttempts` (3) passes…" → a **review gate's** (spec or plan) review |
| design §5.2 events | the round-cap clause reads as the spec gate's ("its round cap"); make it the review gates' |
| design §7.2 | the closing sentence "It applies to the spec gate only — the plan gate (§7.3) keeps today's behaviour entirely, including its uncapped rounds" is now false. The fixed point's other halves — *revise* closing the gate on the edit alone, and the scoped confirming pass — stay spec-only; the round cap does not |
| design §7.3 | the plan gate gets its own sentence: three rounds since the newest reset, then `gate_review_capped` |
| `internal/decide/questions.go:188` | `questionGateReviewCapped`'s doc comment says "the **spec** review has taken `maxAgentAttempts` passes". Its rendered text is already gate-agnostic; the comment is not, and shipping the plan gate through it makes the comment false |

The design document is the repository's account of the decision table; leaving §7.2's
sentence standing would leave it asserting the opposite of the behaviour it describes.

### 6.1 The fixed-point design must be superseded, not left standing

`docs/superpowers/specs/2026-08-26-spec-gate-fixed-point-design.md` is the **second**
authoritative record here — the base design cites it as the account of the cap and does not
restate it. It says the opposite of this change in six places:

| site | text |
|---|---|
| D3 (line 63) | "Applies to the **spec gate only**. The plan gate keeps today's behaviour entirely, including its uncapped rounds." — `source: user` |
| D5 (line 65) | "Review rounds **at the spec gate** are capped at `maxAgentAttempts` (3)" |
| §3 (line 84) | "The plan gate is untouched: `decidePlan` keeps calling `needsRework` exactly as it does now." |
| §8 (line 268) | the cap is stated only as `SpecRounds >= maxAgentAttempts` in `decideBrainstorm` |
| §11 (line 324) | the `internal/decide` test row asserts "plan gate unchanged" |
| A4 (line 341) | "The plan gate's uncapped rounds are tolerable (D3). Untouched. If it becomes the next bottleneck, D5's cap is the cheapest thing to extend to it — it needs no semantic change." |
| §13 (line 350) | out of scope: "Any change to the plan gate…" |

**How they are amended.** The document is the record of a decision that was taken, and A4
already names this change as the expected next step — so it is *superseded in place*, not
rewritten. Each site keeps its original text and gains a short marked amendment naming #69
and pointing at base design §7.3, plus one `> Superseded in part` note under the title so a
reader meets the amendment before D3. D3's spec-only scoping stands for everything except
the round cap; §3's verdict rule (revise-closes-on-edit, the scoped confirming pass) stays
spec-only and is not touched. A4's answer is no longer "untouched" but "extended by #69,
which needed no semantic change — as predicted".

The alternative — deleting the superseded sentences — would destroy the reasoning that
makes the cap's shape legible, and would leave A4's prediction unresolved.

## 7. Testing

| test | pins |
|---|---|
| `TestPlanReviewRoundsAreCapped` (`internal/decide`) | `PlanRounds = 2` → `ActExec`; `= 3` → `ask gate_review_capped` with `gate: "plan"`, `attempts: 3`, three choices |
| `TestPendingPlanReworkVerdictOutranksTheRoundCap` (`internal/decide`) | `PlanGate.Verdict = "rework"` **and** `PlanRounds = 3` → `gate_review`, not the capped gate. The cap test alone cannot tell the two checks apart, since it never sets a verdict |
| `cmd_answer` tests (`internal/cli`) | all three answers on a pending capped **plan** gate, since only *retry* has a spec-gate precedent to lean on (`cmd_answer_test.go:84`): *retry* appends `gate_rounds_reset{gate: "plan"}`; *accept* records `gate_overridden` for the **plan** gate at the **plan** hash with the reason, and carries the plan findings forward; *stop* leaves the gate open and clears nothing |
| `cmd_answer` negative test (`internal/cli`) | answering the capped plan gate writes nothing to the **spec** gate — the two receipts and the two round counts stay independent |
| existing spec-gate cap tests | must pass untouched: this adds a branch, it does not move one |

## 8. Out of scope

- **#28** — plan-gate *revise* has no defined mechanism, so the session hand-edits the
  planner's artifacts against the skill's own invariant. The cap bounds how often that
  happens; it does not decide the rule. Left as its own issue, by the user's decision.
- Making the cap configurable, or giving the plan gate a different number from the spec
  gate's.
- Any change to the spec gate's fixed-point behaviour.
- Giving the plan gate an `acceptRevision` equivalent: `revise` closing a gate on the edit
  alone is spec-only by design, and stays so.

## 9. Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| Does this run also take on #28, which #69 calls "worth pairing"? | No — the cap only | #28 is a rule to decide (carve the invariant, or re-dispatch the planner with findings), not a bound to add; pairing them would put a new dispatch path, brief template and events in a two-line change | user-confirmed |
| What is the plan gate's cap? | `maxAgentAttempts` (3), the constant the spec gate and every agent-retry cap already share | #69 asks for "3, matching spec"; a second constant or a config knob would be a knob nobody has asked to turn | assumed |
| Does a pending rework verdict outrank the cap? | Yes, same as the spec gate | While a verdict is unanswered there is a *revise* to offer; showing *accept / retry / stop* instead would strand it. Pinned by its own test | assumed |
| Does *retry* reset both gates' counts? | Only the answered gate's | `gate_rounds_reset` carries `{gate}` and `gate.Rounds` filters on it; nothing shared exists to reset | assumed |
| Does the capped question need plan-specific wording? | No | `questionGateReviewCapped` already renders the gate name and `reviews/<gate>.md` from context | assumed |
| Does the cap fire when plan review is disabled? | No | The whole branch sits inside `st.Config.Review.Plan`; a run with plan review off never reaches it | assumed |
| How is the fixed-point design's D3 ("spec gate only — including its uncapped rounds") handled? | Superseded in place: original text kept, each of the seven sites marked with an amendment naming #69 | It is the record of a decision that was taken, and its own A4 predicted this extension. Deleting the sentences would destroy the reasoning that makes the cap legible | assumed |
| Does D3's spec-only scoping fall entirely? | No — only its round-cap half | §3's verdict rule (revise closing on the edit alone, the scoped confirming pass) stays spec-only, exactly as §8 of this spec says | assumed |
