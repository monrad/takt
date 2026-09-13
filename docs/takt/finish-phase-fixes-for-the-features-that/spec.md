# Finish-phase fixes: retro rows, PR closing keywords, the push invariant and stale archive prose

Four features shipped in the last three runs and each left a defect the first real use exposed.
The retro skeleton renders a dash where a waived wave's commit should name its tasks, and counts a
re-closed wave twice. A pull request opened from a takt run never closes the issue the run was
started from. Both host prompts still forbid the very push an `archived` stop now hands the session.
And three comments plus two design-doc paragraphs describe code that #63 replaced. This run fixes
all four, and — because fixing the third one honestly means making the copilot prompt a generated
file rather than a hand-kept copy — adds the generator that makes prompt drift impossible.

## 1. Problem

### 1.1 #71 — the retro skeleton's first real render

`docs/takt/lets-work-on-69/retro.md` is the first retrospective rendered from a real run rather
than from a test fixture. Two rows of it are wrong.

**(a) `## What shipped` shows `—` for a waived wave's tasks.** The row is built from the
`wave_committed` event, and that event's task list is empty for any wave whose committing close
followed a waive at `wave_failures`:

- The first close of the wave grades the dispatched attempt, finds failures, does not commit
  (`res.Committed = sliceDone(...)` is false) and raises `wave_failures`.
- The user waives the failing tasks; `takt next` closes the wave again.
- That second close grades nothing, so `gradedIDs(res.Tasks)` — read at
  `internal/cli/cmd_close_wave.go:169`, deliberately *before* `persistClose`'s `carryForward`
  merges the retired round's results back in — is empty.
- Every task of the slice is now `waived`, not `done`, and `doneWaveFiles`
  (`cmd_close_wave.go:627`) only collects ids for `StatusDone`, so `mine := inSlice(...)` is empty
  too.
- `commitWaveOnce` therefore returns `ids == nil` (`cmd_close_wave.go:544`), and
  `recordCloseOutcome` writes `wave_committed` with `tasks: []`.

`BuildShipped` faithfully renders what the event says, and `tasksCell` renders an empty list as
`—`. The commit is real, it carries real tasks, and the table refuses to name them.

**(b) `## Numbers` lists a re-closed wave twice.** `waveTimings`
(`internal/finish/retro.go:314`) emits one span per `wave_closed` event. A waive does not bump the
attempt, so both closes of a waived wave carry the same `(wave, slice, attempt)` key and the table
gets two rows for one dispatch — the failed close and the committing one, indistinguishable to a
reader and double-counted in any span arithmetic.

Neither defect is covered: none of the seven golden documents in
`internal/finish/skeleton_test.go` is built from a waived-then-re-closed wave.

### 1.2 #74 — a run started from an issue never closes it

`finish.BuildPR` writes a title, the spec's opening paragraph, `## Goals` and `## Run`. It writes
no closing keyword. Every takt run started from a GitHub issue therefore opens a pull request that
merges without closing the issue it was started to fix — the issue has to be closed by hand
afterwards, and in practice is not.

### 1.3 #66 — the prompts forbid a push the binary now asks for

Both host prompts carry this invariant:

> Never commit or push except where an op says so (`push_pr`); never run `git add -A`; never
> delete or check out branches — the `archived` stop lists what is left for you as `cleanup`.

Since the #60/#62 run, `prCleanup` (`internal/cli/archive.go`) makes an `archived` stop's
`cleanup` list carry `git push origin <branch>` / `git push -u origin <branch>` for the `pr`
disposition — a push the op table's own `stop` bullet tells the session to run once the user
confirms. Inside one file, the Invariants section now forbids what the op table instructs. The
same contradiction sits in the branch-deletion clause: the discard hand-off is literally
`git checkout <base> && git branch -D <branch>`.

The lens that found this (`docs/takt/lets-work-on-60-and-62/waves/0/lens-docs.s1.a1.json`) also
noted that the sentence is duplicated verbatim in the copilot skill and is *not* among the
`crossHostInvariants` anchors, so the parity test would not catch the two copies disagreeing.

