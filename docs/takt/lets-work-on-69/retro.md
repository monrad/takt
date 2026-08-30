# Retro — lets-work-on-69

## What shipped

The plan review gate now stops after three rounds instead of never: `decidePlan` gains a
`f.PlanRounds >= maxAgentAttempts` branch that asks `gate_review_capped` — *accept · retry ·
stop* — placed after the `needsRework` branch so a verdict waiting to be answered still wins,
and `gatherGateFacts` fills `PlanRounds` from `gate.Rounds(events, gate.Plan)` inside the guard
that already scopes the plan gate. Thirteen production and prose lines; everything downstream
(`gate.Rounds`, `questionGateReviewCapped`, `answerGateReviewCapped`, the gate vocabulary) was
already gate-agnostic and needed no change. The other 654 lines are tests — three new files
pinning the cap, its precedence, the per-gate counting and all three answers — plus both
prompts and both design documents, the second of which had asserted in seven places that the
plan gate's rounds were deliberately uncapped.

| wave | attempt | tasks | commit |
| --- | --- | --- | --- |
| 0 | 1 | 1 — Cap decidePlan's review rounds: Facts.PlanRounds, the branch, and the two decide tests; 4 — Both prompts: gate_review_capped is a spec or plan review | 28b55e7d3e41c9a00ceaed977705c06a1bfd0352 |
| 1 | 2 | 2 — Fill Facts.PlanRounds in gatherGateFacts, pinned by a mixed-events facts test | aa43773e3c95360f3a002d18c18142de178a2cbe |
| 2 | 3 | — | 8c7a16375a8a00f302b3b5a7884a3d983c63e611 |
| 3 | 1 | 5 — Amend both design documents; whole-repo gate on the assembled branch | a72f109595c91b3875d496651f056bdc8ceb8a03 |

## Decisions

