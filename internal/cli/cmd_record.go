package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/op"
)

// cmdRecord records an agent's result: an implementer digest (`--task`, Task
// 7) or a non-task agent's artifacts (`--agent`) — spec §5.1.
func cmdRecord(env Env) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	agent := fs.String("agent", "", "planner | alignment-auditor | goal-assessor | reviewer")
	mode := fs.String("mode", "", "alignment-auditor: clauses | verdicts; reviewer: <lens> | verify")
	from := fs.String("from", "", "file holding the agent's final message")
	task := fs.Int("task", 0, "task id (implementer digest)")
	attempt := fs.Int("attempt", 0, "attempt the digest belongs to")
	status := fs.String("status", "", "done | failed | blocked (overrides STATUS: in --from)")
	summary := fs.String("summary", "", "overrides SUMMARY:")
	blockers := fs.String("blockers", "", "overrides BLOCKERS:")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	if *task > 0 {
		return recordTask(env, tgt.ws, tgt.bdir, tgt.st, digestInput{
			task: *task, attempt: *attempt, from: *from,
			status: *status, summary: *summary, blockers: *blockers,
		})
	}
	switch *agent {
	case op.AgentPlanner:
		return recordPlanner(ctx, env, tgt)
	case op.AgentAlignmentAuditor:
		return recordAlignment(env, tgt.bdir, tgt.st, *mode, *from)
	case op.AgentGoalAssessor:
		return recordGoals(ctx, env, tgt, *from)
	case op.AgentReviewer:
		return recordReviewer(env, tgt, *mode, *attempt, *from)
	}
	return fail(env.Stderr, exitUsage,
		"record needs --task N or --agent planner|alignment-auditor|goal-assessor|reviewer", "")
}

// recordPlanner validates what the planner wrote and reports the problems
// instead of failing, so the loop can re-dispatch it (spec §5.3 row 8).
func recordPlanner(ctx context.Context, env Env, tgt *runTarget) int {
	if code := stampPlannerSpecHash(env, tgt.bdir); code != 0 {
		return code
	}
	facts, err := gatherFacts(ctx, tgt.ws, tgt.bdir, tgt.st, false, false, timeNow(), "")
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if !facts.HasIndex {
		_ = bundle.AppendEvent(tgt.bdir, "plan_invalid", map[string]any{
			keyProblems: []string{"plan.index.json missing"},
		})
		return printJSON(env, map[string]any{keyValid: false, keyProblems: []string{"plan.index.json was not written"}})
	}
	// Validity is gatherIndexFacts's judgment, not a second opinion taken
	// here: `takt next` decides from the same facts, so a plan this command
	// called valid while Decide called it invalid would put the run in a
	// loop it could not leave (review N0, M8). The missing plan.md is one of
	// those problems — it arrives in IndexProblems like any other.
	if !facts.IndexValid {
		_ = bundle.AppendEvent(tgt.bdir, "plan_invalid", map[string]any{keyProblems: facts.IndexProblems})
		return printJSON(env, map[string]any{keyValid: false, keyProblems: facts.IndexProblems})
	}
	return printJSON(env, map[string]any{keyValid: true})
}

// stampPlannerSpecHash overwrites plan.index.json's spec_hash with takt's
// own hash of spec.md before anything validates the index. The planner has
// no Bash and no way to compute a sha256 itself, so spec_hash is takt's
// fact, not the agent's: whatever the planner wrote there — a guess, a
// placeholder, or nothing — is discarded. A missing or unparseable
// plan.index.json is not an error here; gatherFacts reports that the
// ordinary way once this returns.
func stampPlannerSpecHash(env Env, bdir string) int {
	idx, err := readIndex(bdir)
	if err != nil {
		return 0
	}
	if !fileNonEmpty(filepath.Join(bdir, "spec.md")) {
		return fail(env.Stderr, exitError, "spec.md is missing or empty",
			"write the approved spec to "+filepath.Join(bdir, "spec.md")+" first")
	}
	spec, err := os.ReadFile(filepath.Join(bdir, "spec.md"))
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	idx.SpecHash = goals.Hash(spec)
	if err = writeIndex(bdir, idx); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return 0
}