That duplication is the deeper problem. `commands/takt.md` and
`hosts/copilot/skills/takt/SKILL.md` are two hand-maintained files describing one contract. They
differ in 11 regions out of 53 lines; the other 42 lines are byte-identical and kept that way by
nothing but care and a seven-entry anchor list. `task hosts:gen` does not regenerate the copilot
skill: `internal/tools/hostgen` renders only `hosts/copilot/agents/*.agent.md` from `agents/*.md`.

### 1.4 #72 — three comments and two design paragraphs describe deleted code

#63 changed the archived path and left its prose behind.

- `applyAndStop`'s doc comment (`internal/cli/archive.go:133`) ends: *"The later call on an
  archived run takes no lock, so it passes plainOp."* `plainOp` was deleted; the archived path in
  `cmdNext` now runs after `acquireLock` and passes `r.emit` — precisely so a `takt retro
  --rewrite` and a concurrent `next` cannot interleave over the retro pair
  (`internal/cli/cmd_next.go:91`).
- The same comment opens *"It writes nothing tracked: the archive commit is the run's last one"*.
- Design §7.5 step 5 says *"That commit is the run's last one, which is what lets a merge carry the
  archived bundle"*, repeats the claim in quotes further down (*"…is unaffected: the push is a
  cleanup command, not a commit"*), and states *"there is no `disposition_applied` event and no
  write after the archive commit"*. But `done --step retro` accepts the archived phase
  (`doneRetroChecks`, `internal/cli/cmd_done.go:207`) and ends in
  `commitBundle(..., "retro done")` — a `takt(<slug>): retro done` commit after the archive commit,
  which `doneRetro`'s own comment already claims step 5 "contemplates". It does not.
- Design §5.1's row for `takt retro --rewrite` says it *"takes the session lock (§4.6) as `next`
  does"*. It takes the lock, but not with `next`'s behaviour: `cmdRetro` installs a `lockBlocked`
  callback that fails the command outright, naming the holder and its heartbeat, because the
  command is not an op loop and has nothing to hand a question back to. §4.6 describes `next`
  returning `ask: owner`, so the row points a reader at the wrong contract.

## 2. What changes

### 2.1 `internal/finish` — the retro (#71)

**`BuildShipped` gains the close records.** The signature becomes
`BuildShipped(events []bundle.Event, closes []wave.CloseResult, idx plan.Index) []ShippedRow`.
When a `wave_committed` event carries a non-empty task list, nothing changes — that list is what
the commit recorded and it wins. When it is empty, the ids are derived, in this order:

1. The close record whose `(wave, slice, attempt)` equals the event's, taking **every** id in its
   `tasks` list, whatever status each result carries. After `carryForward` that record holds the
   whole slice's story across both rounds, and a committing close only happens when `sliceDone`
   holds — every task of the slice is `done` or `waived` *in `state.json`* — so every id in the
   record is a task whose files `commitWaveOnce` staged.

   **The statuses in the record must not be filtered on.** `takt waive` writes
   `state.Tasks[i].Status` and nothing else (`internal/cli/cmd_waive.go:48`); the
   `wave.TaskResult` in the close record keeps the last verdict a review round gave it, and
   `carryForward` copies it across unchanged. The `lets-work-on-69` bundle is the proof: the
   surviving record for wave 2 slice 1 is `attempt 3, committed: true, tasks: [{task: 3, status:
   "rework"}]` — a `done`/`waived` filter would discard precisely the task the row exists to name.
2. Failing that, the `wave_dispatched` event with the same `(wave, slice, attempt)` key, whose
   `tasks` list is the slice as it went out.
3. Failing both, today's `—`.

Ids are resolved to titles by the existing `shippedTasks` path, so an id the index does not know
still renders bare. `BuildShipped` stays pure — the records arrive as an argument.

The one caller, `internal/cli/retro.go:105`, already has `closes` in hand for `BuildRetroInputs`
and just passes it along.

**`waveTimings` de-duplicates by dispatch key.** One span per `(wave, slice, attempt)`; when two
`wave_closed` events share a key, the last one in log order wins — it is the close that describes
how the dispatch actually ended. A reworked wave still reports the thrown-away attempt and the
landed one, because those carry different attempt numbers, and a sliced wave still reports one span
per slice. The output stays sorted by wave, then slice, then attempt.

