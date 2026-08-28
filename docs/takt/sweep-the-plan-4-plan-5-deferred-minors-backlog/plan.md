# Plan — sweep the plan-4 / plan-5 deferred minors backlog

## Approach

Eight tasks, grouped by file rather than by issue, exactly as the spec lays them
out: `internal/cli/cmd_next.go` and `internal/cli/cmd_init.go` are each wanted by
three separate issues, so a per-issue split would serialise the whole sweep on
two files. Wave 1 runs six file-disjoint tasks (T1, T2, T4, T5, T6, T7); wave 2
runs the two that must wait — T3 shares `cmd_next.go` with T2, and T8 uses the
`warnings` contract T2 defines. Waves are computed by takt from `depends_on`;
the file sets of tasks without an ordering edge are disjoint, which is takt's own
`plan-disjoint` rule.

Every task carries a cheap tripwire verify (a `grep` for a symbol, test name or
string the task introduces) that fails on the current tree, plus the package
tests the spec names. The two wave-2 tasks additionally carry the repo-wide
gates — `go test -race ./...` and `golangci-lint run ./...` — so the assembled
branch is proven green by the last tasks to touch Go (G13).

All fixes were verified against the tree at `bd221d9` during planning: line
numbers cited in task descriptions are where the code stands today.

## Tasks

### T1 — the version handshake (#3, #10, #11) — `bounded`

`cmd_version.go` dispatches on `*expect != ""` (line 22/26), so a literal-empty
`--expect` falls through to the plain print and exits 0 — the one hole in the
handshake. The task detects that the flag was given (`flag.Visit` or a sentinel
default), routes empty through the existing versionless refusal, collapses the
`manifestFailure` + second `ManifestMatches` double evaluation into one helper
returning problem, hint and `dev` together, and gives `--expect`'s failure its
own subject noun ("skill" — that flag is the Copilot skill's handshake;
`--expect-manifest` keeps "plugin"). Scoped to the one file and its test file;
`ManifestMatches` stays exported because `TestManifestMatches` reads it.
Class `bounded`: the spec fully specifies each of the three changes, the failure
wording, and the tests to add — there is no open design decision.

### T2 — the init/next write path (#5, #6, #12, #15's writeLogsIgnore case) — `implement`

The largest task, and deliberately so: it owns every file the degradation,
atomicity and escaping fixes touch, so no other wave-1 task can collide with it.
Four things land together because they share those files. (1) The `warnings`
contract: `Warnings []string \`json:"warnings,omitempty"\`` on `op.Op`, a
`keyWarnings` constant in `cli.go`, and a `warnings` key on `init`'s output map —
absent when empty, never an error channel, no exit-code change. (2)
`bundle.WriteFileAtomic(path string, data []byte) error` beside
`WriteJSONAtomic`, same temp-then-rename shape and permissions, used at the four
call sites an agent is handed a file from: `writeStableBriefAt` (cmd_next.go:616)
and `ensureSliceDiff` (cmd_next.go:672), `renderTaskBrief` (launch.go:304), and
`writeLogsIgnore` (cmd_init.go:351). (3) A gitignore-pattern escaper in
`internal/gitx`, backslash first, then `*` `?` `[`, leading `#`/`!`, trailing
space; `excludeLogsDir` builds both rules through it while `EnsureExclude`'s
written-exactly-as-given contract is unchanged. The escaper gets a unit test, and
`init` gets cli-level cases for *both* shapes the spec names — a `--dir` holding a
glob metacharacter (`docs/[takt]`) and one holding a literal backslash — since
only those prove path handling and rule construction together, which is the
pairing the whole fix exists for. (4) Degradation: `excludeLogsDir`
failure stops failing `init` (no rollback) and `next`; both report it as a
`warnings` entry and carry on, because the tracked `logs/.gitignore` is what
protects commits and clones. The writeLogsIgnore already-present test closes
that slice of #15. Twelve files — at the cap, which is the argument for not
splitting further: any split would put two wave-1 tasks in the same file.

