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
mix them (`` *` `` is one run of length two); the runs at the two ends of a value are
**balanced** only when their text is byte-identical (`**`…`**` is balanced, `**`…`*`
is not, `` *_ ``…`` _* `` is not). Balance decides only whether a *pair* is stripped as
a wrapper — never whether the line is accepted.

### Step 1 — strip leading markers

From the front of the trimmed line, repeatedly remove one marker and the whitespace
after it, until the front of the line is not a marker. Looping is what handles stacked
markers such as `> 1. `.

The required whitespace is what disambiguates `*`: `* STATUS: done` is a bullet,
`**STATUS:** done` is emphasis, and only the first form is consumed here.

### Step 2 — split the decoration run

Take the leading decoration run off the front of what remains and keep it as `deco`
(possibly empty). Nothing later requires `deco` to be closed.

### Step 3 — match the key

Match the remainder against the exact, uppercase prefixes `STATUS:`, `SUMMARY:`,
`BLOCKERS:`. No match means the line is not a trailer line and is skipped. Because the
match is anchored at the start of the line (after markers and decoration only), a body
line that merely mentions `STATUS:` mid-sentence can never match.

### Step 4 — clean the value

With `rest` = what follows the key:

1. `strings.TrimSpace(rest)`.
2. If `deco` is non-empty: drop `deco` from the **front** of the value if it is there
   (the `**STATUS:** done` shape), otherwise drop it from the **end** if it is there
   (the whole-line-wrapped `**STATUS: done**` shape). If it is at neither end, leave
   the value alone — the run was simply never closed, and the line still counts.
3. Drop one **balanced** wrapper as defined above (the `STATUS: **done**` shape).
   Trim again.
4. For `STATUS`, lowercase the result, as today.

Step 3's balance requirement is deliberate: `SUMMARY: fixed *parseReport*` keeps its
internal emphasis intact, because no `deco` preceded the key and the value's start and
end runs do not match.

### Accepted shapes — the axes

The accepted set is a cross-product, not a list of examples. A line is a trailer line
when it is

```
<marker>* <deco>?<KEY>:<deco>? <deco>?<value><deco>?
```

with every `<deco>` independent and optional — no `<deco>` has to be matched by
another. The axes the tests enumerate:

| axis | representative classes | boundaries also tested |
|---|---|---|
| marker prefix (M) | none · `-` · `*` · `+` · `>` · `#` · `1.` | `######` (six, accepted) · `#######` (seven, rejected) · `0.` · `007.` · `2)` · stacked `> - 1.` |
| decoration placement (P) | none · key · value · key+value · whole line | opened-and-never-closed · closing run only · mismatched pair |
| decoration run (D) | `*` · `**` · `_` · `__` · `` ` `` | mixed run `` *` `` |
| key (K) | `STATUS` · `SUMMARY` · `BLOCKERS` | — |

The M/D/P/K values in the first column are **representative classes**, not the whole
language: the grammar above is the authority, and the boundary column is what pins the
places where a plausible implementation could disagree with it.

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

- **Cross-product case.** Nested table-driven loops over the marker axis (M), the
  decoration-run axis (D) and the placement axis (P), asserted for each key (K). Every
  generated line must yield the expected key and the value `done` / `fixed the parser`
  / `none` with all decoration removed. The subtest name carries M/D/P/K so a failure
  names the exact combination.
- **Boundary rows** from the second column of the axes table: `######` accepted and
  `#######` rejected, `0.` and `007.` and `2)` accepted, stacked `> - 1.` accepted, a
  mixed decoration run accepted.
- **Unbalanced decoration rows:** opened-and-never-closed, closing-run-only and
  mismatched-pair lines all parse, per the tolerance ruling.
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
| When are the decoration runs around a value stripped? | Only when byte-identical at both ends, or when they close the run that preceded the key. | Asymmetric emphasis inside a summary is content, not decoration. | assumed |
| Does `*` need following whitespace to count as a list marker? | Yes. | Without it, `**STATUS:**` would lose one `*` to the marker stripper. | assumed |
| Is `regexp` used? | No — plain `strings` steps. | The package is stdlib-only by constraint and the repo's parsing style is explicit string handling; the steps are simpler to read than one dense pattern. | assumed |
| Does anything about the digest schema or `recordTask`'s validation change? | No. | The failure was purely in extraction. | assumed |