### 2.2 `internal/finish/pr.go` — the closing section (#74)

`BuildPR` gains an `## Issues` section, rendered between `## Goals` and `## Run`:

```markdown
## Issues

These are the issues this run set out to fix; `## Goals` above says which of them it proved.

Closes #66, closes #71, closes #72, closes #74
```

- The references are parsed from `state.Topic`, which `BuildPR` already receives as `topic`. Not
  from the slug: `deriveSlug` (`internal/cli/slug.go:22`) rewrites an issue-URL topic to
  `issue-<n>`, and every other topic loses its `#` to `nonSlug`.
- **Three token forms**, because a takt run is started from an issue in more than one way. They are
  one alternation, tried in this order, and each is rendered into the closing line *verbatim* —
  GitHub accepts all three after a closing keyword:

  | form | pattern | example |
  |---|---|---|
  | cross-repository | `[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#\d+` | `monrad/takt#71` |
  | issue URL | `https?://\S+?/issues/\d+` | `https://github.com/monrad/takt/issues/71` |
  | bare number | `#\d+`, not preceded by `/` or a word character, not followed by a word character | `#71` |

  The URL form is the one the review caught: `deriveSlug` supports `/issues/<n>` topics, so the
  canonical "started from an issue" run — the run §1.2 is about — has a topic with no `#N` in it at
  all. Requiring the `https?://` prefix keeps a bare `/issues/12` fragment, which is not a link,
  out of the closing line.
- **De-duplicated by rendered token**, in the order the topic names them. Two *different* forms are
  never merged: `BuildPR` is pure and knows neither the repository nor the host, so it cannot prove
  that `#71` and `https://github.com/monrad/takt/issues/71` are the same issue. A topic naming both
  emits both; GitHub resolves them to one issue and closes it once.
- The keyword is repeated per reference (`Closes #66, closes #71, …`). GitHub links only the first
  issue of a bare comma list, so one keyword for four issues closes one issue.
- When the topic names no issue at all, the whole section — heading, sentence and line — is
  omitted. A `## Issues` heading over nothing would read as an omission.
- When the run's goals are off (`gs == nil`, so `## Goals` is not rendered), the sentence drops its
  second clause and reads *"These are the issues this run set out to fix."*

Two limits, stated rather than engineered around. A topic that names only part of an issue
(`#49 item 1`) closes `#49` — the existing behaviour of a closing keyword, and out of scope here. And
a six-digit colour literal (`#123456`) is indistinguishable from an issue number in prose a person
wrote; the bare-number form does not try to tell them apart, it only refuses a token with a letter
stuck to it (`#71b`).

### 2.3 The prompts and their generator (#66)

**The invariant is reworded**, in `commands/takt.md`, so that the exception is the op rather than
one step:

> - Never commit, push, delete a branch or check one out on your own initiative — two ops say
>   otherwise, and only those: the `push_pr` run op, and an `archived` stop's `cleanup` commands
>   once the user has confirmed them; never run `git add -A`, ever.

The substring both `TestPromptHandshakeVerbsAndInvariants` and `crossHostInvariants` already anchor
on — ``never run `git add -A` `` — survives verbatim.

**`crossHostInvariants` gains the push clause** — the anchor
`` the `push_pr` run op, and an `archived` stop's `cleanup` commands once the user has confirmed them ``
— so the parity test covers the sentence that drifted.

**`hosts/copilot/skills/takt/SKILL.md` becomes generated.** `internal/hosts` gains a copilot
profile: an ordered list of exact `from → to` substitutions over `commands/takt.md`, covering the
11 regions that legitimately differ —

