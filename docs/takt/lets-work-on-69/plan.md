# Plan — lets-work-on-69: cap the plan review gate's rounds (#69)

## Approach

The change is deliberately tiny — one fact, one branch — and everything downstream of the
ask is already gate-agnostic (`gate.Rounds` filters per gate, `questionGateReviewCapped`
renders the gate name from context, `answerGateReviewCapped` resolves the gate from the
pending payload). So the plan is: land the decide-side cap with its two unit tests first,
then the one-line facts fill with the mixed-events facts test, then the end-to-end
`cmd_answer` tests that drive a real run into the capped plan state and exercise all three
answers, and in parallel the two-prompt sentence edit. The two design documents are amended
last, on top of the finished behaviour, and that task carries `task check` so the assembled
branch is proven green (G8).

Five tasks, no file shared between any two that could run together. Waves are left to takt:
tasks 1 and 4 have no dependencies; 2 needs 1 (the `Facts.PlanRounds` field); 3 needs 1 and
2 (its fixture drives the real `takt next`, which only asks the capped gate once both the
branch and the fill exist); 5 needs everything (it documents shipped behaviour and runs the
whole-repo check).

## Tasks

### Task 1 — the cap in `decidePlan`, `Facts.PlanRounds`, and the two decide tests

`internal/decide/decide.go` gains `PlanRounds int` on `Facts` (documented like its sibling
`SpecRounds` at decide.go:222) and the cap branch in `decidePlan`, placed after the
`needsRework(f.PlanGate)` branch and before the `exec` — mirroring `decideBrainstorm`
(decide.go:341) line for line, because the order is load-bearing: a pending
rework/reject/error verdict outranks the cap. `internal/decide/questions.go`'s
`questionGateReviewCapped` doc comment (line 188) currently says "the spec review"; the
rendered text is already gate-agnostic, so only the comment moves to "a spec or plan
review". The two tests G1 and G2 name land in `decide_test.go` beside their spec-gate
precedents (lines 1067 and 1115): the cap test alone cannot tell the two checks apart
(it never sets a verdict), which is exactly why the precedence test exists — its comment
should say so, like the spec one does. Scoped to one package so the whole task is judged
by `go test ./internal/decide/...`. That run proves the spec gate's own tests still pass,
but not that they were not *edited* — a rewritten test passes too — so G7 is pinned
instead by a diff assertion: `git diff main -- internal/decide/decide_test.go` must show
zero removed lines. The task adds test functions and touches no existing one.

### Task 2 — fill `PlanRounds` in `gatherGateFacts`, pinned by a mixed-events facts test

One line inside the existing plan branch of `gatherGateFacts` (facts.go:211), which is
already guarded by `Config.Review.Plan && HasIndex && IndexValid && plan.md` non-empty —
so the cap cannot fire when plan review is disabled, by construction. The test is a new
package-internal file in the `reviewer_facts_test.go` style (package `cli`, driving the
real `gatherFacts` over a real bundle): a plan-phase bundle with a valid index, and an
events log interleaving spec and plan `gate_reviewed` and `gate_rounds_reset` entries so
that the two gates' counts come out different — a fill that counts the other gate's
events, or ignores the per-gate reset, fails. The test must also assert `HasIndex` and
`IndexValid` are true, or a broken fixture would make `PlanRounds == 0` pass vacuously.
That positive test proves the count is right but not that it is *guarded*: its fixture
satisfies every conjunct, so an unconditional assignment outside the branch would pass it
unchanged. A second test therefore runs the same events log through three fixtures that
each break one conjunct — plan review off, no valid index, empty `plan.md` — and asserts
`PlanRounds == 0` while `SpecRounds` is still 1, so the zero is the guard's doing rather
than an empty log. Kept separate from task 1 because it is the CLI half of the seam and
its fixture is real I/O, not table rows.

### Task 3 — `cmd_answer` tests: all three answers on a capped plan gate, and spec-gate independence

A `planCapFixture` mirroring `specCapFixture` (cmd_answer_test.go:37): drive a run through
brainstorm (spec, goals, approving spec review), record a valid plan, take three plan
review rounds with the fake backend returning `rework`, editing `plan.md` before each so
the receipt never answers at the next hash, then one final unreviewed edit — three
`gate_reviewed{plan}` events, current hash unreviewed, no verdict pending. Four tests:
*retry* appends `gate_rounds_reset{gate: "plan"}` and the next `next` execs
`takt review plan` again; *accept* requires `--reason`, records `gate_overridden` for the
plan gate at the plan hash, carries the plan findings to follow-ups, and the run proceeds
to the alignment audit; *stop* keeps the gate open and the same ask comes back; and the
negative test — answering the capped plan gate leaves the spec gate's receipt bytes,
round count and events untouched. New file rather than an edit to `cmd_answer_test.go`,
so G7's "existing spec-gate cap tests pass untouched" is literal: the spec fixtures are
not even touched, and the verify runs both families side by side.

