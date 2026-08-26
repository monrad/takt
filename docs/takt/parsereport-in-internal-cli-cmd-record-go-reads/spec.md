# Tolerant STATUS / SUMMARY / BLOCKERS trailer parsing

## Problem

`parseReport` (`internal/cli/cmd_record.go:333`) reads an implementer's final message
line by line, trims each line and matches with `strings.HasPrefix` on the exact
strings `STATUS:`, `SUMMARY:` and `BLOCKERS:`. A model that decorates its trailer the
way models routinely decorate output — `**STATUS:** done`, `- STATUS: done`,
`STATUS: **done**`, `` `STATUS:` done `` — matches nothing, so `recordTask` sees an
empty status and fails the record with:

```
digest status must be done, failed or blocked
```

The wave then stalls on an attempt that in fact succeeded. The agent contract asks for
undecorated lines, but a prompt is not an enforcement mechanism: the parser is the
right place to absorb the variation.

## Scope

One function and its tests, plus the three places that state the contract. No change
to `recordTask`, to the digest schema, to the `--status` / `--summary` / `--blockers`
overrides (they still win over the parsed values through `cmp.Or`), or to
last-occurrence-wins semantics.

## Design

`parseReport` keeps its signature `func(text string) (string, string, string)` and its
loop over lines with last-occurrence-wins. Per line it delegates to a new unexported
helper that decides whether the line is a trailer line and, if so, for which key and
with what value. The helper is split into small steps so `cyclop`, `gocognit` and
`funlen` stay quiet under the golden config.

### What this parser is, and is not

It is a **tolerance layer, not a markdown validator**. Decoration is *removed where it
is found*; it is never required to be well formed, and malformed decoration is never a
reason to reject a line. `**STATUS: done` — an opened bold run the model forgot to
close — records `done`, deliberately: the failure this change exists to remove is a
correct result thrown away over punctuation, and a stricter reading would reintroduce
it in a new shape.

What *does* reject a line is structural: the key must be uppercase, must carry its
colon, and must start the line once markers and decoration are removed. Those three
are the whole must-not-match set, and they are what keeps prose out of the digest.

### Grammar

Two terms are used below and are defined here exactly, so two implementations cannot
disagree about what is accepted.

**Marker.** One of:

- a single `>`;
- a single `-`, `*` or `+`;
- a run of one to six `#`;
- an *ordered marker*: one or more ASCII digits (`0`–`9`, leading zeros permitted,
  no sign, no other characters) followed by a single `.` or `)`.

Every marker must be followed by at least one space or tab. An unordered marker is
**one** character, never a run: `--` and `>>` are not markers, and `**` is therefore
never consumed as a marker. `#` is the one exception, because `##` is a real heading;
a run of seven or more is not a marker.

**Decoration run.** A maximal run of the characters `*`, `_` and `` ` ``. A run may
mix them (`` *` `` is one run of length two). Runs are never required to match each
other: the run before the key, the run opening the value and the run closing the value
are three independent slots, and `**`…`*` is as acceptable as `**`…`**`. A run is
*interior* to a value when it starts after the first byte and ends before the last —
the one distinction step 4 draws.

### Step 1 — strip leading markers

From the front of the trimmed line, repeatedly remove one marker and the whitespace
after it, until the front of the line is not a marker. Looping is what handles stacked
markers such as `> 1. `.

The required whitespace is what disambiguates `*`: `* STATUS: done` is a bullet,
`**STATUS:** done` is emphasis, and only the first form is consumed here.

### Step 2 — drop the decoration before the key

Drop the leading decoration run from the front of what remains. It is consumed here and
plays no further part: nothing requires it to be closed, and the value is cleaned by
its own rules in step 4. Making the value's cleaning independent of the key's
decoration is what keeps every slot in the grammar independent.

### Step 3 — match the key

Match the remainder against the exact, uppercase prefixes `STATUS:`, `SUMMARY:`,
`BLOCKERS:`. No match means the line is not a trailer line and is skipped. Because the
match is anchored at the start of the line (after markers and decoration only), a body
line that merely mentions `STATUS:` mid-sentence can never match.

### Step 4 — clean the value

With `rest` = what follows the key:

