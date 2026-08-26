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
a run longer than six is not a marker.

**Decoration run.** A maximal run of the characters `*`, `_` and `` ` ``. A run may
mix them (`` *` `` is one run of length two); the runs at the two ends of a value are
**balanced** only when their text is byte-identical (`**`…`**` is balanced, `**`…`*`
is not, `` *_ ``…`` _* `` is not).

### Step 1 — strip leading markers

From the front of the trimmed line, repeatedly remove one marker and the whitespace
after it, until the front of the line is not a marker. Looping is what handles stacked
markers such as `> 1. `.

The required whitespace is what disambiguates `*`: `* STATUS: done` is a bullet,
`**STATUS:** done` is emphasis, and only the first form is consumed here.

### Step 2 — split the decoration run

Take the leading decoration run off the front of what remains and keep it as `deco`
(possibly empty).

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
   (the whole-line-wrapped `**STATUS: done**` shape). Trim again.
3. Drop one **balanced** wrapper as defined above (the `STATUS: **done**` shape).
   Trim again.
4. For `STATUS`, lowercase the result, as today.

Step 3's balance requirement is deliberate: `SUMMARY: fixed *parseReport*` keeps its
internal emphasis intact, because no `deco` preceded the key and the value's start and
end runs do not match.

### Accepted shapes — the matrix

The accepted set is a cross-product, not a list of examples. A line is a trailer line
exactly when it is

```
<marker>* <deco-open><KEY>:<deco-close> <deco-open><value><deco-close>
```

with each part optional per the grammar above. The three axes:

| axis | values |
|---|---|
| marker prefix (M) | none · `-` · `*` · `+` · `>` · `#` · `##` · `1.` · `2)` · `007.` · stacked `> 1.` |
| decoration placement (P) | none · around the key · around the value · around key **and** value · around the whole line |
| decoration run (D) | `*` · `**` · `_` · `__` · `` ` `` |
| key (K) | `STATUS` · `SUMMARY` · `BLOCKERS` |

Worked examples, one per placement:

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

### Shapes that must not match

| line | why |
|---|---|
| `the digest is rejected when STATUS: is missing` | the key does not start the line |
| `see STATUS: done in the brief` | same |
| `status: done` | keys stay uppercase |
| `STATUS done` | no colon |
| `-STATUS: done` | a marker needs the space after it |
| `####### STATUS: done` | seven `#` is not a heading marker |

## Files

| file | change |
|---|---|
| `internal/cli/cmd_record.go` | `parseReport` plus the new unexported helpers; the doc comment states the tolerance |
| `internal/cli/cmd_record_test.go` (new) | table-driven tests, `//nolint:testpackage // tests an unexported helper` + `package cli` |
| `agents/implementer.md` | the report-contract sentence mentions the tolerance |
| `hosts/copilot/agents/takt-implementer.agent.md` | regenerated by `go run ./internal/tools/hostgen` |
| `docs/superpowers/specs/2026-08-24-takt-design.md` | §5.1 row for `takt record --task` (line 330) describes the tolerant parse |

## Tests

`internal/cli/cmd_record_test.go` proves the matrix rather than a handful of examples:

- **Cross-product case.** Nested table-driven loops over the marker axis (M), the
  decoration-run axis (D) and the placement axis (P), asserted for each key (K). Every
  generated line must yield the expected key and the value `done` / `fixed the parser`
  / `none` with all decoration removed. The subtest name carries M/D/P/K so a failure
  names the exact combination.
- **Must-not-match rows**, one per line of the table above, asserting the field stays
  empty.
- **Emphasis preservation:** `SUMMARY: fixed *parseReport*` keeps its internal `*`.
- **Whole message:** the brief's template block quoted earlier in the body, the real
  (decorated) trailer last — last occurrence wins, and the earlier template lines do
  not leak into the digest.
- **Regression:** the plain undecorated trailer parses exactly as before.

The file follows the internal-test convention already used by `slug_test.go` and
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
| Is an unordered marker a run or a single character? | A single character, except `#`, where one to six are a heading. | `**` must stay decoration, and `--`/`>>` are not markdown markers. | assumed (spec review, minor) |
| What may an ordered marker look like? | One or more ASCII digits, leading zeros allowed, then `.` or `)`, then whitespace. | Removes the "zero, leading zeros, arbitrary digits" ambiguity the review named. | assumed (spec review, minor) |
| When are the decoration runs around a value stripped? | Only when byte-identical at both ends, or when they close the run that preceded the key. | Asymmetric emphasis inside a summary is content, not decoration. | assumed |
| Does `*` need following whitespace to count as a list marker? | Yes. | Without it, `**STATUS:**` would lose one `*` to the marker stripper. | assumed |
| Do the `--status` / `--summary` / `--blockers` flags still override? | Yes, unchanged via `cmp.Or` in `recordTask`. | Out of scope; the flags are the escape hatch when parsing fails entirely. | assumed |
| Is `regexp` used? | No — plain `strings` steps. | The package is stdlib-only by constraint and the repo's parsing style is explicit string handling; the steps are simpler to read than one dense pattern. | assumed |
| Does anything about the digest schema or `recordTask`'s validation change? | No. | The failure was purely in extraction. | assumed |
