takt's retro is written for takt; masterplan's is written for the project. Comparing the
three retros under `docs/takt/` with masterplan's fourteen:

## Goals

- G1 — `internal/finish.RenderSkeleton` renders the four deterministic sections — the wave × tasks × commit table, Decisions, the Not-proven seed and bucketed Follow-ups — plus the Numbers block verbatim from the inputs, and is pure: the same input renders identical bytes twice. — achieved
- G2 — The *What shipped* table carries one row per `wave_committed` event — retried attempts and backfills included — with the commit SHA and each task's id and title, resolved by `BuildShipped` from `plan.Index` so that `RenderSkeleton` itself looks nothing up. — achieved
- G3 — `finish/retro-skeleton.md` is written atomically by the same code path that writes `finish/retro-inputs.json`, so a replayed `takt next` writes identical bytes and re-emitting the retro op is free. — achieved
- G4 — `gate_answered` events carry the user's `--reason`, omitted when none was given, and an event written before the field existed still reads as a reasonless answer. — achieved
- G5 — A new `internal/spec.ParseAssumptions` parses spec.md's `## Assumptions & Open Decisions` table by header name rather than column position, and yields an empty slice — never an error — for a spec with no section, no table, missing headers or a short row. — achieved
- G6 — Decisions render from all five sources: gate answers **carrying a reason** (a reasonless or legacy answer contributes nothing), `task_waived`, `goal_waived`, the disposition **when non-nil** — nil on the first pass, since `decideFinish` emits the retro before `branch_finish`, where it renders "not yet chosen" — and the spec's `user-confirmed` assumptions, which reach the page only through `BuildDecisions`. — achieved
- G7 — `internal/brief/templates/run-retro.md` instructs the seven-section retro, tells the session to start from the skeleton and fill the `<!-- prose: … -->` slots, scopes "grounded in the inputs" to numbers only, invites the session's own observations, warns a fresh-session rewrite not to invent an account, and no longer says "dispatch→commit". — achieved
- G8 — `takt retro --rewrite` takes the run lock, re-derives the inputs and skeleton and prints the same `run`/`retro` op `next` emits, in the `finish` and `archived` phases; bare `takt retro` is a usage error, an earlier phase is refused, and a held lock is reported rather than written through. — achieved
- G9 — `done --step retro` is accepted in the `archived` phase, still refused in `execute`, and refuses a `retro.md` that still contains an unfilled `<!-- prose:` slot. — achieved
- G10 — The retro op's shared derivation is called by both `takt next` and `takt retro` from one helper, with no behaviour change on the `next` side. — achieved
- G11 — The design doc records the change: `finish/retro-skeleton.md` in the §4.2 bundle layout, the seven-section retro, the skeleton and the disposition's absence on the first pass in §7.5 step 3, and `retro` in the §5.1 command table (§6 is the command prompt, not the command list). — achieved
- G12 — `commands/takt.md` and `hosts/copilot/skills/takt/SKILL.md` both describe the retro `run` row as starting from the skeleton, and stay in parity. — achieved
- G13 — The branch is green on the repository's own checks. — achieved

## Run

Bundle: docs/takt/lets-work-on-63/ — spec.md, plan.md, reviews/, retro.md