| # | region |
|---|---|
| 1 | the frontmatter block (`description:` → `name:` + a Copilot-worded `description:`) |
| 2 | the H1 (`# /takt — the op loop` → `# takt — the op loop (Copilot CLI host)`) |
| 3 | the handshake paragraph (`--expect-manifest "${CLAUDE_PLUGIN_ROOT}/…"` → `--expect <version>`) |
| 4 | the three `/takt …` verb bullets → their `"takt …"` phrasings |
| 5–6 | `AskUserQuestion` → `ask_user`, in the slug-ambiguity bullet and the autonomy paragraph |
| 7 | the `dispatch` bullet (Agent tool → `takt-<agent>` custom agents, advisory model) |
| 8 | the `ask` bullet's opening clause |
| 9 | the `run` bullet's `brainstorm` clause (`superpowers:brainstorming` → design in-conversation) |
| 10 | the `exec` bullet's opening clause (Bash tool, background → shell tool) |
| 11 | the delegation invariant (`Every `Agent` call carries the `model`…` → `Every delegation targets…`) |

`hostgen` then:

- reads `commands/takt.md`, applies the profile in order, and writes
  `hosts/copilot/skills/takt/SKILL.md`;
- **errors** when a substitution does not match its declared count. Every substitution carries an
  expected multiplicity — `1` for all but the `AskUserQuestion` → `ask_user` swap, which declares
  `2` — and a `from` found a different number of times stops the generator, naming the substitution
  and both counts. That is the whole safety property, and it has one contract, not two: a reworded
  *shared* sentence propagates silently and correctly, while a reworded *host-specific* sentence
  fails the build by name instead of drifting;
- injects the version from `.claude-plugin/plugin.json` into `takt version --expect <v>`, which
  `TestCopilotSkillHandshakeMatchesTheManifest` requires today and a human has had to remember on
  every bump;
- reports the skill file as stale under `--check` exactly as it does for the agents, so
  `task hosts:check` and CI cover it.

Everything the profile does not replace is identical by construction. That is the point: 42 of 53
lines can no longer drift.

### 2.4 The stale prose (#72)

**`internal/cli/archive.go`**, `applyAndStop`'s doc comment:

- the closing sentence stops naming `plainOp` and says what is true — every caller, `archive()` and
  every later `takt next` on the archived run alike, reaches here holding the lock and prints
  through the caller's `emit`;
- the opening claim becomes what it actually needs: the archive commit leaves the tree clean for
  every choice, which is what makes the discard hand-off a command the session can run. It no
  longer claims to be the run's last commit.

**Design §7.5 step 5:**

- *"That commit is the run's last one"* becomes *the last commit the archive step takes* — which is
  still what lets a merge carry the archived bundle, since the git side of the disposition happens
  only after it;
- the paragraph names the one write that can follow: a post-archive `takt retro --rewrite` plus
  `done --step retro` lands a `takt(<slug>): retro done` bundle commit on the branch, which is why
  `doneRetroChecks` accepts the archived phase;
- the later sentence that quotes the old claim (*"…is unaffected: the push is a cleanup command,
  not a commit"*) is restated against the new one;
- *"there is no `disposition_applied` event and no write after the archive commit"* is narrowed to
  what it means: the *disposition* is never recorded in state and the archive step writes nothing
  after its commit.

**`internal/cli/cmd_done.go`**, `doneRetro`'s comment: its citation *"(design §7.5 step 5 already
contemplates post-archive bundle writes)"* is repointed at the sentence that now actually says so.

**Design §5.1**, the `takt retro --rewrite` row: it takes the session lock as `next` does, *except*
that a live holder fails the command outright — naming the holder and its heartbeat, with a hint to
`takt unlock` — rather than returning `ask: owner` as §4.6 describes for `next`. The command has one
thing to do and no next call to answer a question.

## 3. What already works unchanged

- The `wave_committed` emitters. #71(a) is fixed where the retro is *rendered*, not where the event
  is written, so the bundles already on disk — including the `lets-work-on-69` run whose retro
  exposed this — render correctly on the next `takt retro --rewrite`. `commitWaveOnce`,
  `recordCloseOutcome` and `backfillCommitSHA` are not touched.
- Commit subjects. `waveSubject` already falls through to the wave's waiver list, so a waived
  wave's commit says `waived 3, 4` rather than trailing off. Only the event's task list was empty.
- Every existing golden document, and the six other `BuildShipped` / `RenderSkeleton` tests.
- `BuildPR`'s title, opening paragraph, `## Goals` and `## Run` sections, and `goalOutcome`'s
  waiver-wins rule.