### Bookkeeping outside the task graph

Two acts in this run are not tasks and produce no diff, because closing a GitHub
issue is outward-facing and does not belong in an implementer's commit: issue #19
(fixed by PR #22, never closed) and issue #9 (fixed by `config.Validate` and
pinned by `TestValidateRejectsNonPositiveDurations`) are closed by the driving
session. G12 — the anchor's fourteen listed issues resolving to eleven fixes, two
rulings and one closure — is evidenced by spec.md's "#9 is already fixed" section
and its Scope list, both already in the bundle. No task edits them, and T3 carries
the goal only as its recorded owner.

### T3 — `lock_taken` on an explicit `--force` (#4, #2, G12) — `implement`

Depends on T2 (both edit `cmd_next.go` and its test file). The event switch in
`acquireLock` (cmd_next.go:161-171) is gated on a takeover having happened —
`outcome` is `stolen` or `forced` — and the generated-over-generated exemption
is conditioned on `--force` not being passed, so an explicit forced takeover
always appends one `lock_taken` carrying the outcome `Acquire` graded, while a
plain `next` between two generated sessions stays silent and a `--force` against
a free lock or the caller's own appends nothing. §4.6 of the design doc is
rewritten to state all three parts. This task also carries G12: the goal's
evidence is the spec's own "#9 is already fixed" and Scope sections, which are
already recorded in this bundle and need no diff of their own — T3 is the task
that lands the sweep's other ruling-derived documentation change, and its wave
commit carries the bundle's spec.md into history, so the arithmetic the anchor
states loosely is evidenced there rather than left implicit.

### T4 — the planner brief quotes its rejections (#1) — `bounded`

`planner.md:4` renders `{{range .Problems}}- {{.}}` as bare text; the problems
are agent-authored strings (task titles, file paths, `%q` fragments from
`plan/validate.go`), which is the injection the delimiter token exists to close.
Apply the shape the other three templates use — heading and retry sentence
outside, `{{quote .Token "rejection" (join .Problems "\n")}}` inside — and
extend `assertRejectionSection` to the planner. Class `bounded`: the target
shape is quoted verbatim in the spec and the assertion helper already exists;
the work is transplanting a known pattern.

### T5 — hostgen error messages (#14) — `bounded`