// recordGoals parses the assessor's verdicts, validates them against
// goals.md and either checks the goals at HEAD or leaves the unmet list
// for the goals_unmet gate.
func recordGoals(ctx context.Context, env Env, tgt *runTarget, from string) int {
	if code := finishPhaseOnly(env, tgt.st, "record --agent goal-assessor"); code != 0 {
		return code
	}
	vs, problems, code := readVerdicts(env, tgt.bdir, from)
	if code != 0 {
		return code
	}
	if len(problems) > 0 {
		// No record is written, so the dispatch is still pending: the next
		// `takt next` hands the brief out again — with these problems
		// quoted in the brief — and the assessment is simply retaken (spec
		// §5.3 row 21). The event is the durable record the attempt cap
		// counts: three of them since the last `goals_attempts_reset` and
		// the run asks `agent_invalid` instead of retrying again (§4.4).
		// A usable reply ends the streak with a reset of its own below.
		_ = bundle.AppendEvent(tgt.bdir, evGoalsInvalid, map[string]any{keyProblems: problems})
		return printJSON(env, map[string]any{keyValid: false, keyProblems: problems})
	}
	head, err := tgt.ws.Repo.HeadSHA(ctx)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	rec := finish.GoalsRecord{SHA: head, Verdicts: vs, At: timeNow()}
	// A re-assessment keeps the earlier waivers as long as the record they
	// were written into still covers HEAD. "Still covers" has to be
	// headCovered, not prev.SHA == head: the waive answer commits the bundle
	// on its way out, so by the time a record with waivers exists HEAD is
	// already one bundle-only commit past it, and an equality test would
	// silently re-open every goal the user just waived. A code commit does
	// move the goalposts, and there the waivers are correctly dropped —
	// they were given against code HEAD no longer holds.
	if prev, _ := finish.ReadGoals(tgt.bdir); prev != nil {
		covered, cerr := headCovered(ctx, tgt.ws, tgt.bdir, prev.SHA)
		if cerr != nil {
			return fail(env.Stderr, exitError, cerr.Error(), "")
		}
		if covered {
			rec.Waived = prev.Waived
		}
	}
	unmet := rec.Unmet()
	if len(unmet) == 0 {
		if err = markGoalsChecked(tgt, rec, nil); err != nil {
			return fail(env.Stderr, exitError, err.Error(), "")
		}
	} else if err = finish.WriteGoals(tgt.bdir, rec); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	lost := endAttemptStreak(tgt.bdir, evGoalsInvalid, evGoalsReset, map[string]any{keyReason: reasonRecorded})
	return printJSON(env, warnStreakLoss(map[string]any{
		keySHA: head, "all_achieved": len(unmet) == 0, "unmet": unmetList(unmet),
	}, lost))
}

// readVerdicts pulls the JSON block out of the assessor's message and
// validates it against the run's frozen goal ids. It returns the verdicts,
// the problems that make the message unusable, and an exit code — and only
// one of the three is ever non-zero.
//
// What the agent got wrong is a problem list, not a failure: spec §5.1 has
// `record --agent` report validation errors instead of failing, which is the
// contract the planner already answers on ({valid:false, problems}, exit 0)
// and the one contract a prompt has to handle (review M1). takt's own
// invariants are a different thing: an unreadable --from file is a
// mis-wired session, and a goals.md this run cannot read or parse is a
// broken bundle — neither is the assessor's doing, and both exit 1 as spec
// §13 asks.
func readVerdicts(env Env, bdir, from string) ([]finish.GoalVerdict, []string, int) {
	raw, err := os.ReadFile(from)
	if err != nil {
		return nil, nil, fail(env.Stderr, exitError, err.Error(), "")
	}
	gb, err := os.ReadFile(filepath.Join(bdir, "goals.md"))
	if err != nil {
		return nil, nil, fail(env.Stderr, exitError, err.Error(), "")
	}
	g, err := goals.Parse(gb)
	if err != nil {
		return nil, nil, fail(env.Stderr, exitError, err.Error(), "")
	}
	js, err := backend.ExtractJSON(string(raw))
	if err != nil {
		return nil, []string{"no JSON block in the assessor's message: " + err.Error()}, 0
	}
	vs, err := finish.ParseVerdicts(js, g.IDs())
	if err != nil {
		return nil, []string{err.Error()}, 0
	}
	return vs, nil, 0
}

// unmetList is the goals_unmet ask context shape: {id, verdict, evidence}.
func unmetList(vs []finish.GoalVerdict) []map[string]any {
	out := []map[string]any{}
	for _, v := range vs {
		out = append(out, map[string]any{"id": v.ID, keyVerdict: v.Verdict, "evidence": v.Evidence})
	}
	return out
}

