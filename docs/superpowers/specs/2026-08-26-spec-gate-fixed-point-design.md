# The spec gate's fixed point — design

**Status:** draft for review · **Date:** 2026-08-26 · **Repo:** `github.com/monrad/takt` ·
**Issues:** closes [#40](https://github.com/monrad/takt/issues/40), closes
[#29](https://github.com/monrad/takt/issues/29) · **Amends:** `2026-08-24-takt-design.md` §4.2, §4.4,
§5.2, §5.3, §7.2, §9

Two dogfood runs spent most of their wall clock in the spec gate: six passes and 2h26m of planning
before nine minutes of execution on the first, five passes and no code at all on the second. This
design gives that loop a fixed point without discarding the reviewer, and carries an approving pass's
leftover findings forward instead of freezing them.

---

## 1. Problem

`takt review plan` judges the plan **against the spec**: coverage is finite and checkable, so the
question terminates. `takt review spec` points a critic at a prose design doc with no referent, and
"is this unambiguous and testable?" can always be answered no.

Three mechanisms compound it in the current code:

| # | mechanism | where |
|---|---|---|
| P1 | **Any edit re-arms the gate.** `revise` is `return false, nil`; the session edits, the hash moves, and the whole document is re-judged — finding new defects in the new text. | `internal/cli/cmd_answer.go:107`, `internal/gate/gate.go` `Compute` |
| P2 | **No severity gate.** `needsRework` keys on the bare verdict, so two nits and a blocking finding are the same event. The reviewer already emits `blocking\|major\|minor\|nit` and `writeFindings` renders it to prose and drops the structure. | `internal/decide/decide.go:242`, `internal/cli/cmd_review.go` `writeFindings` |
| P3 | **No cap.** `maxAgentAttempts` caps agent retries and `1 + MaxRework` caps task rework; gate review rounds are uncapped. The one loop that cannot self-limit is the only one without a limit. | `internal/decide/decide.go:138` |

Measured on the second run, roughly two thirds of the findings after pass 1 were defects the *previous
revision* introduced. **Pass 1 carried the signal; passes 2..n were mostly noise the loop generated
about itself.** That is the evidence this design is built on.

A fourth, separate defect (#29): an `approve` verdict may carry minor findings, and acting on them
edits `spec.md`, which re-arms the gate under P1. On the first run both minors were real and both were
dropped — one wording ambiguity and one genuine test-coverage gap.

### Why not simply drop the spec gate

Masterplan, which takt's design came from, never reviews specs. Rejected for three reasons:

1. **The catches were cheap-early, expensive-late.** The two findings the spec reviewer earned its keep
   with — a `--title "<title>"` interpolation that would break or execute on a heading containing a
   quote or `$(…)`, and a claim that `executeRun` sets `ActiveWave` when it does not — are *factual
   errors about this codebase*, not prose-quality findings. The plan reviewer would not catch them: its
   rubric is coverage and consistency **against the spec**, so a plan faithfully restating a wrong spec
   scores well. They would surface at task-review or verify time, after `spec.md` is frozen into the
   plan gate's hash.
2. **It forecloses #39.** That issue's value is seeding `crit` with the model reviewer's findings so a
   human disposes of them inline. No model pass, no seed.
3. **It is the harder reversal.** If one pass still does not pay for itself, deleting it later is a
   small change to `decideBrainstorm`.

---

## 2. Decision record

`user` = confirmed in the 2026-08-26 brainstorm; `assumed` = chosen here, open to revision.

| id | decision | source |
|---|---|---|
| D1 | One review pass is the default; a `rework` verdict with no blocking finding closes on `revise` without a second backend call. | user |
| D2 | A `blocking` finding earns exactly one **scoped** confirming pass, judged against the prior finding list rather than the whole document. | user |
| D3 | Applies to the **spec gate only**. The plan gate keeps today's behaviour entirely, including its uncapped rounds. | user |
| D4 | `reject` keeps today's loop — it asks, `revise` re-arms, the whole document is re-judged. | assumed: "wrong approach" is the one verdict where re-judging everything is correct, and it is rare. D5 bounds it. |
| D5 | Review rounds at the spec gate are capped at `maxAgentAttempts` (3); the run then asks instead of spending a fourth call. | user (#40 option 4) |
| D6 | Findings are carried forward when the gate closed without anyone being asked to act on them, or when the user explicitly declined. | user (#29 folded in) |
| D7 | No new configuration. `Review.Spec` already toggles the gate; the cap reuses `maxAgentAttempts`. | assumed: YAGNI — neither knob has a demonstrated second setting. |
| D8 | Severities are defined in the rubric text, not left to the reviewer's judgement. | assumed: once `blocking` is the only severity that costs a round, an undefined `blocking` inflates and D2 degrades into "always re-review". |

---

## 3. The verdict rule

For the **spec gate only**, with a receipt at the current hash:

| verdict | blocking findings | behaviour |
|---|---|---|
| `approve` | any | satisfied. Findings, if any, are carried forward (§6). |
| `rework` | 0 | ask `gate_review`. **`revise` closes the gate** (§4) — no second backend call. |
| `rework` | ≥ 1 | ask `gate_review`. `revise` re-arms; the next pass is scoped (§5). |
| `reject` | any | ask `gate_review`; `revise` re-arms; the next pass is a full re-review. |
| `error` | — | ask `gate_review`, on its error row: nothing was reviewed, so the choices are `retry` (re-run the reviewer; writes nothing), `accept` and `stop`. `revise` is not offered. |

`skipped` and `gate_overridden` keep today's meaning. The plan gate is untouched: `decidePlan` keeps
calling `needsRework` exactly as it does now.

"Blocking findings" is read from `Receipt.Severities["blocking"] > 0` (§7.1), so neither `gate.Compute`
nor `Decide` parses a findings file to reach a gate decision.

---

## 4. How `revise` closes the gate

At answer time the session has not edited yet, so there is no new hash to bind an override to. The
`revise` answer therefore records intent, and the *edit* completes it.

`answerGateReview`, when the gate is `spec` **and** the receipt at the current hash has verdict
`rework` with zero blocking findings, appends:

```json
{"type":"gate_revision_accepted","data":{"gate":"spec","hash":"sha256:<H1>"}}
```

`gate.Compute` gains one rule, evaluated **after** the receipt is read and only when no receipt answers
at the current hash:

> A `gate_revision_accepted` event for this gate satisfies it **when the current hash differs from the
> event's hash, and no `gate_reviewed` event for the gate has followed it**. The newest revision event
> for the gate wins, but a `gate_reviewed` event for the gate clears whatever revision was pending
> before it — a later review has now answered the revision, so it must not go on satisfying the gate
> once that review has spoken. Status verdict is `revised`.

The precedence matters. A receipt at the current hash is a fresher and more specific answer than an
event bound to an older hash, so it governs. In the ordinary flow there is no receipt at the new hash
and the event decides; but if the user runs `takt review spec --force` after revising and the reviewer
comes back `reject`, that verdict wins rather than being masked by a stale revision event. This keeps
`--force` meaningful as an escape hatch.

### Why "differs from"

Every other satisfier in §9 of the base design binds to the *current* hash. This one binds to "not that
hash", and that inversion is load-bearing rather than an inconsistency:

- **It is self-enforcing.** Answer `revise` and edit nothing, and the hash is still H1, so the gate
  stays open and asks again. The gate cannot be closed by assertion, only by an edit.
- **It needs no new command, no new state field, and no second backend call.** The event is the whole
  mechanism.

What it verifies is that *an edit happened*, not that the findings were applied. That is the trade D1
accepts, and it applies only where the reviewer itself said nothing blocking.

### Edge cases

| case | behaviour |
|---|---|
| `revise` answered, nothing edited | Gate stays open; `Decide` re-asks `gate_review` from the unchanged receipt. |
| Spec edited back to H1 after a revision event at H1 | Gate re-opens. Correct: the reviewed text is once again the text the reviewer objected to. |
| Several revision events for `spec` | Newest wins; older ones are inert history. |
| Revision event present, artifact later edited again, no review since | Still satisfied — an edit does not consume the event, only a later review does. |
| A `gate_reviewed` event for the gate follows the revision event (e.g. `takt review spec --force`) | Revision cleared; a later edit no longer resurrects it on its own — the new receipt, if it answers at the current hash, governs, otherwise the gate is open until revised again. |
| `revise` on the plan gate, or on a blocking/`reject` spec receipt | No event written; today's re-arm behaviour, unchanged. |
| `revise` on an `error` spec receipt | Not reachable: the question's error row offers `retry` instead (§3). Were it answered anyway, no event is written and the gate re-arms as before. |

---

## 5. The scoped confirming pass

When a blocking `rework` re-arms at H2, `runReview` reads `reviews/spec.json` (§7.2) — not the
receipt — for both the verdict and the blocking count: if that file's verdict is `rework` with
`SeverityCounts()["blocking"] > 0`, it renders `review-spec-followup.md` instead of `review-spec.md`,
quoting the findings it holds.

The receipt is not the source here because it records the gate's state *including* its failures — a
backend outage between a blocking rework and its confirming pass comes back as a result with
`Verdict == error`, and that error legitimately replaces the receipt's `rework`. An `error` verdict is
not an answer — the same principle `cachedReceipt` applies when deciding whether a receipt can
short-circuit a re-run — so `storeFindings` leaves `reviews/spec.json` untouched on one (§7.2). Reading
the findings file instead of the receipt keeps a transient outage from silently widening the scoped
pass back to the full rubric.

The scoped rubric asks one question: **are these N findings addressed in the new text? Do not raise new
findings.** That question has the property the current one lacks — a finite, checkable referent — so it
terminates for the same reason the plan gate does.

Its verdicts carry the ordinary meanings: `approve` (all addressed; any leftover non-blocking findings
are carried forward under §6), `rework` (some not addressed — the still-unaddressed subset scopes the
next pass), `reject` (the revision made things worse; falls back to the full rubric on the next pass).

"Do not raise new findings" is a prompt-level constraint a model may ignore. D5's cap is what makes
that survivable rather than fatal.

---

## 6. Carrying findings forward (#29)

**The rule: carry what nobody was asked to act on, or what the user explicitly declined to act on.**

| how the gate closed | carried? | why |
|---|---|---|
| `approve` with findings | yes | The gate closed; nobody was asked for anything. #29's exact case. |
| `accept` (override) | yes | The user explicitly declined to act. |
| `rework` closed on `revise` | no | The findings *were* the instruction. |
| Scoped pass returns `approve` with leftovers | yes | Via the `approve` row. |

Findings that were the instruction for a `revise` are never carried, because the session was asked to act on them.

Two call sites, and nothing else needs to know: `runReview` when the verdict is `approve` and findings
exist, and `answerGateReview` on `accept`.

Carried findings append to `follow-ups.json` in the bundle root (§7.3). `finish.RetroInputs` gains
`FollowUps []FollowUp`; `BuildRetroInputs` stays pure, taking them as a parameter the way it already
takes events. `run-retro.md` already has a `## Follow-ups` heading and gains one instruction to list
carried findings there with their severity.

The result: a minor on an approving pass reaches the retro with its severity intact instead of existing
only in `reviews/spec.md`.

---

## 7. Data

### 7.1 `Receipt` gains severity counts

```go
type Receipt struct {
    // … unchanged fields …
    Severities map[string]int `json:"severities"` // "blocking"|"major"|"minor"|"nit" → count
}
```

Counts only, not the findings themselves, so the gate decision never reads a second file. A receipt
written before this change decodes with `Severities == nil`; `nil["blocking"]` is `0`, so an old
receipt behaves as "no blocking findings" — the D1 path. That is the safe default: it closes on
`revise` rather than looping.

### 7.2 `reviews/<gate>.json`

The structured `backend.ReviewResult`, written alongside the existing human-readable
`reviews/<gate>.md`. Today `writeFindings` renders severities into prose and the structure is lost, so
nothing downstream can read them. This file is what the scoped pass (§5) and the carry-forward (§6)
consume. `Receipt.Findings` continues to point at the `.md`; the `.json` sits beside it under the same
name.

It is written for **both** gates, because `runReview` is shared and the cost is one file; only the spec
gate reads it. Each review overwrites it — except one with an `error` verdict, which writes neither the
`.json` nor the `.md`: a backend failure is not a reviewer's answer, and letting it erase the last real
pass's findings would drop the run back into the unscoped re-review loop this design exists to end. So
it always describes the newest pass a reviewer actually answered, which is what §5 needs when a scoped
pass reports a still-unaddressed subset and that subset must scope the pass after it.

### 7.3 `follow-ups.json`

Bundle root, append-only, written through `bundle.WriteJSONAtomic`:

```json
{"items":[
  {"gate":"spec","severity":"minor","file":"spec.md","line":42,
   "title":"…","detail":"…","source":"approve","ts":"2026-08-26T10:04:11Z"}
]}
```

`source` is `approve` or `override`. Absent file means no follow-ups. Paths are bundle-relative, per
§4.5 of the base design.

### 7.4 `Facts` and `GateStatus`

```go
type GateStatus struct {
    Satisfied bool
    Verdict   string // "", approve, rework, reject, error, skipped, overridden, revised
    Blocking  bool   // Severities["blocking"] > 0 on the receipt at the current hash
}
```

`Facts` gains `SpecRounds int`: the count of `gate_reviewed` events for the gate in `events.jsonl`
**since the newest `gate_rounds_reset` event for it** (§8), mirroring how `AlignmentAttempts` counts
since `alignment_attempts_reset`. Both event streams are already recorded, so no new bookkeeping. A
`--force` re-run writes another `gate_reviewed` event and therefore counts as another round, which is
correct.

Two new event types are introduced by this design — `gate_revision_accepted` (§4) and
`gate_rounds_reset` (§8) — and both belong in §4.4 of the base design.

---

## 8. The cap and its gate

At `SpecRounds >= maxAgentAttempts` (3) with the spec gate still unsatisfied, `decideBrainstorm` asks
the new `gate_review_capped` gate instead of emitting a fourth `exec` op. This is the same shape as
`plan_invalid` and `agent_invalid`.

| choice | effect |
|---|---|
| `accept` (recommended) | Requires `--reason`; records `gate_overridden` at the current hash, and carries the findings forward under §6. |
| `retry` | One more review pass. Appends a `gate_rounds_reset` event; `SpecRounds` counts `gate_reviewed` events **since the newest reset**, mirroring how `AlignmentAttempts` counts since `alignment_attempts_reset`. |
| `stop` | Ends the turn with the gate open. |

The gate id is added to `decide.Question`'s switch, to `Vocab().Gates`, and to the gate list in
`commands/takt.md:39`; `hosts/` is then regenerated with `hostgen`. The existing prompt-parity test
covers it once those three are in step.

---

## 9. Rubric changes

`review-spec.md` currently states only `Severities: blocking, major, minor, nit`. Under D2 `blocking`
is the sole severity that costs a round, so it gains definitions:

- **blocking** — the design as written will not work, or will produce incorrect behaviour: a factual
  error about this codebase, a self-contradiction, or a missing decision that blocks planning.
- **major** — a real gap, but a competent implementer would still get it right.
- **minor** / **nit** — wording, precision, polish.

New template `review-spec-followup.md` for §5. `brief.ReviewData` gains `PriorFindings
[]backend.Finding` to render it.

---

## 10. Files touched

| area | change |
|---|---|
| `internal/gate/gate.go` | `Receipt.Severities`; `Compute` honours `gate_revision_accepted`; `Rounds(events, gate)`; follow-up read/append |
| `internal/decide/decide.go` | spec branch of `decideBrainstorm`; `GateStatus.Blocking`; `Facts.SpecRounds` |
| `internal/decide/questions.go`, `vocabulary.go` | `gate_review_capped` |
| `internal/cli/cmd_review.go` | write `reviews/<gate>.json`; severities on the receipt; pick the scoped template; carry on `approve` |
| `internal/cli/cmd_answer.go` | `revise` writes the revision event (spec, non-blocking only); `accept` carries findings; `gate_review_capped` choices |
| `internal/cli/facts.go` | build `SpecRounds` and `GateStatus.Blocking` |
| `internal/brief/brief.go`, `templates/` | severity definitions; `review-spec-followup.md`; `ReviewData.PriorFindings`; `run-retro.md` follow-ups line |
| `internal/finish/retro.go` | `RetroInputs.FollowUps` |
| `commands/takt.md`, `hosts/copilot/skills/takt/SKILL.md` | gate id list in both — each is hand-maintained and each has its own parity test (`internal/prompt/prompt_test.go`, `internal/prompt/copilot_test.go`). `hostgen` renders only `agents/*.md` and is not involved. |
| `docs/superpowers/specs/2026-08-24-takt-design.md` | §4.2, §4.4, §5.2, §5.3, §7.2, §9 |

Follow-up read/append lives in `internal/gate` because gate closure is the only thing that produces
follow-ups; it moves to its own package if task reviews ever produce them too.

---

## 11. Testing

| package | cases |
|---|---|
| `internal/gate` | Revision event satisfies at a different hash; does **not** at the same hash; newest event wins; older receipts with `Severities == nil` read as zero blocking. |
| `internal/decide` | Table across `{approve, rework+blocking, rework−blocking, reject, error}` × `{rounds < cap, rounds ≥ cap}`; plan gate unchanged; `gate_rounds_reset` restarts the count. |
| `internal/cli` | `revise` writes the event for spec and not for plan, and not on a blocking receipt; `reviews/spec.json` round-trips; the scoped template is chosen only after a blocking receipt; carry-forward fires on `approve` and `accept`, not on `revise`. |
| `internal/finish` | Follow-ups reach `RetroInputs`. |
| prompt parity | Covers `gate_review_capped` once `Vocab` and `commands/takt.md` agree. |

The end-to-end shape worth asserting: a run whose reviewer returns `rework` with only minor findings
makes **exactly one** backend review call at the spec gate and then reaches `PhasePlan`.

---

## 12. Assumptions & open decisions

| # | assumption | default until revisited |
|---|---|---|
| A1 | One `blocking`-triggered scoped pass is enough; a scoped pass that keeps reporting "not addressed" is rare. | D5's cap catches it; the `gate_review_capped` question tells the user how many rounds were spent. |
| A2 | Reviewers respect "do not raise new findings" in the scoped rubric well enough to be useful. | If they do not, the pass still terminates via the cap; the fallback is to drop §5 and let every `rework` close on `revise` (pure D1). |
| A3 | Defining severities (D8) is sufficient to prevent `blocking` inflation. | If `blocking` rates stay high across runs, tighten by requiring a blocking finding to cite a file and line. |
| A4 | The plan gate's uncapped rounds are tolerable (D3). | Untouched. If it becomes the next bottleneck, D5's cap is the cheapest thing to extend to it — it needs no semantic change. |
| A5 | `follow-ups.json` in the bundle root is the right home, rather than an events-only record. | A file, because the retro reads it as data and events are an append-only log the retro does not otherwise mine for findings. |
| A6 | Carried findings need no lifecycle (no "done" marking). | They are retro input, not a tracker. If they need one, it belongs with #39's crit integration. |

## 13. Out of scope

- #39 (human review through `crit`). This design deliberately preserves the seam it needs:
  `reviews/spec.json` is exactly the structured findings list `crit comment --json` would be seeded
  from.
- Any change to the plan gate, task reviews, or the goals loop.
- The `error` verdict's handling, which remains today's ask.