- `hosts/copilot/agents/*.agent.md` and their rendering from `agents/*.md`; `--check`'s orphan
  sweep; `CopilotAgentName`.
- The archived path's behaviour. #72 is prose only — not one line of executable code changes.

## 4. What the user sees

- A retro whose `## What shipped` names the tasks of a waived wave's commit, and whose `## Numbers`
  has one row per dispatch.
- A pull request that closes its issues on merge.
- Two prompts that no longer forbid the cleanup push, and a copilot skill that can no longer drift
  from the Claude one — `task hosts:gen` regenerates it, `task hosts:check` fails when it is stale.
- Comments and a design document that describe the code as it is.

## 5. Testing

- **#71:** an eighth golden in `internal/finish/skeleton_test.go`, built from a wave that was
  dispatched, failed, waived and re-closed — its `RetroInputs` produced by `finish.BuildRetroInputs`
  and its extras by `finish.BuildShipped`, from that event log and close record, rather than
  hand-written. The golden then proves both halves end to end: the `## What shipped` row names the
  waived wave's tasks and `## Numbers` carries one span for the wave. The golden's close record
  models what the close path actually persists — the waived task carried at its pre-waiver
  `rework` status — so a `done`/`waived` filter reintroduced later fails it. Plus focused tests:
  the three-step derivation order (event list wins · close record · dispatch event · `—`), a close
  record whose task results are all non-`done` still naming its tasks, and `waveTimings` collapsing
  two closes of one key while keeping two attempts and two slices apart.
- **#74:** a table-driven test over a topic naming no issue, one, several, `#49 item 1`, an issue
  URL, a cross-repository `owner/repo#N`, a mix of forms, and a repeat of one form — asserting in
  every case that the count of the closing keyword equals the count of references rendered (the
  assertion that fails if the keyword is ever emitted once for a comma list), that each reference
  appears verbatim, and that the order is the topic's. Plus the absence of the whole section when
  there is none, the sentence's goals-off form, and negative cases: `#71b` and a bare `/issues/12`
  fragment are not references.
- **#66:** the generator's own tests — a substitution that matches zero times is an error naming it
  and both counts; one that matches twice where it declares one is the same error; the repeated
  substitution matching its declared two is not; a full render of `commands/takt.md` equals the
  committed `SKILL.md`; `--check` reports the skill stale. Plus
  `TestPromptInvariantsReadTheSameOnEveryHost` with the new anchor, and the existing copilot suite
  (`TestCopilotSkillNamesEverythingTheBinaryCanEmit`,
  `TestCopilotSkillHandshakeMatchesTheManifest`, `TestCopilotHostFrontmatterIsParseable`) passing
  against the generated file.
- **#72:** no behavioural test — the change is comments and prose. Verified by `go build ./...`,
  `go vet ./...` and greps that the deleted identifier `plainOp` is named nowhere and that each
  amended paragraph carries its new sentence.
- The whole suite: `task test` (`go test -race -count=1 ./...`), `task lint`, `task hosts:check`.

## 6. Out of scope

- #67 and #68 — review-loop mechanics; their own run.
- #57 / #58.
- Any change to what `BuildPR` does with a topic that names only part of an issue, beyond the one
  sentence in §2.2.
- Making `commands/takt.md` itself generated (shape B, considered and rejected in §7): the shipped
  Claude prompt stays hand-written and authoritative.
- Fixing the empty `tasks` list at the `wave_committed` emitter. The event is an honest record of
  what that close round graded; the retro is where the question "what did this commit carry?" is
  asked, and answering it there also repairs the bundles already written.

## 7. Assumptions & Open Decisions