// recordAlignment stores the auditor's clauses or verdicts in alignment.json,
// bound to the anchor they were produced from (spec §7.3).
//
// What the auditor got wrong is a problem list, not a failure: it answers on
// the same contract the planner and the assessor do — `{valid:false,
// problems}` at exit 0, the rejection on the event log as
// `alignment_invalid`, and nothing written — so `takt next` finds the
// dispatch still pending and hands the brief out again, this time quoting
// what was wrong with the last reply; after three rejections since the last
// `alignment_attempts_reset` it asks `agent_invalid` rather than retry a
// fourth time (spec §5.1, §5.3). A usable reply ends the streak: the record
// appends that reset itself, with no problems on it, so neither the count
// nor the quoted rejections carry into the next mode's brief.
// Exiting 1 instead stopped the loop dead on a mistake the auditor
// could have corrected on a second attempt, and did it for the one agent
// whose result is advisory. takt's own invariants stay failures: an
// unreadable --from file and an unusable --mode are a mis-wired session, and
// a bundle that cannot be written is broken (spec §13).
func recordAlignment(env Env, bdir string, st *bundle.State, mode, from string) int {
	if mode != alignmentModeClauses && mode != alignmentModeVerdicts {
		return fail(env.Stderr, exitUsage, "--mode must be clauses or verdicts", "")
	}
	raw, err := os.ReadFile(from)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	a, _ := readAlignment(bdir)
	if a == nil || a.AnchorHash != anchorHash(st.Topic) {
		a = &alignmentFile{AnchorHash: anchorHash(st.Topic)}
	}
	if problems := applyAuditorMessage(bdir, a, mode, string(raw)); len(problems) > 0 {
		_ = bundle.AppendEvent(bdir, evAlignmentInvalid, map[string]any{keyProblems: problems})
		return printJSON(env, map[string]any{keyValid: false, keyProblems: problems})
	}
	if err = writeAlignment(bdir, *a); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	lost := endAttemptStreak(bdir, evAlignmentInvalid, evAlignmentReset,
		map[string]any{keyReason: reasonRecorded, keyMode: mode})
	return printJSON(env, warnStreakLoss(map[string]any{keyMode: mode, "ok": true}, lost))
}

// The two things the alignment auditor is ever asked for (spec §7.3).
const (
	alignmentModeClauses  = "clauses"
	alignmentModeVerdicts = "verdicts"
)

// applyAuditorMessage pulls the JSON block out of the auditor's final message
// and applies it to a, returning the problems that make the message unusable
// — and mutating a only when there are none.
func applyAuditorMessage(bdir string, a *alignmentFile, mode, message string) []string {
	js, err := backend.ExtractJSON(message)
	if err != nil {
		return []string{"no JSON block in the auditor's message: " + err.Error()}
	}
	if mode == alignmentModeClauses {
		return applyClauses(bdir, a, js)
	}
	return applyVerdicts(bdir, a, js)
}

// applyClauses replaces the clause list, which un-confirms it and drops any
// verdicts judged against the old clauses.
func applyClauses(bdir string, a *alignmentFile, js []byte) []string {
	var msg struct {
		Clauses []brief.Clause `json:"clauses"`
	}
	if err := json.Unmarshal(js, &msg); err != nil {
		return []string{"the auditor's JSON block does not parse: " + err.Error()}
	}
	if len(msg.Clauses) == 0 {
		return []string{"the auditor's JSON block has no clauses"}
	}
	a.Clauses, a.Confirmed, a.Verdicts = msg.Clauses, false, nil
	_ = bundle.AppendEvent(bdir, "alignment_clauses", map[string]any{keyCount: len(msg.Clauses)})
	return nil
}

// applyVerdicts stores the per-clause drift verdicts.
func applyVerdicts(bdir string, a *alignmentFile, js []byte) []string {
	var msg struct {
		Verdicts []alignmentVerdict `json:"verdicts"`
	}
	if err := json.Unmarshal(js, &msg); err != nil {
		return []string{"the auditor's JSON block does not parse: " + err.Error()}
	}
	if len(msg.Verdicts) == 0 {
		return []string{"the auditor's JSON block has no verdicts"}
	}
	a.Verdicts = msg.Verdicts
	_ = bundle.AppendEvent(bdir, "alignment_verdicts", map[string]any{keyCount: len(msg.Verdicts)})
	return nil
}

// digestInput is the `--task` half of `takt record`'s command line: the
// agent's report file and the explicit overrides for what it should have
// said.
type digestInput struct {
	task     int
	attempt  int
	from     string
	status   string
	summary  string
	blockers string
}

// The three keys an implementer's trailer carries, without their colons.
const (
	trailerStatus   = "STATUS"
	trailerSummary  = "SUMMARY"
	trailerBlockers = "BLOCKERS"
)