1. `strings.TrimSpace(rest)`.
2. Repeatedly drop a leading decoration run and the whitespace after it, until the
   value no longer starts with one. Repetition is required, not cosmetic: in
   `` `STATUS:` `done` `` the first run is the *key's* closer and the second opens the
   value, and a single strip would leave `` `done ``.
3. Drop one trailing decoration run, if the value ends with one **and** the value has
   no *interior* decoration run — a run that starts after the first byte and ends
   before the last. Trim.
4. For `STATUS`, lowercase the result, as today.

The interior guard in step 3 is the whole subtlety, and it is what distinguishes
punctuation from content. `STATUS: done**` has no interior run, so the stray `**` is
decoration and comes off. `SUMMARY: fixed *parseReport*` has one (the `*` before
`parseReport`), so the trailing `*` is the closer of an emphasis the author meant, and
the value survives verbatim.

Every accepted shape below follows from these three steps alone; the decoration before
the key never reaches the value.

### Accepted shapes — the axes

The accepted set is a cross-product, not a list of examples. A line is a trailer line
when it is

```
<marker>* <Dk-open>?<KEY>:<Dk-close>? <Do>?<value><Dc>?
```

with every `<deco>` independent and optional — no `<deco>` has to be matched by
another, and any slot may be empty. The axes the tests enumerate:

| axis | representative classes | boundaries also tested |
|---|---|---|
| marker prefix (M) | none · `-` · `*` · `+` · `>` · `#` · `1.` | `######` (six, accepted) · `#######` (seven, rejected) · `0.` · `007.` · `2)` · stacked `> - 1.` |
| run before the key (Dk-open) | none · `*` · `**` · `_` · `__` · `` ` `` | mixed run `` *` `` |
| run after the key's colon (Dk-close) | none · `*` · `**` · `_` · `__` · `` ` `` | — |
| run opening the value (Do) | none · `*` · `**` · `_` · `__` · `` ` `` | — |
| run closing the value (Dc) | none · `*` · `**` · `_` · `__` · `` ` `` | — |
| key (K) | `STATUS` · `SUMMARY` · `BLOCKERS` | — |

The four decoration slots vary **independently**: no combination is excluded, and a
mismatched pair (`*STATUS:** done`) or a lone closer (`STATUS: done**`) is as
acceptable as a matched one. That independence is the grammar, so the tests enumerate
the full product rather than a diagonal through it.

The values in the first column are **representative classes**, not the whole language:
the grammar above is the authority, and the boundary column is what pins the places
where a plausible implementation could disagree with it.

Worked examples:

| line | key | value |
|---|---|---|
| `STATUS: done` | STATUS | `done` |
| `**STATUS:** done` | STATUS | `done` |
| `STATUS: **done**` | STATUS | `done` |
| `` `STATUS:` `done` `` | STATUS | `done` |
| `**STATUS: done**` | STATUS | `done` |
| `> 1. **STATUS: done**` | STATUS | `done` |
| `_SUMMARY:_ fixed the parser` | SUMMARY | `fixed the parser` |
| `+ __BLOCKERS:__ none` | BLOCKERS | `none` |
| `**STATUS: done` (never closed) | STATUS | `done` |
| `STATUS: done**` (closing run only) | STATUS | `done` |
| `*STATUS:** done` (mismatched pair) | STATUS | `done` |

### Shapes that must not match

| line | why |
|---|---|
| `the digest is rejected when STATUS: is missing` | the key does not start the line |
| `see STATUS: done in the brief` | same |
| `status: done` | keys stay uppercase |
| `Status: done` | same |
| `STATUS done` | no colon |
| `-STATUS: done` | a marker needs the whitespace after it |
| `####### STATUS: done` | seven `#` is not a heading marker |

Malformed decoration is **not** on this list, by the ruling above: the last three rows
of the accepted table are its counter-examples.

## Files

| file | change |
|---|---|
| `internal/cli/cmd_record.go` | `parseReport` plus the new unexported helpers; the doc comment states the tolerance |
| `internal/cli/cmd_record_test.go` (new) | table-driven tests, `//nolint:testpackage // tests an unexported helper` + `package cli` |
| `internal/cli/execute_test.go` | one case proving `--status` / `--summary` / `--blockers` still beat the parsed trailer |
| `agents/implementer.md` | the report-contract sentence mentions the tolerance |
| `hosts/copilot/agents/takt-implementer.agent.md` | regenerated by `go run ./internal/tools/hostgen` |
| `docs/superpowers/specs/2026-08-24-takt-design.md` | §5.1 row for `takt record --task` (line 330) describes the tolerant parse |

## Tests

`internal/cli/cmd_record_test.go` proves the grammar rather than a handful of examples:

- **Cross-product case.** Nested table-driven loops over M × Dk-open × Dk-close × Do ×
  Dc × K — the full product of the axes above, every slot independent (7 × 6 × 6 × 6 ×
  6 × 3 lines, all pure string work). Every generated line must yield the expected key
  and the value `done` / `fixed the parser` / `none` with all decoration removed. The
  assertion is inline (`t.Errorf` naming the combination and the line), not one
  `t.Run` per case: tens of thousands of subtest frames would cost more than the
  strings they check.
- **Boundary rows** from the second column of the axes table: `######` accepted and
  `#######` rejected, `0.` and `007.` and `2)` accepted, stacked `> - 1.` accepted, a
  mixed decoration run accepted.
