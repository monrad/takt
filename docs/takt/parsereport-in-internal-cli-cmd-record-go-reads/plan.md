# Plan — parsereport-in-internal-cli-cmd-record-go-reads

## Approach

The spec is unusually complete: it fixes the grammar (markers, decoration runs, the
opener rule), the exact set of accepted and rejected shapes, the test structure down to
the cross-product oracle, and the three prose surfaces that state the contract. The
plan therefore follows the spec's own file table almost one-to-one and spends its
judgement on task boundaries and verification, not on design.

Four tasks. The parser change and its grammar tests land together as one task — the
tests are the specification made executable, and splitting them would leave one half
with no verify command that can fail. The `execute_test.go` override case is a separate
task ordered after it, because it edits and compiles the same `internal/cli` package:
running its verify while another agent has `cmd_record.go` half-edited in the same
worktree would fail spuriously, so the ordering buys wave stability even though the
files do not overlap. The two documentation surfaces are independent of the code and of
each other (the Go suite's host-parity tests use inline fixtures, verified in
`internal/hosts/copilot_test.go` and `internal/tools/hostgen/main_test.go`, so editing
`agents/implementer.md` cannot break a concurrently running `go test ./...`), and they
can share the first wave with the parser task.

Verify commands are chosen so at least one per task genuinely fails before the work: a
`grep -q` for a file or sentence that does not exist yet, followed by the real gates
(`go test -race`, `golangci-lint run`, `hostgen --check`). Tasks 3 and 4 therefore
mandate one concrete word each ("decorated"; task 4 also "backtick") in the sentence
they add — a small imposition on wording that makes the verify honest. The greps are
anchored to the sentence and the row rather than run over the whole file: task 3
requires "decorated" and "marker" on the same line as "End your final message", and
task 4 requires "decorated" and "backtick" on the `takt record --task N` row itself,
with that row still carrying its stale-attempt sentence and still being exactly one
row. A word added anywhere else in a thousand-line spec no longer satisfies them.

## Task 1 — Tolerant trailer grammar in `parseReport`, proven by its own tests (implement)

Rewrites the per-line matching in `internal/cli/cmd_record.go`'s `parseReport`
(currently `strings.HasPrefix` on `STATUS:`/`SUMMARY:`/`BLOCKERS:` at line ~333) to
delegate to new unexported helpers implementing the spec's four steps: strip leading
markers (loop; `-`/`*`/`+`/`>` single-character with mandatory following whitespace,
`#`×1–6, ordered `digits` + `.`/`)`), drop the decoration run before the key (recording
an opener), match the exact uppercase key with colon anchored at what remains, then
clean the value (trim; repeatedly strip leading decoration runs, each an opener; strip
one trailing run only if the line produced an opener; lowercase STATUS). Signature,
last-occurrence-wins, and `recordTask` stay untouched; stdlib `strings` only, no
`regexp`; helpers small enough that `cyclop`/`gocognit`/`funlen` stay quiet. The new
`internal/cli/cmd_record_test.go` follows the `slug_test.go` convention
(`//nolint:testpackage // tests an unexported helper` + `package cli`, no
`export_test.go` entry) and carries the spec's whole test inventory: the full
M×Dk-open×Dk-close×Do×Dc×K cross-product with the opener-rule oracle (a lone closer
survives only when every other slot is empty), the boundary rows (`######` vs
`#######`, `0.`/`007.`/`2)`, stacked `> - 1.`, mixed run), both sides of the opener
rule, `STATUS:done`, every must-not-match row asserting empty fields, the blunt
`**SUMMARY: fixed *parseReport***` case, the whole-message last-occurrence case, and
the undecorated regression, and the marker boundaries that separate the grammar from a
plausible misreading of it: `--` and `>>` are not markers (so those lines do not match
at all), a tab satisfies the marker whitespace, an ordered marker is ASCII digits with
no sign, and `** STATUS: done` does not match at all — `**` is not a marker, and
stripping it as decoration leaves the key behind a space, unanchored. The adjacent
`**STATUS: done` is the decoration-path case. Scoped as one task because the tests are the only thing
that can make the verify fail-before/pass-after, and the two files are one change.
It carries the repo gates (`go test -race ./...`, `golangci-lint run ./...`) and G6 —
though task 2 touches Go code afterwards and repeats them, so the final assembled state
is gated too.

## Task 2 — Flag overrides beat the parsed trailer (test)

Adds the one case the spec says nothing exercises today: `internal/cli/execute_test.go`
gains `TestRecordFlagsBeatParsedTrailer`, using the existing `executeRun`/`runIn`
fixtures — write a report file whose trailer says `STATUS: failed`, record it with
`--status done --summary "s" --blockers none`, and assert the digest
(`waves/0/task-1.a1.digest.json` per `digestPath`, or the task's `LastDigest` via
`bundle.LoadState`) carries `done`/`s`/`none`, proving `cmp.Or` in `recordTask` keeps
the flags ahead of parsed values. Class `test` is right by definition — it tests the
existing `cmp.Or` override path, which this run does not change. It depends on task 1
not because files overlap but because its verify compiles and runs the package task 1
is rewriting; sequencing avoids a flaky shared-worktree wave.