`RenderCopilotAgent` gains the source path as a parameter, its one caller
(`render` in hostgen's main.go:111-117) passes the `src` it already resolved
under `--root`, and both failures — the frontmatter error (copilot.go:33) and
the missing-description error (copilot.go:37) — use one error style naming that
path, so a message can no longer claim `agents/<x>.md` when `--root` read the
file from somewhere else. Class `bounded`: the signature change, the single
caller and the two messages are all named by the spec; the `--root` test case
is specified in G9's evidence.

### T6 — `task build` stamps the version (#13) — `bounded`

`setversion` gains `--print`, which parses `.claude-plugin/plugin.json` with the
existing `versionLine` regexp and prints the version without writing anything —
reader and writer stay one implementation, no new dependency. `Taskfile.yml`'s
`build` reads it into a var and runs `go build -ldflags "-X
github.com/monrad/takt/internal/version.Version=<v>" -o takt ./cmd/takt` (an
output and a main package are required: `go build ./...` over many packages
emits no binary), keeping a plain `go build ./...` beside it as a compile check.
`/takt` is already gitignored, so the built binary never dirties the tree; local
`go build` / `go test` keep reporting `0.0.0-dev`, which the handshake's dev
exception exists for.

Both binary checks remove `./takt` before building. It is gitignored and may
already exist from a hand-run `go build ./cmd/takt`, so a stale artifact could
otherwise satisfy them while the build task still emitted nothing.

That exception is also why the verify cannot use `takt version --expect`:
`ManifestMatches` returns `(true, true)` for a `0.0.0-dev` binary, so an
unstamped build satisfies *any* expectation and the check could never fail — it
would mark the goal achieved whether or not the ldflags landed. The stamp is
proved directly instead: `./takt version` must print the manifest's version, and
must not print `0.0.0-dev`. Class `bounded`: mechanism, flag name, ldflags string
and verify are all fixed by a user-confirmed decision.

### T7 — the remaining test gaps (#15) — `test`

Three pins against existing code, no production change. The `Acquire` steal
boundary (`now.Sub(heartbeat) > ttl` means exactly-ttl blocks, ttl+1ns steals)
and "mine but stale" (`held.ID == who.ID` is graded before staleness, so
`LockHeldBySelf` wins) go into `lock_test.go`. A cross-prompt test asserts the
invariant sentences that must read the same in `commands/takt.md` and
`hosts/copilot/skills/takt/SKILL.md` — the `owner`-gate exception, the
`kept: true` rule, the `git add -A` prohibition — so a drift in one host fails
the suite; today `TestPromptHandshakeVerbsAndInvariants` loads only the Claude
prompt. And `quotedScalarProblem`'s one-byte escape look-back
(copilot_test.go:226) is replaced by counting the run of preceding backslashes —
even means unescaped — with a table test proving a body ending `\\"` is now
caught. Class `test` by definition: it verifies behaviour that already exists
(and fixes a helper that lives inside a test file).

### T8 — `endAttemptStreak` reports what it loses (#16) — `implement`

Depends on T2 for the `warnings` contract; file-disjoint from it and from T3.
`endAttemptStreak` (facts.go:256-263) returns an `error` instead of discarding
the `ReadEvents` and `AppendEvent` failures, and each of its four callers —
cmd_record.go:174 (goals), cmd_record.go:261 (alignment),
record_reviewer.go:134 (lens), record_reviewer.go:258 (verify) — folds a failure
into the `warnings` array of the JSON it already prints. Exit codes and existing
keys are unchanged: every call site runs after the substantive write has landed,
so failing loud would halt the host loop on work already on disk, which is the
ruling the spec records. The test seeds a rejection streak, forces the append to
fail, and asserts exit 0, intact keys and the named loss.

## Risks

- **Same-worktree wave concurrency.** T1, T2 and T7 all compile `internal/cli`
  or `internal/bundle` in wave 1, and every implementer shares the worktree, so
  a task's verify can transiently observe another task's half-written edit. The
  cost is a spurious verify failure that takt re-attempts; it cannot grade a
  wrong result, because the wave is graded on the committed tree. This is the
  spec's accepted residual risk, adopted rather than re-mitigated.
- **T2's size.** Twelve files is the cap. The mitigation is that its four
  sub-changes are mechanically related (they share the same call sites) and each
  has its own tripwire verify, so a partial landing is caught by the verify set
  rather than by review alone.
- **`task build` in T6's verify.** The verify needs `go-task` on PATH; it is in
  the repository's devShell and `Taskfile.yml` is the repo's own entry point, and
  the spec fixes this verify by a user-confirmed decision. The handshake half is
  meaningful only after the build because a stale unstamped `./takt` would
  dev-pass — the fail-before property comes from `task build` currently emitting
  no binary at all, so `./takt` does not exist.
- **Prose tripwires.** T3's doc verify greps for "explicitly forced", a phrase
  the ruling itself uses and the design doc does not contain today. It is a
  tripwire against the edit landing in the wrong place, not an oracle for the
  sentence's meaning — the wave review and G6's assessment judge the prose.

## Class justifications (below `implement`)

- **T1, T4, T5, T6 `bounded`** — each is small, confined to files the spec
  names, and fully specified down to the failure wording (T1), the template
  line (T4), the signature and caller (T5) and the exact ldflags string (T6);
  the tests to add are named in the spec or the goals' evidence.
- **T7 `test`** — tests against existing code only; the single code change is
  to a helper that itself lives in a `_test.go` file.