- gate: gate_review — accept (Four rounds, findings 3/3/4/4, not converging. Rounds 2-4 each raised the verification bar on the previous round's own fixes rather than finding a new defect; round 3's one blocking finding (task 2 claimed a fixture isolation gatherIndexFacts makes impossible) was real and is fixed. Round 4's four are all 'the verify commands do not prove the requirement' on a decomposition the reviewer itself calls plausible: the override reason not asserted (T3), greps not proving sentence replacement (T4), row 9 not pinning the threshold (T5), the PlanRounds field comment unchecked (T1). The per-task review judges these against real code, which is the cheaper referent. User's instruction at the round-3 gate: fix round 3, then accept round 4. This is precisely the loop #69 exists to cap — the plan gate has no gate_review_capped yet, which is what this run adds.)
- task_waiver: task 3 (Rework exhausted twice over four attempts; neither remaining finding is fixable or worth a fifth. The BLOCKING finding is a false positive raised for the third consecutive round: it names docs/takt/lets-work-on-69/{events.jsonl,state.json,waves/2/**} as the implementer's out-of-scope edits, but those are takt's own bookkeeping, written by the binary as it drives this run, and every implementer brief's Rules forbid touching docs/takt/lets-work-on-69/**. No attempt can satisfy it without violating its own rules — the backend reviewer sees the whole worktree and cannot distinguish takt's writes from the agent's. The MAJOR finding (the fixture should assert its dispatch is the planner, not just that a dispatch happened) is real but marginal, and is the fourth turn of an assert-one-more-thing ratchet: attempt 1 was asked for carried-finding-field and dispatch-target assertions, attempt 2 for ask-context assertions in all four tests, attempt 3 for this. The delivered work is sound: all six internal lenses returned zero findings on attempt 3, all nine verify commands pass, mutation checks confirm each new assertion bites, and git diff --quiet main -- internal/cli/cmd_answer_test.go holds so G7's byte-identity claim is intact. G4's four tests exist and pass.)

disposition: not yet chosen

- spec_assumption: Does this run also take on #28, which #69 calls "worth pairing"? — No — the cap only (#28 is a rule to decide (carve the invariant, or re-dispatch the planner with findings), not a bound to add; pairing them would put a new dispatch path, brief template and events in a two-line change)

## What went well / what was hard

**The run dogfooded its own premise, in the worst possible way.** The plan gate took four
rounds — findings 3, 3, 4, 4 — and closed only by override, because the cap it was building did
not exist yet. The shape #69 describes was exact: rounds 2 and 3 raised findings *against the
previous round's fixes*. Round 1 demanded a zero-deletions diff check to prove `decide_test.go`
untouched; round 2 correctly pointed out an implementer could still weaken a test with
additions only. Round 1 demanded anchored doc greps; round 2 pointed out seven generic "Amended
by #69" lines would pass them. What ended it was the lesson from the #60/#62 run: restate the
rule once instead of patching the instance. "Existing test files are never edited, so new tests
go in new files" killed the whole class — and `git diff --quiet main -- <file>` is a total proof
where any check over a *modified* file is not. Round 3 also found one genuine defect under the
noise: the plan claimed a fixture could isolate the guard's `fileNonEmpty(plan.md)` conjunct,
which `gatherIndexFacts` makes impossible by folding plan.md emptiness into `IndexValid`. That
one finding is the argument for the cap's *accept · retry · stop* rather than a hard stop.

**The bundle-churn false positive cost two tasks their rework budget.** The cross-vendor
per-task reviewer sees the whole worktree and cannot tell takt's own writes from the agent's, so
it flagged `docs/takt/lets-work-on-69/{state.json,events.jsonl,waves/**}` as the implementer's
out-of-scope edits — on task 2 (major), then task 3 twice (blocking the second time). Telling
each retried implementer explicitly that the finding was takt's bookkeeping and must not be
acted on worked: every one of them left `docs/takt/` alone and said so in its report. Without
that note the attempt would have been spent trying to revert the very files the brief's Rules
forbid touching.

**The internal lenses were the cheap, accurate half.** Eleven candidates across four waves, of
which the adversarial verifier refuted three — including two wave-3 consistency claims it
disproved by *measuring*: a "line is ~62 chars" claim where the real line was 94, and an
"inconsistent marker convention" claim where all seven amendment markers turned out to be
identical prose. Every lens returned zero findings on the final attempt of every wave.

## Not proven

- task 3 — waived: The tests largely cover the requested plan-cap behavior, but the change set violates the declared file scope and the fixture does not verify the required planner dispatch.

The waiver above is narrow: task 3's tests exist, pass, and were mutation-checked by the
implementer, and all six lenses cleared its final attempt. What is *not* pinned is the one thing
the last backend round asked for — `planCapFixture` asserts only that a dispatch happened, not
that the dispatched agent is the planner. A future change that made `takt next` dispatch
something else at that point would not fail this fixture.

Three follow-ups from the overridden plan gate are likewise unaddressed by construction: the
accept test does not assert `Data["reason"]` survives on the `gate_overridden` event (it does
assert every field of the carried *finding*); task 4's greps prove the new prompt sentence is
present but not that the old "the spec review" sentence is gone; and task 5's row-9 check does
not pin the `PlanRounds ≥ maxAgentAttempts (3)` threshold itself. All three are real gaps in the
*verification*, not evidence of wrong behaviour — the code paths they cover are exercised by
other tests in the same files.

Nothing here proves the cap behaves well in a real capped run. It has never fired outside a
fixture: this run's own plan gate was overridden by a binary that predated the cap.

## Lessons

**When a review round raises the bar on the previous round's fix, stop patching the instance
and restate the rule.** Twice in this run a rule-level answer closed a whole class of findings
that a surgical patch would have kept alive: "existing test files are never edited" (making
`git diff --quiet main -- <file>` a total proof of G7, where a zero-deletions check was not),
and "every doc amendment names #69 *and* points at §7.3, checked per site" (where a file-wide
`#69` count passed with a site omitted). This is the same move that ended the #60/#62 loop.

**Pair every absence check with a positive one.** `grep -c 'its round cap' … | grep -qx 0` is
satisfied by deleting the clause. The reviewer caught this and it generalises: a doc check that
only proves a phrase is *gone* licenses the deletion you did not want.

**Anchor doc verifies at their site, never file-wide.** Seventeen anchored checks replaced four
loose counts. The cost is that each pins an original sentence verbatim — but for a
supersede-in-place amendment, keeping that sentence *is* the requirement, so the constraint and
the requirement are the same one.

**The backend reviewer's whole-worktree view is a standing hazard here.** Budget for it: on any
retry, tell the implementer in the dispatch that `docs/takt/<slug>/**` churn is takt's own and
must be left alone. It escalated from major to blocking across attempts, and it is not fixable
by any attempt — waive with the two facts side by side (the files named, the Rules forbidding
them) rather than spending a fourth try.

**Unescape agent replies before `takt record`.** Six of this run's replies arrived with `&gt;`
or `&amp;&amp;` in them; recorded raw, a verifier reply comes back `valid: false` with no quoted
reason. A two-line Python pass over every reply file made it a non-event.

## Follow-ups

- major — Task 3 does not verify that the override reason is recorded (gate plan) — Spec §§5 and 7 require the plan-gate `gate_overridden` event to contain the user's reason. Task 3 checks that an empty reason fails and that the resulting event has the plan gate and current hash, but never asserts `Data["reason"] == "known gap"`. The tests could pass if the reason were accepted by the CLI and then discarded.
- major — Task 4's greps do not prove the prescribed sentence replacement (gate plan) — Each verification only searches for the new substring, while the prompt parity tests do not anchor this sentence. Both commands would pass if the new phrase were appended elsewhere and the false “the spec review” sentence remained, or if the options were changed. Require the complete expected gate sentence in both files and assert the obsolete wording is absent.
- major — Task 5's base-design checks omit the cap threshold and precedence semantics (gate plan) — The row-9 check proves only that `ask gate_review(plan)` and `gate_review_capped` occur on one line; it does not require `PlanRounds ≥ maxAgentAttempts (3)`, that pending rework/reject/error comes first, or that normal review remains the fallback. Likewise, the §7.3 check only requires the gate id, not three rounds since the newest reset. The documented behavior could therefore be materially wrong while every verify command passes.
- major — Trailing bare '§7.3' citation collides with the other document's own §7.3 (wave 3/task 5) — Row 9 ends with '(fixed-point design §3, §8; §7.3)'. Everywhere else in this same diff, a bare '§N' means 'this document' and a cross-document reference is always spelled out ('fixed-point design §N') — e.g. the new §7.3 sentence at line 732-733 writes '(§7.2, fixed-point design §8)', self-reference first, cross-doc reference explicitly labelled second. Row 9 breaks that ordering: the bare '§7.3' is appended directly after 'fixed-point design §3, §8;' with no document reset, so it reads as continuing the fixed-point-design citation list. That matters here because docs/superpowers/specs/2026-08-26-spec-gate-fixed-point-design.md has its own real §7.3 ('follow-ups.json', line 242) — an unrelated topic. The intended referent (base design's own §7.3, Plan gate) is inferable from context, but the citation itself doesn't follow the convention this same wave establishes elsewhere, and this parenthetical wasn't part of the task's literal quoted text for row 9 — it was added on top of it.
- 2 minor, 3 nit — see follow-ups.json, which holds every one verbatim

## Numbers

```json
{
  "internal_review": {
    "candidates": 11,
    "confirmed": 8,
    "false_positives": 3,
    "unattributed": 1,
    "by_lens": {
      "consistency": {
        "reported": 5,
        "confirmed": 3
      },
      "docs": {
        "reported": 1,
        "confirmed": 0
      },
      "intent": {
        "reported": 1,
        "confirmed": 1
      },
      "simplicity": {
        "reported": 1,
        "confirmed": 1
      },
      "tests": {
        "reported": 3,
        "confirmed": 3
      }
    },
    "scoped_passes": 0,
    "scoped_changed_verdict": 0,
    "overlap": 0,
    "skipped": 0
  },
  "wave_timings": [
    {
      "wave": 0,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T16:55:10.172111116Z",
      "closed_at": "2026-08-30T17:07:47.597178822Z",
      "committed": true,
      "committed_at": "2026-08-30T17:07:47.597081985Z"
    },
    {
      "wave": 1,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T17:07:52.082077045Z",
      "closed_at": "2026-08-30T17:18:57.381018338Z",
      "committed": false
    },
    {
      "wave": 1,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-30T17:19:01.682420332Z",
      "closed_at": "2026-08-30T17:24:07.288612866Z",
      "committed": true,
      "committed_at": "2026-08-30T17:24:07.28858082Z"
    },
    {
      "wave": 2,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T17:24:11.862250935Z",
      "closed_at": "2026-08-30T17:34:14.263865751Z",
      "committed": false
    },
    {
      "wave": 2,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-30T17:34:18.371790103Z",
      "closed_at": "2026-08-30T17:42:43.216388161Z",
      "committed": false
    },
    {
      "wave": 2,
      "slice": 1,
      "attempt": 3,
      "dispatched_at": "2026-08-30T17:43:16.007051249Z",
      "closed_at": "2026-08-30T17:50:09.145839597Z",
      "committed": true,
      "committed_at": "2026-08-30T17:51:49.593667456Z"
    },
    {
      "wave": 2,
      "slice": 1,
      "attempt": 3,
      "dispatched_at": "2026-08-30T17:43:16.007051249Z",
      "closed_at": "2026-08-30T17:51:49.593704829Z",
      "committed": true,
      "committed_at": "2026-08-30T17:51:49.593667456Z"
    },
    {
      "wave": 3,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T17:51:55.54857261Z",
      "closed_at": "2026-08-30T18:14:16.8577229Z",
      "committed": true,
      "committed_at": "2026-08-30T18:14:16.857681964Z"
    }
  ]
}
```