### Task 4 — the two prompts describe the capped gate as a spec or plan review

The identical one-sentence edit in `commands/takt.md` §Gates (line 39) and
`hosts/copilot/skills/takt/SKILL.md` §Gates (line 40): "the spec review" becomes "a spec
or plan review"; the three choices stay as written. The two files are hand-maintained
copies of one contract, so the verify greps for the same new phrase in both, and
`go test ./internal/prompt/...` proves every parity test still passes (the gate id list
checks are unaffected — the id itself does not change).

### Task 5 — both design documents, then the whole-repo gate

The base design (`docs/superpowers/specs/2026-08-24-takt-design.md`) is corrected in five
places: §5.3 row 9 mirrors row 6 with `PlanRounds ≥ maxAgentAttempts (3) → ask
gate_review_capped` after the rework branch; §5.4's gate vocabulary describes the capped
gate as a review gate's (spec or plan); §5.2's round-cap clause stops reading as the spec
gate's alone; §7.2's closing sentence — "it applies to the spec gate only — the plan gate
(§7.3) keeps today's behaviour entirely, including its uncapped rounds" — is rewritten so
the fixed point's other halves (revise-closes-on-edit, the scoped confirming pass) stay
spec-only while the round cap does not; and §7.3's plan-gate paragraph gains its own
sentence: three rounds since the newest reset, then `gate_review_capped`. The fixed-point
design (`2026-08-26-spec-gate-fixed-point-design.md`) is superseded in place, not
rewritten: a `> Superseded in part` note under the title, and a short marked amendment at
each of the seven sites (D3, D5, §3, §8, §11, A4, §13) naming #69 and pointing at base
design §7.3, with every original sentence kept — A4's answer becomes "extended by #69,
which needed no semantic change — as predicted". Each amendment sits within four lines of
the passage it amends (six at §8), and the verify commands exploit that: one per site,
each grepping the original sentence verbatim and requiring `#69` in its immediate context,
so a check fails both when an amendment is missing and when the original was deleted or
reworded. A file-wide `#69` count is deliberately not used — it passes with a site
omitted. This task runs last and carries `task check` (build + `go test ./... -race` +
lint + host parity), which is G8's evidence on the assembled branch.

## Risks

- **The end-to-end fixture (task 3) is the long pole.** Driving a run into the capped plan
  state crosses brainstorm, goals, the spec gate and planner recording. Every step has a
  precedent in `cmd_next_test.go`'s `TestNextWalksBrainstormAndPlan` and
  `cmd_answer_test.go`'s `specCapFixture`, and the task description spells the recipe out,
  but any drift in those helpers' assumptions surfaces here first.
- **Prose amendments are where the #60/#62 loop came from.** Interlocking claims across
  two design documents invited exactly the patch-introduces-inconsistency cycle the spec
  describes. Mitigation: both documents are edited in one task, by one implementer, against
  the already-shipped code, with the spec's §6/§6.1 tables as the site-by-site checklist —
  and originals are kept, never deleted, at the fixed-point sites.
- **Doc verifies trade latitude for coverage.** Twelve site-anchored checks replace the
  earlier loose counts, which could pass with an amendment site omitted. The cost is that
  each check now pins an original sentence verbatim as its anchor — which is exactly what
  §6.1 requires be kept, so the constraint and the requirement are the same one — while
  the amendment's own wording stays free.
- **The verify commands are the plan's own dogfood.** This run exists because the plan
  gate could not stop; its first round found that two tasks could pass while their
  requirement failed. Both were verification gaps, not decomposition errors, which is the
  argument for fixing them here rather than trusting the per-task review to catch them
  later against real code.

## Class justifications (tasks below `implement`)

- **Task 3 (`test`)** — it writes tests against behaviour tasks 1 and 2 already shipped;
  no production code changes. That is the definition of the class.
- **Task 4 (`mechanical`)** — one sentence, applied identically to two files, with the
  wording given verbatim in the spec; two files, under the three-file mechanical cap.
- **Task 5 (`docs`)** — prose only, across exactly two documents; the spec enumerates
  every passage (§6's table and §6.1's site list) and prescribes the supersede-in-place
  method. The `task check` it carries verifies the branch, not this task's judgement.
