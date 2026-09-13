# Retro — finish-phase-fixes-for-the-features-that

## What shipped

Four issues, in two waves. Wave 0 landed #71 (the retro's `## What shipped` derivation and
`waveTimings` de-duplication), #74 (`BuildPR`'s `## Issues` section) and #66, which grew during
brainstorming from a one-line reword into a generator: `hosts/copilot/skills/takt/SKILL.md` is now
rendered from `commands/takt.md` through an ordered substitution profile, so 42 of the two prompts'
53 lines can no longer drift. Wave 1 was #72's stale prose in `archive.go`, `cmd_done.go` and the
design doc; it took three attempts and ended in a waiver, with two of its fixes landing as a session
commit (`06c080e`) that the wave machinery could not carry. All nine goals were assessed achieved at
`caac6fb`, and the 41-command verify passed with none failing.

| wave | attempt | tasks | commit |
| --- | --- | --- | --- |
| 0 | 1 | 1 — BuildShipped derives a waived wave's tasks from the close record; waveTimings de-duplicates by dispatch key; 2 — BuildPR renders an ## Issues section with a closing keyword per reference; 3 — Reword the push invariant, anchor it across hosts, and generate the copilot skill from commands/takt.md | b87580d399a03ecd851c6404c0cccabd106a17d5 |
| 1 | 3 | 4 — Retire the stale archived-path prose in archive.go, cmd_done.go and the design doc; whole-branch gate | 3f131569c1f5f0d8061edbe2cdb4f64d093bb8d0 |

## Decisions