- **Interior-run rows:** a value with an interior decoration run keeps its trailing
  run (`fixed *parseReport*`), and one without loses it (`done**`) — the two sides of
  the step 3 guard.
- **Must-not-match rows**, one per line of the must-not-match table, asserting the
  field stays empty.
- **Emphasis preservation:** `SUMMARY: fixed *parseReport*` keeps its internal `*`.
- **Whole message:** the brief's template block quoted earlier in the body, the real
  (decorated) trailer last — last occurrence wins, and the earlier template lines do
  not leak into the digest.
- **Regression:** the plain undecorated trailer parses exactly as before.

`internal/cli/execute_test.go` gains one case for the override half of the contract,
which no test exercises today (its `record` helper always goes through `--from`): a
report file whose trailer says `failed`, recorded with `--status done --summary "s"
--blockers none`, produces a digest with status `done` and summary `s` — `cmp.Or` in
`recordTask` keeps the flags ahead of the parsed values.

The new file follows the internal-test convention already used by `slug_test.go` and
`brief_stable_test.go`; no `export_test.go` entry is added.

## Verification

- `go test -race ./internal/cli/...`
- `go run ./internal/tools/hostgen --check`
- `go test -race ./...`
- `golangci-lint run ./...`

## Assumptions & Open Decisions

| question | decision | rationale | source |
|---|---|---|---|
| How far does the tolerance go? | Leading `-`, `*`, `+`, `>`, `#` and ordered-list markers, and `*` / `_` / `` ` `` decoration around the key, around the value, or around the whole line. | Covers the shapes models actually emit without inviting false positives. | user-confirmed |
| Are lowercase keys (`status:`) accepted? | No. | A prose line beginning "status: ..." would silently become the recorded digest. | user-confirmed |
| Is a key matched anywhere on the line? | No — only at the start, after markers and decoration only. | Keeps a mid-sentence mention from overwriting a real trailer. | user-confirmed |
| Which surfaces change besides the parser and tests? | `agents/implementer.md`, the regenerated Copilot agent, and spec §5.1. | The repo rule is that a behaviour change amends the spec in the same commit; the Copilot agent is generated and held in parity by `task hosts:check`. | user-confirmed |
| Does the brief template (`internal/brief/templates/implementer.md`) also change? | No. | The agent file already carries the contract; changing the template would churn the brief goldens for no behavioural gain. | user-confirmed |
| Is unclosed or mismatched decoration accepted? | Yes — decoration is stripped where present and never validated. | The parser exists to stop a correct result being thrown away over punctuation; rejecting malformed decoration would reintroduce the same failure in a new shape. Rejection stays structural (case, colon, anchoring). | user-confirmed (spec review pass 2, major ×2) |
| Is an unordered marker a run or a single character? | A single character, except `#`, where one to six are a heading. | `**` must stay decoration, and `--`/`>>` are not markdown markers. | assumed (spec review, minor) |
| What may an ordered marker look like? | One or more ASCII digits, leading zeros allowed, then `.` or `)`, then whitespace. | Removes the "zero, leading zeros, arbitrary digits" ambiguity the review named. | assumed (spec review, minor) |
| Is the M/D/P/K matrix the language, or a sample of it? | A sample: representative classes plus the boundary cases that pin the grammar's edges. | The grammar is the authority; a finite matrix could never be the definition. | assumed (spec review pass 2, minor) |
| Are the `--status` / `--summary` / `--blockers` overrides proven anywhere? | Not today — this change adds the case to `execute_test.go`. | G2 claims the overrides still win and nothing tested it; the claim needs evidence, not assertion. | assumed (spec review pass 2, minor) |
| When are the decoration runs around a value stripped? | A leading run always; a trailing run only when the value has no interior run. | Asymmetric emphasis inside a summary is content, not decoration — an interior run is the evidence that the trailing one closes something the author wrote. | assumed (spec review pass 3, major) |
| Do the key's decoration and the value's interact? | No — the run before the key is consumed in step 2; the key's closing run and the value's opening run are both eaten by step 4's repeated leading strip. | Coupling them made `*STATUS:** done` yield `* done`; a single strip left `` `STATUS:` `done` `` as `` `done ``. | assumed (spec review pass 3, major) |
| Does `*` need following whitespace to count as a list marker? | Yes. | Without it, `**STATUS:**` would lose one `*` to the marker stripper. | assumed |
| Is `regexp` used? | No — plain `strings` steps. | The package is stdlib-only by constraint and the repo's parsing style is explicit string handling; the steps are simpler to read than one dense pattern. | assumed |
| Does anything about the digest schema or `recordTask`'s validation change? | No. | The failure was purely in extraction. | assumed |