Two things the task must get right. `executeRun` builds its fixture with tasks and a
phase but no `ActiveWave`, so a bare `record` is rejected with "task 1 is not in the
active wave" — wave 0 has to be dispatched first through the shared `next` helper
(`internal/cli/cmd_next_test.go:65`), the way `TestWaveLaunchCloseAndCommit` does. And
because this is the *last* task to touch Go code, it — not task 1 — is where the
repository gates belong: task 1's `go test -race ./...` and `golangci-lint run ./...`
run before `execute_test.go` is edited and therefore cannot cover the assembled change.
Task 1 keeps its copies (it must be green on its own), and task 2 repeats them as the
terminal check.

## Task 3 — State the tolerance in the implementer contract; regenerate the Copilot agent (docs)

Amends the report-contract sentence in `agents/implementer.md` (the Rules line ending
"End your final message with exactly three lines: …") to say the trailer should be
plain but that takt tolerates decorated lines — the sentence must contain the word
"decorated" and the word "marker", on the same line as "End your final message", so
the grep proves the contract sentence changed rather than the file. Then regenerates
`hosts/copilot/agents/takt-implementer.agent.md` with `go run ./internal/tools/hostgen`
rather than editing it by hand; `go run ./internal/tools/hostgen --check` proves
parity. `internal/brief/templates/implementer.md` is explicitly out of scope (spec
decision: it would churn brief goldens for no gain). Class `docs`: both files are
prose, one of them generated; there is no logic to get wrong, and the greps plus
`hostgen --check` pin the outcome.

## Task 4 — Describe the tolerant parse in the spec of record (docs)

Updates the `takt record --task` row of §5.1 in
`docs/superpowers/specs/2026-08-24-takt-design.md` (line ~330) so the `--from`
sentence describes the tolerant parse: leading list/quote/heading/ordered markers and
bold, italic or backtick decoration around the key, the value, or the whole line are
stripped; keys stay uppercase and colon-anchored at line start; a trailing decoration
run comes off only on a line that opened one. The row must contain the words
"decorated" and "backtick" so the verifies fail before and pass after. One row, no
table restructuring. Class `docs` for the same reason as task 3: the change is a
sentence in the spec of record, fully specified, with grep-pinned evidence.

## Risks

- **The cross-product test is large (~27k lines of input).** It is pure string work
  with inline `t.Errorf` per the spec (no per-case `t.Run`), so runtime is negligible;
  the risk is an implementer building the oracle wrong. The spec gives the oracle
  verbatim (a closer with no opener anywhere survives); task 1's description repeats
  it.
- **Lint pressure.** The helper chain must stay under `cyclop`/`gocognit`/`funlen`
  budgets in the golden config; the spec's four-step decomposition is designed for
  that, and `golangci-lint run ./...` is in task 1's verify so a violation fails the
  task, not the run.
- **What a grep can and cannot prove.** The docs tasks' verifies are tripwires: they
  fail when the edit lands in the wrong file, the wrong sentence or the wrong table row,
  and they fail when a required semantic word is missing. They cannot judge whether the
  prose is *correct* — no grep can. That judgement belongs to the wave review, which
  reads the diff, and to the G4/G5 goal assessment, which reads the finished files. The
  words chosen ("decorated", "marker", "plain", "backtick", "uppercase", "opened") are
  the ones the sentence has to contain to say what the spec says.
- **Grep-mandated wording.** Tasks 3 and 4 constrain one word of prose each so their
  verifies can fail before the work, and anchor the grep to the sentence or table row
  being changed so the check cannot be satisfied from elsewhere in the file. If a
  reviewer prefers different wording, the grep words ("decorated", "marker",
  "backtick") are natural enough that the constraint should not distort the sentence.
- **Concurrent verifies in wave 1.** Tasks 1, 3 and 4 share a wave. Task 3's verifies
  compile only `internal/tools/hostgen` and task 4's are greps, so nothing races task
  1's package rewrite; the host-parity Go tests use inline fixtures, not the real
  agent files.

## Class justifications (below `implement`)

- Task 2 (`test`): asserts behaviour of existing, unchanged code (`recordTask`'s
  `cmp.Or`); the case, flags and expected digest are fully specified.
- Task 3 (`docs`): prose in an agent contract plus a mechanical regeneration; parity
  is machine-checked by `hostgen --check`.
- Task 4 (`docs`): one sentence in the design spec's command table; content fully
  dictated by the approved spec.