// trailerDecoration holds the emphasis characters a decoration run is made of.
// A run is a maximal sequence of them and may mix them, so "*`" is one run of
// length two; runs are never required to match each other.
const trailerDecoration = "*_`"

// trailerSpace is the inline whitespace a leading marker must be followed by,
// and the whitespace a decoration run may be followed by.
const trailerSpace = " \t"

// maxHeadingMarker is the longest run of '#' that still counts as a heading;
// seven or more is not a marker.
const maxHeadingMarker = 6

// parseReport extracts the trailing STATUS / SUMMARY / BLOCKERS lines of an
// agent's final message and returns them in that order; the last occurrence
// of each wins, so an agent that quoted the template earlier in its message
// does not fool the parser.
//
// The match is tolerant of the way models decorate their output: leading list,
// quote and heading markers ("- ", "> ", "1. ", "## ") and bold, italic or
// backtick runs around the key, around the value or around the whole line come
// off before the key is matched, so "**STATUS:** done" and "> 1. **STATUS:
// done**" record what "STATUS: done" records. This is a tolerance layer, not a
// markdown validator: decoration is removed where it is found and never
// validated, so an unclosed run ("**STATUS: done") and a mismatched pair
// ("*STATUS:** done") are trailer lines too. What rejects a line is
// structural — the key must be uppercase, must carry its colon, and must start
// the line once markers and decoration are gone — and that is what keeps a
// mid-sentence mention of STATUS: out of the digest. One shape is deliberately
// left alone: a closing run on a line that opened nothing ("STATUS: done**")
// keeps its stars, because stripping it would also have to strip the star from
// "SUMMARY: changed wildcard *", which is what an undecorated line has always
// recorded (spec §5.1).
func parseReport(text string) (string, string, string) {
	var status, summary, blockers string
	for raw := range strings.SplitSeq(text, "\n") {
		key, value, ok := matchTrailerLine(raw)
		if !ok {
			continue
		}
		switch key {
		case trailerStatus:
			status = strings.ToLower(value)
		case trailerSummary:
			summary = value
		case trailerBlockers:
			blockers = value
		}
	}
	return status, summary, blockers
}

// matchTrailerLine decides whether one line of an agent's message is a trailer
// line. It strips the leading markers and the decoration run before the key
// (steps 1 and 2 of the grammar), matches the exact uppercase key anchored at
// what remains (step 3) and cleans the value (step 4), returning the key
// without its colon, the value, and whether the line matched at all.
func matchTrailerLine(raw string) (string, string, bool) {
	ln := stripTrailerMarkers(strings.TrimSpace(raw))
	ln, opener := cutDecorationRun(ln)
	for _, key := range []string{trailerStatus, trailerSummary, trailerBlockers} {
		if rest, ok := strings.CutPrefix(ln, key+":"); ok {
			return key, cleanTrailerValue(rest, opener), true
		}
	}
	return "", "", false
}

// stripTrailerMarkers removes the leading markers from ln: one marker plus the
// whitespace it must be followed by, repeatedly, so stacked markers such as
// "> 1. " all come off (step 1). The mandatory whitespace is what
// disambiguates '*': "* STATUS: done" is a bullet, "**STATUS:** done" is
// emphasis, and only the first form is consumed here.
func stripTrailerMarkers(ln string) string {
	for {
		n := trailerMarkerLen(ln)
		if n == 0 {
			return ln
		}
		rest := strings.TrimLeft(ln[n:], trailerSpace)
		if len(rest) == len(ln)-n {
			return ln
		}
		ln = rest
	}
}

// trailerMarkerLen reports the length of the marker at the front of ln, or 0
// if there is none. An unordered marker is a single character, never a run, so
// "--" and ">>" are not markers and "**" is never consumed as one; '#' is the
// exception, because "##" is a real heading, up to six.
func trailerMarkerLen(ln string) int {
	if ln == "" {
		return 0
	}
	switch ln[0] {
	case '-', '*', '+', '>':
		return 1
	case '#':
		n := len(ln) - len(strings.TrimLeft(ln, "#"))
		if n > maxHeadingMarker {
			return 0
		}
		return n
	default:
		return orderedMarkerLen(ln)
	}
}

// orderedMarkerLen reports the length of the ordered-list marker at the front
// of ln — one or more ASCII digits, leading zeros permitted and no sign, then
// a single '.' or ')' — or 0 if there is none.
func orderedMarkerLen(ln string) int {
	n := 0
	for n < len(ln) && ln[n] >= '0' && ln[n] <= '9' {
		n++
	}
	if n == 0 || n == len(ln) {
		return 0
	}
	if ln[n] != '.' && ln[n] != ')' {
		return 0
	}
	return n + 1
}

