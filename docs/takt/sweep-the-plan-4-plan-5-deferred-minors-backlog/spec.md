# Sweep the plan-4 / plan-5 deferred minors backlog

## Why

Thirteen findings from the plan-4 and plan-5 reviews were filed rather than fixed.
Eleven are Minors that were out of their wave's scope and have sat since, each a
one-file change whose fix the issue already names — cheaper to land as one sweep
than as eleven branches. The other two are rulings the reviews deliberately left
to a human, and one of those decides the wording of a Minor that follows it.

The sweep's value is not any single fix. It is that the backlog stops being a
place findings go to be forgotten, and that `go test -race ./...` and
`golangci-lint run ./...` stay green while thirteen of them land at once.

## Scope

**In:** GitHub issues #1, #2, #3, #4, #5, #6, #10, #11, #12, #13, #14, #15, #16.

**Out:** #9 (already fixed — see below), #34 (open PR #38 on
`fix/doctor-finding-count`), and every issue in the diagnostics (#7 #8 #26 #27 #33
#36 #37), retro (#23 #24 #25 #35 #44) and review-layer (#45 #48 #49 #50 #51)
clusters. Nothing here touches the op protocol, the gate machinery or the review
layers.

### #9 is already fixed

`config.Validate` (`internal/config/config.go`) rejects `lock_ttl`,
`wave_stale_after` **and** `verify_timeout` as non-positive, by name, and
`TestValidateRejectsNonPositiveDurations` (`internal/config/config_test.go`) pins
all three. The issue outlived its fix. It is closed as part of this run's
bookkeeping, not implemented.

## Verified state

Every item below was read in the tree at `bd221d9` before this spec was written.
The line numbers are where it stood then, not a contract.

| Issue | Confirmed defect |
|---|---|
| #1 | `internal/brief/templates/planner.md:4` renders `{{range .Problems}}- {{.}}` as bare text. The auditor, assessor and alignment templates were converted to `{{quote}}`; the planner is the one exception left. |
| #2 | Design §4.6 says, unqualified, that "an older heartbeat is taken over with a `lock_taken` event". Since plan 5's F1 that is not true for a generated holder. |
| #3 | `cmdVersion` dispatches on `*expect != ""`, so `takt version --expect ""` never reaches `versionExpect` and exits 0 printing the version. A whitespace-only expectation is already refused. |
| #4 | `cmd_next.go`'s event switch grades `case orphaned && r.genID` first, so an explicit `--force` from a generated session over a generated holder appends nothing. |
| #5 | `cmd_next.go` writes the brief and the slice diff with `os.WriteFile`; `cmd_init.go`'s `writeLogsIgnore` does the same. `internal/bundle` has `WriteJSONAtomic` and no byte-slice equivalent. |
| #6 | `excludeLogsDir`'s error fails `init` (via `failInit`) and fails `next`. The rule it writes is a convenience: the tracked `logs/.gitignore` still protects clones and commits. |
| #10 | `manifestFailure` calls `ManifestMatches` to decide the failure; `versionExpect` and `versionExpectManifest` each call it a second time to learn `dev`. |
| #11 | `manifestFailure` returns "does not match plugin version" / "update the plugin" for `--expect` too, where the host is the Copilot skill and there is no plugin. |
| #12 | `excludeLogsDir` builds `/<rel>/logs/*` and `!/<rel>/logs/.gitignore` from a repo-relative path and passes them verbatim; `EnsureExclude` documents escaping as the caller's business and takt's only caller does none. |
| #13 | `Taskfile.yml`'s `build` is `go build ./...` with no `-ldflags`, so `task build` produces a binary reporting `0.0.0-dev`. Only the flake and goreleaser stamp. |
| #14 | `internal/hosts/copilot.go:33` uses `fmt.Errorf("agents/%s.md: %w", …)` and `:37` uses `errors.New("agents/" + ccName + ".md: …")` for the same job. Both hardcode `agents/` although `hostgen` accepts `--root`. |
| #15 | `internal/bundle/lock_test.go` has two tests and neither covers the steal boundary or "mine but stale". `TestPromptHandshakeVerbsAndInvariants` loads `commands/takt.md` only. `quotedScalarProblem` looks back exactly one byte. `writeLogsIgnore` compares before writing but only the deleted-and-restored path is tested. |
| #16 | `endAttemptStreak` (`internal/cli/facts.go`) discards both `bundle.ReadEvents` and `bundle.AppendEvent` errors and returns nothing. |

## The two rulings

### #16 — surface the loss, do not fail on it

`endAttemptStreak` runs at every call site (`cmd_record.go` twice,
`record_reviewer.go` twice) **after** the substantive write has already succeeded
and immediately before the command prints its JSON. Failing loud would therefore
exit non-zero on work that is already on disk, and the host prompt's invariant is
to stop on a non-zero exit — a bookkeeping append would halt a run whose record
landed. Staying silent is also wrong: the documented loss (the streak keeps
counting, the next brief keeps quoting a dead rejection) is user-visible at the
moment it happens.

**Decision: report it, keep exit 0.** `endAttemptStreak` returns an `error`; each
caller folds a failure into the JSON it already prints, as a field naming the loss
rather than an error that aborts. The command's contract is unchanged: exit 0,
same keys, one extra key when something was lost.

This is the same degradation shape #6 asks for, and the two must use one idiom:
a named field in the command's own JSON output, at exit 0, describing what was not
written and why.

### #4 — log every explicit `--force`

The exemption in `cmd_next.go` exists to keep `events.jsonl` — a tracked file —
from being rewritten on every `takt next` a session without
`CLAUDE_CODE_SESSION_ID`/`TAKT_SESSION` makes. That argument covers the automatic
paths (`orphaned`, and `outcome == LockStolen` for an idle generated holder). It
does not cover `--force`: `r.force` is set only from the command line, and the
only thing that tells a user to pass it is the `owner` gate's `takeover` choice.
Nothing in `commands/takt.md` or `hosts/copilot/skills/takt/SKILL.md` passes it
automatically.

**Decision: grade `r.force` ahead of the exemption.** An explicit `--force`
always appends a `lock_taken`, whatever the holder's kind and whatever `Acquire`
graded the outcome. The silent generated-over-generated takeover stays silent when
`--force` was not passed.

#2 then states both halves in §4.6: a `lock_taken` is recorded when a **named**
session takes over, and whenever a takeover was **explicitly forced**; a generated
session taking over a generated holder on its own records nothing.

## Tasks

Seven tasks. `internal/cli/cmd_next.go` and `internal/cli/cmd_init.go` are each
wanted by three separate issues, so the grouping is by file rather than by issue —
otherwise the obvious per-issue split would put three tasks in one file and no
wave could run them in parallel.

### T1 — the version handshake (#3, #10, #11)

`internal/cli/cmd_version.go`, `internal/cli/cmd_version_test.go`.

- Route a literal-empty `--expect` through the same refusal a whitespace-only one
  gets ("the host's handshake names no version"). Detect that the flag was
  *given* — `flag.Visit`, or a sentinel default — rather than comparing to `""`,
  so a host whose stamp came out empty fails the handshake instead of passing it.
  `--expect-manifest` keeps its current empty-means-absent dispatch.
- Collapse the double evaluation: one helper that judges once and returns the
  problem, the hint and `dev` together. `ManifestMatches` stays exported — its
  tests read it directly.
- Give the failure a subject noun. `--expect` is the Copilot skill's handshake:
  "does not match skill version" / "update the skill". `--expect-manifest` keeps
  "plugin". The empty-manifest failure keeps naming the manifest path.

Verify: `go test ./internal/cli -run 'TestVersion|TestManifest' -count=1`.

### T2 — the init/next write path (#5, #6, #12, and #15's `writeLogsIgnore` case)

`internal/bundle`, `internal/gitx`, `internal/cli/cmd_init.go`,
`internal/cli/cmd_next.go` and their tests.

- **Atomic writes.** Add `bundle.WriteFileAtomic(path string, data []byte) error`
  beside `WriteJSONAtomic`, with the same temp-then-rename shape and the same
  permissions. Use it for the brief writer and the slice-diff writer in
  `cmd_next.go` and for `writeLogsIgnore` in `cmd_init.go`. Spec §13's "every
  bundle write is atomic" then holds for the files an agent is handed.
- **Escaping.** Add a gitignore-pattern escaper in `internal/gitx` — backslash the
  metacharacters `*`, `?`, `[`, and the leading indicators `#` and `!`, and escape
  a trailing space, which git otherwise strips — and have `excludeLogsDir` build both rules through it.
  `EnsureExclude` keeps taking patterns verbatim; its doc comment already says
  escaping is the caller's business, and that contract is unchanged. Test with a
  `--dir` such as `docs/[takt]`.
- **Degradation.** `excludeLogsDir`'s failure stops failing `init` and `next`. Both
  report it in the JSON they already print — the field named in the #16 ruling —
  and carry on. The tracked `logs/.gitignore` is what protects a commit and a
  clone; `info/exclude` only keeps the sidecar invisible from another branch, and
  losing it is a cosmetic loss, not a broken run. `init`'s rollback must not run
  for this.
- **The test gap.** `writeLogsIgnore` compares before writing; add the
  already-present case beside `TestNextRestoresADeletedLogsIgnore`, asserting the
  file is not rewritten (mtime or a write counter, whichever the test helpers make
  honest).

Verify: `go test ./internal/gitx ./internal/bundle ./internal/cli -count=1`.

### T3 — `lock_taken` on an explicit `--force` (#4, #2)

`internal/cli/cmd_next.go`, `internal/cli/cmd_next_test.go`,
`docs/superpowers/specs/2026-08-24-takt-design.md`.

Depends on T2 — same file.

- Grade `r.force` ahead of `case orphaned && r.genID`. The appended event carries
  the outcome `Acquire` returned, so a `--force` over a long-idle holder still
  reads `stolen`; what changes is that it is recorded at all.
- Keep the generated-over-generated silence for every path that is not an explicit
  `--force`, and keep the comment explaining why — it is the reason the exemption
  exists and it is still correct for the automatic case.
- Rewrite §4.6's sentence to state both halves, per the ruling above.
- A test that an explicit `--force` from a generated session over a generated
  holder appends exactly one `lock_taken`, and its sibling that a plain `next` in
  the same situation appends none.

Verify: `go test ./internal/cli -run 'TestNext|TestLock' -count=1`.

### T4 — the planner brief quotes its rejections (#1)

`internal/brief/templates/planner.md`, `internal/brief/brief_test.go`.

The problems the template renders are agent-authored strings: `plan/validate.go`
builds `unknown class %q`, `%q not found on PATH`, and task titles and file paths
out of the index the planner itself wrote. Handing them back unquoted is the
injection the delimiter token exists to close.

Apply the shape the other three templates use — the heading and the retry sentence
outside the quote, `{{quote .Token "rejection" (join .Problems "\n")}}` inside —
and extend `assertRejectionSection` to cover the planner, so the rejection section
sits ahead of every other quoted artifact and names every problem inside the
delimiter pair.

Verify: `go test ./internal/brief -count=1`.

### T5 — hostgen error messages (#14)

`internal/hosts/copilot.go` and its tests.

One error style for the two adjacent failures, and thread the source path through
so a message cannot name `agents/<x>.md` when `hostgen --root` read the file from
somewhere else.

Verify: `go test ./internal/hosts ./internal/prompt -count=1`.

### T6 — `task build` stamps the version (#13)

`Taskfile.yml`, `internal/tools/setversion`.

`setversion` gains a way to print the version it would write — it already parses
`.claude-plugin/plugin.json` with `versionLine`, so the reader and the writer stay
one implementation and `task build` gains no new dependency. `Taskfile.yml`'s
`build` reads it into a var and passes
`-ldflags "-X github.com/monrad/takt/internal/version.Version=<v>"`.

`go build` and `go install` keep reporting `0.0.0-dev` — that is what the dev
exception in the handshake is for. Only `task build` stamps.

Verify: `task build && ./takt version --expect "$(go run ./internal/tools/setversion --print)"` exits 0, plus `go test ./internal/tools/... -count=1`. `/takt` is gitignored, so the built binary does not dirty the tree.

### T7 — the remaining test gaps (#15)

`internal/bundle/lock_test.go`, `internal/prompt`.

- The steal boundary in `bundle.Acquire`: `now - ttl` blocked, `now - ttl - 1ns`
  stolen, and "mine but stale" — `held.ID == who.ID` beats staleness and returns
  `LockHeldBySelf`.
- A cross-prompt sentence test: the invariants that must read the same in
  `commands/takt.md` and `hosts/copilot/skills/takt/SKILL.md` — the `owner`-gate
  exception, the `kept: true` rule, the `git add -A` prohibition — so a drift in
  one host is caught. `TestPromptHandshakeVerbsAndInvariants` loads only the
  Claude prompt today.
- `quotedScalarProblem`'s escape look-back reads one byte, so a double-quoted body
  ending `\\"` is a false negative. Count the run of preceding backslashes; an even
  count means the quote is not escaped.

Verify: `go test ./internal/bundle ./internal/prompt -count=1`.

## Waves

Wave 1 runs T1, T2, T4, T5, T6 — five file-disjoint tasks. Wave 2 runs T3 and T7.

T3 waits on T2 because both edit `cmd_next.go`. T7 waits for a subtler reason:
every implementer of a wave works in the same worktree, and file-disjointness is
not package-disjointness. T7 edits `internal/bundle/lock_test.go` while T2 adds
`WriteFileAtomic` to `internal/bundle`, and T7 edits `internal/prompt` while T5's
verify runs it. Different files, but a shared compile — so their verifies would
race. Wave 2 costs nothing here and removes both hazards.

## Testing

Each task carries its own package tests as its verify. The run as a whole is green
when `go test -race ./...` and `golangci-lint run ./...` both pass. No item here
changes an op, a gate or a JSON key any host prompt parses, with two exceptions
that add keys and remove none: the #6/#16 degradation field, and T1's failure
wording (prose in `error`/`hint`, which no host parses).

## Assumptions & Open Decisions

| question | decision | rationale | source |
|---|---|---|---|
| #16: keep the swallow documented, fail loud, or report it? | Report it in the command's JSON, exit 0 | Every call site runs after the substantive write; a non-zero exit would halt the loop on work already on disk, and the host prompt stops on non-zero exits | user-confirmed |
| #4: log every `--force`, or document the silence? | Log whenever `r.force` is set, ahead of the generated-holder exemption | `r.force` is only ever set from the command line, so the churn argument that justifies the exemption does not reach it | user-confirmed |
| #13: how does `task build` learn the version? | A print mode on `internal/tools/setversion`, read into a Taskfile var | Same Go parser writes and reads the manifest; no `jq` dependency, which the repo currently does not have | user-confirmed |
| Is #9 in scope? | No — closed as already fixed | `config.Validate` rejects all three durations by name and `TestValidateRejectsNonPositiveDurations` pins it | user-confirmed |
| Does #6 also add the `takt doctor` WARN its issue suggests? | No | `doctor.Input` carries `RepoRoot` but no git common-dir handle, and `info/exclude` lives in the common dir; plumbing one through is a change beyond a minors sweep. The issue says "consider", and the JSON field is the part that closes the failure | assumed |
| Do #5's atomic writes extend to every `os.WriteFile` in `internal/cli`? | No — briefs, the slice diff and `logs/.gitignore` only | Those are the files spec §13 covers and that an agent is handed. `cmd_review.go` and `archive.go` are named by other issues (#51) and belong to those | assumed |
| Where does the gitignore escaper live? | `internal/gitx`, called by `excludeLogsDir` | Escaping is gitignore knowledge, so it belongs beside `EnsureExclude` — but `EnsureExclude`'s documented "a rule is written exactly as given" contract stays, because a caller passing an already-escaped pattern must not have it escaped twice | assumed |
| Does T1 change `--expect-manifest`'s empty dispatch too? | No | An absent `--expect-manifest` and an empty one are the same thing: no manifest to read. `--expect` is different because an empty stamp is a broken host prompt, which is what the refusal says | assumed |
| Does the sweep close the issues it fixes? | Not automatically | Closing GitHub issues is outward-facing and belongs to the branch's finish, not to a task's diff | assumed |
