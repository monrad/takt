# Plan — lets-work-on-63

Retro as the project's record: takt renders the deterministic sections of the retrospective
into `finish/retro-skeleton.md`, the session copies it to `retro.md` and fills the prose
slots, and a new `takt retro --rewrite` makes the whole thing redoable after archiving.

## Approach

The dependency spine is short: a new leaf package (`internal/spec`) feeds the new pure
renderer (`internal/finish/skeleton.go`), which the CLI wires into the one code path that
already derives the retro inputs (`writeRetroInputs`, moved to a shared helper in a new
`internal/cli/retro.go`). Everything else hangs off that spine — the `gate_answered` reason
is an independent two-line plumbing change, the template rewrite is independent of the
renderer (it only names a path), and the two consumer-facing surfaces (`takt retro`,
`done --step retro` in `archived`) sit on top of the shared helper. Documentation and the
host-prompt parity change close the run, with the repository-wide gates (G13) attached to
the final task so they run over the assembled tree, following the precedent of the previous
run's plan.

Wave shape as takt will compute it: tasks 1, 2, 4 and 8 have no dependencies and can run
together (all files disjoint); 3 follows 1; 5 follows 3 and 4; 6 follows 5 (shared
`finish_test.go`); 7 follows 5 (shared `cmd_next.go`) and 6 (reuses the finish-or-archived
phase check); 9 is last and depends on 2, 6, 7 and 8 so its full-tree verify commands mean
something.

## Tasks

**Task 1 — `internal/spec`: ParseAssumptions (G5, implement).** A new leaf package parallel
to `internal/goals`, exactly as the spec's §5.2 draws it: `Assumption{Question, Decision,
Rationale, Source}` and a `ParseAssumptions([]byte) []Assumption` that finds the
`## Assumptions & Open Decisions` heading case-insensitively, reads the table under it by
header name rather than column position, and yields an empty slice — never an error — for
every malformed shape, including a header row not followed by a valid separator: without
that case an implementation that blindly discards the line after the header would pass. Scoped to two new files so it can land in the first wave; nothing
else compiles against it until task 3.

**Task 2 — `gate_answered` carries `--reason` (G4, bounded).** Below `implement` because it
is three files, fully specified by spec §5.1 — thread the `*reason` already in scope at
`cmd_answer.go:74` into `clearGate` and record it only when non-empty — and the tests are
enumerated in §9. No judgement beyond the key-omission rule the spec states.

**Task 3 — the skeleton renderer (G1, G2, G6, implement).** The heart of the change:
`BuildShipped`, `BuildDecisions` and the pure `RenderSkeleton`, plus the
`SkeletonPath`/`WriteSkeleton` pair mirroring `RetroInputsPath`/`WriteRetroInputs` in the
same package. Kept to two files (renderer + tests) and free of any CLI wiring so the golden
renders and the purity assertion test the function, not the plumbing. Depends on task 1 for
the `spec.Assumption` type in `BuildDecisions`' signature.