| question | decision | rationale | source |
|---|---|---|---|
| Fix #71(a) where the event is written or where the retro is rendered? | Rendered — in `BuildShipped` | Repairs bundles already on disk, including the run whose retro exposed this; keeps #71 inside `internal/finish` | assumed |
| What does an empty `wave_committed` task list fall back to? | Every task id in the close record for the same `(wave, slice, attempt)`, unfiltered by status; then the `wave_dispatched` event with that key; then `—` | The issue asks for "the close record or the previous attempt's dispatch, whichever the code can prove"; the close record is the fuller proof, the dispatch event the fallback when no record survives | user-confirmed |
| Should the close record's task results be filtered by status? | No | `takt waive` writes only `state.Tasks[i].Status`, so a waived task keeps its `failed`/`blocked`/`rework` verdict in the record — a `done`/`waived` filter would discard exactly the tasks the row exists to name (spec review, blocking) | assumed |
| Should a derived row be marked as derived in the table? | No | The row states what the commit carried, which is true however it was derived; a marker would be noise in a document a human rewrites | assumed |
| What is a `## Numbers` span keyed on? | `(wave, slice, attempt)`, last `wave_closed` in log order wins | Stated in #71; keeps a reworked wave's two attempts and a sliced wave's two slices apart while collapsing a waived wave's two closes | user-confirmed |
| Where does the eighth golden live? | `internal/finish/skeleton_test.go`, beside the other seven | The user asked for it there, and a golden outside the table it belongs to is not one | user-confirmed |
| Is the golden's input hand-written or derived? | Derived — through `BuildRetroInputs` and `BuildShipped` from the event log and close record | A hand-written `WaveTimings` would exercise the renderer and not the two functions being fixed | assumed |
| Where does the #74 section go, and does it carry a heading? | `## Issues`, between `## Goals` and `## Run` | Chosen by the user from three renderings; matches the body's other sections | user-confirmed |
| What forms does the issue parser accept? | Three, over `state.Topic`, in topic order: `owner/repo#N`, `https?://…/issues/N`, and a bare `#N` | `deriveSlug` supports `/issues/<n>` topics, so a run started from an issue URL has no `#N` at all — parsing only `#N` would omit the section for the very case §1.2 is about (spec review, blocking) | assumed |
| Are two forms naming the same issue merged? | No — de-duplication is by rendered token, within a form | `BuildPR` is pure and knows neither repository nor host, so it cannot prove `#71` and an issue URL are the same issue; GitHub closes it once either way | assumed |
| Does the bare-number form exclude colour literals? | No — only a token with a letter attached (`#71b`) | `#123456` is indistinguishable from an issue number in prose; claiming otherwise was wrong (spec review) | assumed |
| What happens when the topic names no issue? | The whole section is omitted | A heading over nothing reads as an omission, which is the failure mode the retro's "none" rule exists to avoid | assumed |
| What does the sentence say when the run's goals are off? | It drops the `## Goals` clause | The sentence must not point at a section the body does not render | assumed |
| Reword the invariant only, or make `SKILL.md` generated? | Generated, from `commands/takt.md`, via a substitution profile | Chosen by the user after the trade-off was put; it makes the original brief's "regenerate with `task hosts:gen`" true and ends the class of defect #66 is an instance of | user-confirmed |
| Which file is the source of truth? | `commands/takt.md` — shape A | It is what the plugin ships and what every existing test loads as authoritative; shape B would make it a build artifact for the same guarantee | user-confirmed |
| What does hostgen do with a substitution that does not match its count? | Errors, naming the substitution and both counts. Every substitution declares an expected multiplicity — 1 for all but the `AskUserQuestion` → `ask_user` swap, which declares 2 | This is the drift alarm: shared prose propagates silently, host-specific prose fails loudly. One contract, so the tests and the implementation cannot read it differently (spec review, minor) | assumed |
| Does hostgen inject the handshake version? | Yes, from `.claude-plugin/plugin.json` | `TestCopilotSkillHandshakeMatchesTheManifest` already requires the pin; generating it removes a manual step from every version bump | assumed |
| Does `crossHostInvariants` survive the generator? | Yes, plus the new push-clause anchor | It becomes the check that nobody turned a shared sentence into a substitution | user-confirmed |
| Does #72 also drop the "run's last commit" claim from `archive.go`, or only the `plainOp` sentence? | Both — the same staleness, applied everywhere it appears | The issue names it in the design doc; leaving the identical claim in the comment beside it would re-create the drift the run is closing | assumed |
| Does any executable code change for #72? | No | Every finding is a comment or a design paragraph; the code they describe is correct as it stands | assumed |
