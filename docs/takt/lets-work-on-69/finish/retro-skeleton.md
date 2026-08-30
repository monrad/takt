# Retro — lets-work-on-69

## What shipped

<!-- prose: what shipped — two or three sentences -->

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

<!-- prose: what went well / what was hard — the session's own account of driving this run -->

## Not proven

- task 3 — waived: The tests largely cover the requested plan-cap behavior, but the change set violates the declared file scope and the fixture does not verify the required planner dispatch.

<!-- prose: not proven — what else must a reader not assume is true -->

## Lessons

<!-- prose: lessons — for the next run in this repository -->

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