// cutDecorationRun removes the maximal decoration run at the front of ln and
// reports whether there was one — the line's opener.
func cutDecorationRun(ln string) (string, bool) {
	rest := strings.TrimLeft(ln, trailerDecoration)
	return rest, len(rest) != len(ln)
}

// cleanTrailerValue turns what follows the key's colon into the recorded value
// (step 4); opener says whether the line already gave up a decoration run
// before the key. Leading runs come off repeatedly — in "`STATUS:` `done`" the
// first closes the key and the second opens the value — and each one counts as
// an opener. One trailing run comes off only if the line produced an opener
// somewhere, which is what leaves a line that carried no decoration at all
// byte-identical to what it has always recorded.
func cleanTrailerValue(rest string, opener bool) string {
	value := strings.TrimSpace(rest)
	for {
		cut, found := cutDecorationRun(value)
		if !found {
			break
		}
		value = strings.TrimLeft(cut, trailerSpace)
		opener = true
	}
	if opener {
		value = strings.TrimRight(value, trailerDecoration)
	}
	return strings.TrimSpace(value)
}

// recordTask writes one implementer's digest (spec §7.4 step 3). A stale
// attempt of a task the wave really did dispatch is ignored rather than an
// error: a crashed agent that reports late must never overwrite the result
// of the attempt that replaced it. A digest for a task this run does not
// have, or for one the active wave never dispatched, is a different thing
// entirely — a mis-wired session, or a `record` aimed at the wrong run — and
// spec §13 lists it among the invariant violations that exit 1 rather than
// being silently swallowed as "ignored" (review M3).
func recordTask(env Env, ws *workspace, bdir string, st *bundle.State, in digestInput) int {
	if in.from != "" {
		b, err := os.ReadFile(in.from)
		if err != nil {
			return fail(env.Stderr, exitError, err.Error(), "")
		}
		s, sm, bl := parseReport(string(b))
		in.status, in.summary, in.blockers = cmp.Or(in.status, s), cmp.Or(in.summary, sm), cmp.Or(in.blockers, bl)
	}
	switch in.status {
	case bundle.StatusDone, bundle.StatusFailed, bundle.StatusBlocked:
	default:
		return fail(env.Stderr, exitError, "digest status must be done, failed or blocked",
			"the agent's final message must end with STATUS: / SUMMARY: / BLOCKERS: lines")
	}
	aw := st.ActiveWave
	t := st.Task(in.task)
	if t == nil {
		return fail(env.Stderr, exitError, fmt.Sprintf("this run has no task %d", in.task),
			"run `takt status` for the task ids, and check --slug")
	}
	if aw == nil || !slices.Contains(aw.Tasks, in.task) {
		return fail(env.Stderr, exitError,
			fmt.Sprintf("task %d is not in the active wave", in.task),
			"only a task the current wave dispatched can be recorded; run `takt next` for the dispatch")
	}
	if aw.Attempt != in.attempt {
		_ = bundle.AppendEvent(bdir, "digest_ignored", map[string]any{keyTask: in.task, keyAttempt: in.attempt})
		return printJSON(env, map[string]any{keyIgnored: true, keyReason: reasonStaleAttempt})
	}
	if code := writeDigest(env, ws, bdir, aw.N, t, in); code != 0 {
		return code
	}
	if err := bundle.SaveState(bdir, st); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(bdir, "task_recorded", map[string]any{
		keyTask: in.task, keyAttempt: in.attempt, keyStatus: in.status,
	})
	return printJSON(env, map[string]any{
		keyTask: in.task, keyAttempt: in.attempt, keyStatus: in.status, "recorded": true,
	})
}

// writeDigest persists the digest file and hangs the same bytes on the task
// so `takt status` can show the last result without re-reading the wave.
func writeDigest(env Env, ws *workspace, bdir string, waveN int, t *bundle.Task, in digestInput) int {
	d := digest{
		Task: in.task, Attempt: in.attempt, Status: in.status, Summary: in.summary,
		Blockers: in.blockers, Model: digestModel(ws.Cfg, bdir, waveN, t), RecordedAt: timeNow(),
	}
	if err := bundle.WriteJSONAtomic(digestPath(bdir, waveN, in.task, in.attempt), d); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	// The same bytes the file holds, hung on the task so `takt status` can
	// show the last result without re-reading the wave directory.
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	t.LastDigest = b
	return 0
}