- task_waiver: task 4 (Two backend findings, neither fixable inside this task's remaining budget. (1) Scope: the backend flagged the design doc's §3.3/§6.1/§14 host-generation restatement as unrelated on all three attempts, because it judges against the recorded task description, which was written at the plan gate and predates the user's explicit authorisation of exactly those regions after wave 0's review confirmed them stale. The verifier examined the identical complaint from the intent lens and refuted it as a false positive. No attempt can satisfy it: keeping the edits draws the finding, reverting them undoes the user's decision and re-breaks documentation this run deliberately fixed. (2) A real one-clause defect attempt 3 surfaced: §7.5 step 5 still reads 'lock released; commit takt(<slug>): archive', while archive() reaches applyAndStop holding the lock and ClearSession runs only after commitBundle returns. It is a pre-existing error, not one this run introduced, and the user chose to fix it as a session commit at the finish phase rather than spend a fourth full review cycle on one clause. All six internal lenses were clean on this attempt; every verify command passes.)

disposition: not yet chosen

- spec_assumption: What does an empty `wave_committed` task list fall back to? — Every task id in the close record for the same `(wave, slice, attempt)`, unfiltered by status; then the `wave_dispatched` event with that key; then `—` (The issue asks for "the close record or the previous attempt's dispatch, whichever the code can prove"; the close record is the fuller proof, the dispatch event the fallback when no record survives)
- spec_assumption: What is a `## Numbers` span keyed on? — `(wave, slice, attempt)`, last `wave_closed` in log order wins (Stated in #71; keeps a reworked wave's two attempts and a sliced wave's two slices apart while collapsing a waived wave's two closes)
- spec_assumption: Where does the eighth golden live? — `internal/finish/skeleton_test.go`, beside the other seven (The user asked for it there, and a golden outside the table it belongs to is not one)
- spec_assumption: Where does the #74 section go, and does it carry a heading? — `## Issues`, between `## Goals` and `## Run` (Chosen by the user from three renderings; matches the body's other sections)
- spec_assumption: Reword the invariant only, or make `SKILL.md` generated? — Generated, from `commands/takt.md`, via a substitution profile (Chosen by the user after the trade-off was put; it makes the original brief's "regenerate with `task hosts:gen`" true and ends the class of defect #66 is an instance of)
- spec_assumption: Which file is the source of truth? — `commands/takt.md` — shape A (It is what the plugin ships and what every existing test loads as authoritative; shape B would make it a build artifact for the same guarantee)
- spec_assumption: Does `crossHostInvariants` survive the generator? — Yes, plus the new push-clause anchor (It becomes the check that nobody turned a shared sentence into a substitution)

## What went well / what was hard

The run fixed itself in front of us. Wave 1 was waived, so its `wave_committed` event carries
`tasks: []` — the exact #71 defect — and the close record holds task 4 at its pre-waiver `rework`
status. The `## What shipped` table above names task 4 anyway, and `## Numbers` shows one span for
wave 1 attempt 3 where the log holds two `wave_closed` events under that key. Both halves of #71
are demonstrated on this run's own bundle rather than only on a fixture.

The spec review earned its keep twice. It caught that the close-record fallback would have filtered
on `done`/`waived` — and `takt waive` writes only `state.Tasks[i].Status`, so a waived task keeps
its `failed`/`rework` verdict in the record; the filter would have discarded exactly the task the
row exists to name. It also caught that `deriveSlug` supports `/issues/<n>` topics, so parsing only
`#N` would have omitted the section for the canonical run-started-from-an-issue case that #74 is
about. Neither was reachable by reading the diff; both came from reading the code the spec claimed
things about. Task 1's implementer then found a third: the golden fixture the plan specified would
not actually have failed under a reintroduced status filter, because filtering it empty just falls
through to the dispatch event with the same id. It gave the waived wave two tasks so the filter
changes the rendered row.

What was hard was task 4, and none of it was the prose. The user authorised two additions after
wave 0's review — the design doc's §3.3/§6.1/§14 and `CONTRIBUTING.md` — and neither could reach
the recorded plan. `CONTRIBUTING.md` was reverted by the scope verifier at the wave close, because
authorisation given in a dispatch prompt is not authorisation in `plan.index.json`. The design-doc
regions stayed, and were then reported as out-of-scope four times: once by the intent lens (refuted
by the verifier as a false positive) and once by the backend on each of the three attempts. The
backend judges against the recorded task description, which was written at the plan gate and cannot
learn anything afterwards, so no attempt could satisfy it.

Attempts 2 and 3 each fixed a real defect and exposed the next one in the same paragraph: attempt 1
over-generalised "no write after the archive commit" into "the disposition itself is never
recorded", which the same step contradicts nine lines above; attempt 2 replaced that with a claim
about commits only, which does not exclude a non-commit write; attempt 3's fix then made visible
that step 5 had always said "lock released; commit", while `ClearSession` runs inside
`applyAndStop` after `commitBundle` returns. That last one was pre-existing, not introduced here.
Three rounds for one paragraph is expensive, but each round found something true.

One tool note: an implementer dispatch died on an API safeguard error mid-run
(`[reasoning_extraction]`, request `req_011CeZkFxFW97yic7RJZdREN`). Re-dispatching the same task
with the same model worked.

## Not proven

- task 4 — waived: The requested stale prose is largely replaced correctly, but the design now contradicts the implemented lock ordering and the diff contains unrelated design-document changes outside the four specified regions.

Task 4 is waived, so its verdict is a decision and not a pass. What the waiver does *not* mean:
every one of its verify commands passed on attempt 3, all six internal lenses were clean, and the
whole-branch gate (`task test`, `task lint`, `task hosts:check`, `go vet ./...`) was green. The
waiver covers two things the attempt could not close — a scope objection no attempt could satisfy,
and one clause that was fixed afterwards in `06c080e` instead.

`CONTRIBUTING.md` and the §7.5 step 5 lock ordering were fixed in a session commit, outside the
wave machinery: they carry no task review and no lens pass, only the suite and a human's reading.
That is weaker evidence than everything else on this branch.

The generator proves the copilot skill is a byte-exact render of `commands/takt.md`; it does not
prove the *profile* is right. A substitution whose replacement text is wrong renders and checks
clean — what fails loudly is only a `from` that stops matching its declared count. The evidence
that the profile is faithful is that regenerating reproduced the committed file with the reworded
invariant as the sole diff.

Nothing here was run against a real GitHub pull request. `## Issues` is proven by unit tests over
`BuildPR`'s output; that GitHub closes four issues from four repeated keywords is documented
behaviour this run took on trust.

## Lessons

**Authorisation has to reach `plan.index.json`, or the scope verifier will undo it.** A file the
user approves mid-run cannot be edited by a task that does not declare it — the close reverts it and
the attempt is wasted. The workable routes are: put the file in the plan before execution, or accept
it will be a session commit at the finish phase. Deciding which *before* dispatching saves a round.

**The backend reviewer cannot learn anything after the plan gate.** It grades the diff against the
recorded task description, so a scope change agreed later reads to it as creep, every time, however
often a verifier refutes it. When a finding is unfixable in that way, spend the attempts on the
findings that are real and waive the other with both facts side by side — this run's waiver quotes
the authorisation and the recorded plan that predates it.

**A negative claim in prose needs its scope kept.** Three attempts went into one paragraph because
each rewrite dropped a qualifier the original had: "no `disposition_applied` *event*" became "the
disposition is never recorded"; "writes nothing after its commit" became "its commit is the last one
it takes". The fix that finally held restates the original's scope rather than paraphrasing its
gist. When retiring a sentence, check what its qualifiers were load-bearing for before replacing it.

**Ask what a test would fail on, not what it covers.** The plan's golden fixture covered the waived
wave and would still have passed with the defect reintroduced, because a second source supplied the
same ids. The implementer caught it by asking which mutation the fixture distinguishes. That
question is worth asking of every fixture written to pin a specific bug.

**Verify commands can check code shape, not just content.** `git diff main -- <files> | grep -E
'^[+-]' | grep -vE '^[+-][+-]' | grep -vE '^[+-][[:space:]]*(//|$)' | grep -c . | grep -qx 0` proves
a diff is comments-only — the kind of claim greps for phrases cannot make. The plan review asked for
it and it held for all three attempts.

## Follow-ups

- major — Copilot host files section still describes the skill as hand-kept except for its version line (wave 0) — CONTRIBUTING.md:67-71 ("### Copilot host files") says only "`hosts/copilot/agents/*.agent.md` are generated from `agents/*.md` by `task hosts:gen`... The skill's `takt version --expect <version>` line is stamped by `task version:set`" — implying the rest of hosts/copilot/skills/takt/SKILL.md is hand-maintained. This diff (internal/hosts/skill.go, internal/tools/hostgen/main.go's generateSkill) makes the WHOLE skill file a render of commands/takt.md via a substitution profile, written and staleness-checked by the same `task hosts:gen` / `task hosts:check` that already covers the agent files (per the new TestRunRegeneratesAHandEditedSkill and TestRunRefusesARootMissingASkillSource). A contributor following this section would still believe hand-editing the skill body is normal practice and that only the version line is generator-owned, which is no longer true and will now be silently overwritten by `task hosts:gen` or fail `task hosts:check`/CI.
- major — Design doc's §14 testing table and §3.3/§6.1 architecture text omit the new skill-generation parity test and its "generated, never hand-edited" status (wave 0) — Line 1239 (§14 Testing table, `prompt` row) names the Copilot parity tests by name — `TestCopilotSkillNamesEverythingTheBinaryCanEmit`, `TestCopilotSkillHandshakeMatchesTheManifest`, `TestCopilotAgentsAreGeneratedFromTheClaudeCodeAgents`, `TestCopilotHostFrontmatterIsParseable` — but not the new `TestCopilotSkillIsGeneratedFromTheClaudeCodePrompt` (internal/prompt/copilot_test.go) that proves the skill is a byte-exact render of commands/takt.md, the direct skill-level analogue of the agents' own parity test already listed. Two related spots are stale the same way: §3.3's repository-layout table (lines 111-112) marks `hosts/copilot/agents/*.agent.md` "generated from agents/*.md — never hand-edited" but leaves the SKILL.md row (line 111) and the internal/hosts and internal/tools/hostgen rows (lines 128-130, "renders agents/*.md for other hosts" / "writes and checks the generated Copilot agent files") not mentioning the skill at all; and §6.1's paragraph (lines 611-616) ends "...fail when a generated agent file is stale" without now also covering the generated skill file. None of these were touched by this diff even though the diff is exactly the change (#66/§2.3) that makes them incomplete.
- major — generateSkill duplicates the agent loop's compare/check/write logic instead of sharing a helper (wave 0/task 3) — run()'s per-agent loop (lines 66-78: read dst, bytes.Equal skip, `check` → print "stale:" + count, else write() + print "wrote") is the same four-branch shape newly written again in generateSkill (lines 117-127) for the skill file. Both were touched/added by this wave's task-3. A small `writeOrReport(dst, out, check, stdout, stderr) (stale int, err error)` would serve both call sites; instead the diff reimplements the pattern rather than factoring it, which is exactly the 'duplicated helper' this lens looks for — and it is the same file, so a future edit to one copy (e.g. the stale-message wording) can silently drift from the other.
- major — generateSkill's RenderCopilotSkill error path (a render error is exitFailure) is never exercised (wave 0/task 3) — internal/tools/hostgen/main.go:112-115 returns the error from hosts.RenderCopilotSkill straight out of generateSkill, and run() (main.go:85-89) turns it into exitFailure — spec §2.3 explicitly names this half of the contract ('a render error is exitFailure'). Neither TestRunRegeneratesAHandEditedSkill nor TestRunRefusesARootMissingASkillSource (nor any other test in main_test.go) ever seeds a root whose commands/takt.md fails a substitution's declared count, so this branch of generateSkill/run is untested at the hostgen level — it is only exercised indirectly through hosts.RenderCopilotSkill's own unit tests in internal/hosts/skill_test.go, never through run()'s wiring of that error into exitFailure and its stderr message.
- 8 minor, 2 nit — see follow-ups.json, which holds every one verbatim

## Numbers

```json
{
  "internal_review": {
    "candidates": 17,
    "confirmed": 16,
    "false_positives": 1,
    "unattributed": 3,
    "by_lens": {
      "consistency": {
        "reported": 6,
        "confirmed": 6
      },
      "docs": {
        "reported": 4,
        "confirmed": 4
      },
      "intent": {
        "reported": 1,
        "confirmed": 0
      },
      "tests": {
        "reported": 7,
        "confirmed": 7
      }
    },
    "scoped_passes": 0,
    "scoped_changed_verdict": 0,
    "overlap": 0,
    "skipped": 0
  },
  "wave_timings": [
    {
      "wave": 0,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T20:55:27.783047344Z",
      "closed_at": "2026-08-30T21:25:04.072448212Z",
      "committed": true,
      "committed_at": "2026-08-30T21:25:04.072421086Z"
    },
    {
      "wave": 1,
      "slice": 1,
      "attempt": 1,
      "dispatched_at": "2026-08-30T21:25:10.80872684Z",
      "closed_at": "2026-08-30T22:25:21.721866904Z",
      "committed": false
    },
    {
      "wave": 1,
      "slice": 1,
      "attempt": 2,
      "dispatched_at": "2026-08-30T22:25:42.246630383Z",
      "closed_at": "2026-08-30T22:37:23.970206841Z",
      "committed": false
    },
    {
      "wave": 1,
      "slice": 1,
      "attempt": 3,
      "dispatched_at": "2026-08-30T22:45:34.28236692Z",
      "closed_at": "2026-08-30T23:11:42.229506913Z",
      "committed": true,
      "committed_at": "2026-08-30T23:11:42.229473593Z"
    }
  ]
}
```
