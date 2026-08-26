# Retro — parsereport-in-internal-cli-cmd-record-go-reads

## What was built

`parseReport` in `internal/cli/cmd_record.go` no longer prefix-matches `STATUS:` /
`SUMMARY:` / `BLOCKERS:` on the raw line. It strips leading list, quote, heading and
ordered-list markers and the decoration runs (`*`, `_`, `` ` ``) that models put around
the key, the value or the whole line, then matches the exact uppercase key anchored at
what remains — so `**STATUS:** done`, `- STATUS: done`, `STATUS: **done**` and
`` `STATUS:` done `` all record `done`, while a mid-sentence mention, a lowercase key
and a key without a colon still record nothing. A new `internal/cli/cmd_record_test.go`
proves the grammar over the full marker × decoration cross-product, and
`internal/cli/execute_test.go` gained the first test of the `--status` / `--summary` /
`--blockers` overrides. The contract text moved with the code: `agents/implementer.md`
(and the regenerated Copilot agent) and §5.1 of the design spec.

All six goals were judged **achieved** at `cdd73ef`, each with evidence the assessor ran
itself; `takt verify` passed all 16 commands at the same sha.

## What went well / what did not

- **Execution was the cheap part.** Four tasks in two waves: wave 0 (tasks 1, 3, 4)
  dispatched 17:44:13 and committed 17:47:55 — 3m42s for the parser, its ~27k-case test
  file, the agent contract and the spec row; wave 1 (task 2) took 5m01s. Planning took
  2h26m of the run's 2h46m.
- **Task 1 verified itself beyond the brief.** The implementer ran two throwaway
  mutations — removing the `if opener` guard (108 assertions failed) and making unordered
  markers a run (the `--` / `>>` / `**` rows failed) — then restored the file
  byte-for-byte before its final verify. That is the evidence the cross-product test
  actually bites, and nothing in the brief asked for it.
- **Task 4 burned an attempt on a wording mismatch.** Its verify grepped `decorat`,
  which matches "decoration"; the task description demanded the literal word
  "decorated". Attempt 1 passed every verify command and was still reworked by the
  reviewer on exactly that word. When a plan mandates wording, the grep and the prose
  have to mandate the same string. Attempt 2 (escalated sonnet → opus) fixed it.
- **The spec gate ran six review passes** (findings 2, 4, 3, 3, 2, then approve) and the
  plan gate four (2, 2, 2, 1, then accepted). Nearly every finding was real, but a
  majority were defects *the previous revision introduced* — the interior-run guard
  written to answer pass 3 created the contradictions pass 4 found, which the
  opener rule then replaced. Reviewing a spec that is being rewritten between passes
  costs a full re-read each time.
- **Alignment came back clean.** All five clauses `covered` on the first audit, no
  contraction and no creep — the review loop grew the spec's precision without moving
  its scope.
- **No task failed, none was waived, and no verification was overridden.** The only
  gate override in the run was the plan gate's final `accept`, taken after the last
  finding had been fixed by hand rather than left standing.

## Follow-ups

- Nothing was waived and nothing was left undone against the declared goals.
- The two minors on the spec's *approving* pass are unaddressed by construction: an
  approve freezes the artifact, and editing it to fold them in would change the hash and
  re-arm the gate. They were (a) "spacing around the colon" reading as if `STATUS : done`
  were allowed, and (b) the must-not-match rows exercising only `STATUS`, not `SUMMARY`
  or `BLOCKERS`. The second is a genuine, if small, test-coverage gap — a per-key
  inconsistency could pass today's rows. Fits issue #15 (test gaps).
- `STATUS: done**` — a closing run on a line that opened nothing — deliberately keeps its
  stars and fails the digest check. Documented as a non-goal with a test, so a future
  change to it is a deliberate one.
- Dogfood findings from this run (issue #20) are listed below and should become issues.

## Dogfood notes (issue #20)

Observations about **takt itself**, which is what this run existed to collect.

- **`retro-inputs.json` reports `review_findings: 0`.** This run produced 14 gate-review
  findings (spec 6 passes, plan 4) and one task rework with a finding. Whatever the
  counter counts, it is not what the retro needs.
- **The goal assessor cited the wrong file.** G1, G2 and G3's citations point at
  `internal/cli/execute_test.go:34-75` and similar, but the tests it names
  (`TestParseReportAcceptsTheDecorationCrossProduct`, `TestParseReportMustNotMatch`) live
  in `internal/cli/cmd_record_test.go`. The verdicts are right and the evidence prose is
  right; the `path:line` citations are not, and nothing validates them.
- **`wave_timings` lost wave 0's first attempt.** Only the attempt that committed
  (attempt 2) appears, so the retro cannot see how long the reworked attempt took.
- **Answering a gate before `takt next` arms it is a silent no-op.** Reading
  `reviews/spec.md` and running `takt answer --gate gate_review --choice revise` before
  the `ask` op had been emitted printed `{"ignored": true, "reason": "no pending gate
  gate_review"}` at exit 0. Correct, but the JSON gives no hint (`run takt next first`),
  and a session that trusts exit 0 would proceed as if the gate were answered.
- **"Revise" at the plan gate has no defined mechanism.** The plan artifacts are written
  by the planner agent, but the gate's revise option is satisfied by the session editing
  `plan.index.json` / `plan.md` by hand — the loop never re-dispatches the planner, and
  nothing in the op text says which is intended.
- **An approve freezes its own leftovers.** Minors on the approving pass can only be
  folded in by changing the artifact, which re-arms the hash-bound gate and costs another
  backend call. There is no "accept with these minors recorded as follow-ups" path.
- **Dispatch briefs are large and must be pasted verbatim.**
  `briefs/alignment-verdicts.md` is 41KB (clauses + anchor + spec + plan + index) and each
  wave-0 task brief is ~22KB, of which the shared spec excerpt is the bulk. The op table
  requires the brief's *contents* as the subagent prompt, so every dispatch reads the file
  into the session and writes the same bytes back out — roughly 2× the brief per dispatch,
  ~136KB for this run. A `--brief-path` convention would remove it, at the cost of the
  agent reading artifacts the session never saw.
- **Gate churn is permanent history.** 27 commits on the branch, 11 of them
  `gate gate_review: revise` / `... reviewed: rework` bookkeeping. Squashing is the user's
  call at `branch_finish`, but the default shape of a small change's history is mostly
  gate traffic.
- **`takt status` shows `tasks: 0 total` during the plan phase** (before the index is
  materialised into state) and prints an empty `alignment:` section even once clauses are
  confirmed. Both read as "nothing there" rather than "not yet".
- **Issue #20 says the retro lands in `finish/retro.md`; the `retro` op writes
  `retro.md`** at the bundle root. The issue text is what is stale, but the two should
  agree.
- **Session cwd drift is a real hazard for the loop-driver.** A `cd` into the bundle in
  one Bash call persisted, and a later repo-relative path reported "No such file or
  directory" for a file that exists. `takt` itself was unaffected — it walks up to the
  repository root — but the session driving the loop should use absolute paths.

### Models and timings observed

| phase | wall clock | agents |
|---|---|---|
| init → goals frozen | 15:07 → 15:12 | none (session) |
| spec review loop (6 passes) | 15:12 → 16:35 | reviewer: copilot / gpt-5.6-sol |
| planning + plan review (4 passes) | 16:35 → 17:29 | planner: **fable**; reviewer: copilot / gpt-5.6-sol |
| alignment (clauses + verdicts) | 17:29 → 17:33 | alignment-auditor: **sonnet** ×2 |
| wave 0 (tasks 1, 3, 4; task 4 ×2) | 17:44 → 17:47 | implementer: **opus** (task 1, class implement), **sonnet** (tasks 3, 4, class docs), **opus** (task 4 attempt 2 — retries escalate) |
| wave 1 (task 2) | 17:47 → 17:53 | implementer: **sonnet** (class test) |
| verify + goal assessment | 17:53 → 17:55 | goal-assessor: **sonnet** |

The two long gaps inside the review loops (15:16 → 16:22 and 16:43 → 17:20) are the
session waiting on the user at an `ask` gate, not takt working.