**Task 4 — the template rewrite (G7, bounded).** Below `implement` because spec §6 dictates
every sentence the new `run-retro.md` must carry, §3 dictates the seven headings, and
G7's evidence names the exact assertions; the code half is a single struct field
(`RunData.SkeletonPath`). The template's instructions are the only enforcement of the
writing workflow, so its test pins each of the five load-bearing ones — no rewriting the
rendered sections, numbers-only grounding, the invitation to the session's own account, the
fresh-session no-invention rule and the closing `done --step retro` line — not just the
headings and the path. It deliberately does not depend on task 3: the template names a
path and headings, and the heading strings are pinned identically in both tasks'
descriptions (and by both tasks' tests) so they cannot drift silently.

**Task 5 — the shared derivation writes both files (G3, G10, implement).**
`nextRun.writeRetroInputs` and the retro branch of `nextRun.run` move to a `bdir`/`state`
helper in a new `internal/cli/retro.go`, extended to parse the spec's assumptions, build
`SkeletonExtras` and write `finish/retro-skeleton.md` atomically beside the inputs; the
retro op gains `skeleton_path`. **One ownership model, fixed here and binding on task 7:**
`retroRunOp` is the sole caller of `writeRetroArtifacts` — it derives the pair once and then
builds the op. Both `nextRun.run` and task 7's `cmdRetro` call `retroRunOp` and nothing else,
so neither path can derive and write the two files twice. The replay test (run `next` twice, byte-identical pair) is
G3's evidence and lives in `finish_test.go`, which is why tasks 6 and 7 are ordered after
this one. Depends on 3 (the renderer) and 4 (the `SkeletonPath` field the op data fills).

**Task 6 — `done --step retro` in `archived`, and the prose-slot guard (G9, bounded).**
Below `implement` because the change is three files and fully specified: `finishPhaseOnly`
gains a finish-or-archived sibling used by this one step, and a `<!-- prose:` marker still
present in `retro.md` is an error naming the slot — the spec's own assumptions table fixes
both the marker syntax and the check. Ordered after 5 for the shared `finish_test.go`.

**Task 7 — `takt retro --rewrite` (G8, G10, implement).** The new command: flags, the
usage error without `--rewrite`, the finish-or-archived phase rule, the run lock taken as
`next` takes it but reported as an error (hinting `takt unlock`) instead of an ask op, the
re-derivation through task 5's helper, and the same `run`/`retro` op with narration
"rewrite the retrospective". Lists `cmd_next.go` only to allow extracting the lock
acquisition for reuse; ordered after 5 (that file, and the helper) and 6 (the phase-check
helper).

**Task 8 — the design doc (G11, docs).** Class `docs` because it is prose in one file with
no behaviour: §4.2 gains the skeleton line, §7.5 step 3 is rewritten around the seven
sections and the disposition's first-pass absence, and §5.1's command table gains `retro`
(§6 is the command prompt, not the command list). Its verify commands are section-scoped —
each greps only inside the section the edit belongs to — so a string that lands somewhere
else in a 1100-line document does not pass for the edit. It has no dependencies — the spec
fully determines the content — so it can land in the first wave.

**Task 9 — host prompts, parity, and the branch-green gates (G12, G13, bounded).** Below
`implement` because the wording is dictated: the retro `run` row in `commands/takt.md` and
`hosts/copilot/skills/takt/SKILL.md` is replaced with one identical sentence naming the
skeleton, and the *entire* clause — through "leave the rendered sections as they are; the numbers
live at `inputs.inputs_path`" — is appended to `prompt_test.go`'s `crossHostInvariants`, so
the two copies cannot drift on any part of it. Pinning only the opening fragment would let
them disagree about the rendered sections or the numbers' location and still pass. As the last task of the final wave it carries the repository-wide
gates — `go test ./... -race`, `golangci-lint run ./...`, `task hosts:check` — over the
fully assembled tree, which is G13's evidence.

## Risks

- **Event decoding.** `wave_committed` data decodes as JSON (`float64` ids, `[]any` task
  lists); `BuildShipped` must floor a missing slice to 1 exactly as `timingKeyOf` does, or
  pre-slice bundles render a slice-0 row. The task description pins this.
- **Skeleton/template heading drift.** The seven headings appear in two places (renderer,
  template). Both task descriptions spell the identical strings and both test files assert
  them, so a drift fails one side's verify.
- **The archived-phase surface.** `done --step retro` on an archived run takes an ordinary
  bundle commit; design §7.5 step 5 already contemplates post-archive bundle writes, and
  the existing `recommitArchive` path sweeps anything left dirty. The lock in `takt retro`
  is what keeps that sweep from capturing a half-updated inputs/skeleton pair; the held-lock
  test pins the refusal.
- **Serial tail.** Tasks 5 → 6 → 7 → 9 are forced serial by shared files
  (`finish_test.go`, `cmd_next.go`, the phase helper). This is the cost of keeping every
  test where §9 of the spec says it lives; the waves are small, so the cost is time, not
  risk.
