package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
)

// cmdRecord records an agent's result: an implementer digest (`--task`, Task
// 7) or a non-task agent's artifacts (`--agent`) — spec §5.1.
func cmdRecord(env Env) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	agent := fs.String("agent", "", "planner | alignment-auditor | goal-assessor")
	mode := fs.String("mode", "", "alignment-auditor: clauses | verdicts")
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
	case "planner":
		return recordPlanner(ctx, env, tgt)
	case "alignment-auditor":
		return recordAlignment(env, tgt.bdir, tgt.st, *mode, *from)
	}
	return fail(env.Stderr, exitUsage, "record needs --task N or --agent planner|alignment-auditor", "")
}

// recordPlanner validates what the planner wrote and reports the problems
// instead of failing, so the loop can re-dispatch it (spec §5.3 row 8).
func recordPlanner(ctx context.Context, env Env, tgt *runTarget) int {
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
	if !facts.IndexValid {
		_ = bundle.AppendEvent(tgt.bdir, "plan_invalid", map[string]any{keyProblems: facts.IndexProblems})
		return printJSON(env, map[string]any{keyValid: false, keyProblems: facts.IndexProblems})
	}
	return printJSON(env, map[string]any{keyValid: true})
}

// recordAlignment stores the auditor's clauses or verdicts in alignment.json,
// bound to the anchor they were produced from (spec §7.3).
func recordAlignment(env Env, bdir string, st *bundle.State, mode, from string) int {
	raw, err := os.ReadFile(from)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	js, err := backend.ExtractJSON(string(raw))
	if err != nil {
		return fail(env.Stderr, exitError, "no JSON block in the auditor's message: "+err.Error(),
			"re-dispatch the auditor")
	}
	a, _ := readAlignment(bdir)
	if a == nil || a.AnchorHash != anchorHash(st.Topic) {
		a = &alignmentFile{AnchorHash: anchorHash(st.Topic)}
	}
	switch mode {
	case "clauses":
		if code := applyClauses(env, bdir, a, js); code != 0 {
			return code
		}
	case "verdicts":
		if code := applyVerdicts(env, bdir, a, js); code != 0 {
			return code
		}
	default:
		return fail(env.Stderr, exitUsage, "--mode must be clauses or verdicts", "")
	}
	if err = writeAlignment(bdir, *a); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{"mode": mode, "ok": true})
}

// applyClauses replaces the clause list, which un-confirms it and drops any
// verdicts judged against the old clauses.
func applyClauses(env Env, bdir string, a *alignmentFile, js []byte) int {
	var msg struct {
		Clauses []brief.Clause `json:"clauses"`
	}
	if err := json.Unmarshal(js, &msg); err != nil || len(msg.Clauses) == 0 {
		return fail(env.Stderr, exitError, "auditor JSON has no clauses", "")
	}
	a.Clauses, a.Confirmed, a.Verdicts = msg.Clauses, false, nil
	_ = bundle.AppendEvent(bdir, "alignment_clauses", map[string]any{keyCount: len(msg.Clauses)})
	return 0
}

// applyVerdicts stores the per-clause drift verdicts.
func applyVerdicts(env Env, bdir string, a *alignmentFile, js []byte) int {
	var msg struct {
		Verdicts []alignmentVerdict `json:"verdicts"`
	}
	if err := json.Unmarshal(js, &msg); err != nil || len(msg.Verdicts) == 0 {
		return fail(env.Stderr, exitError, "auditor JSON has no verdicts", "")
	}
	a.Verdicts = msg.Verdicts
	_ = bundle.AppendEvent(bdir, "alignment_verdicts", map[string]any{keyCount: len(msg.Verdicts)})
	return 0
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

// parseReport extracts the trailing STATUS / SUMMARY / BLOCKERS lines of an
// agent's final message and returns them in that order; the last occurrence
// of each wins, so an agent that quoted the template earlier in its message
// does not fool the parser.
func parseReport(text string) (string, string, string) {
	var status, summary, blockers string
	for raw := range strings.SplitSeq(text, "\n") {
		ln := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(ln, "STATUS:"):
			status = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(ln, "STATUS:")))
		case strings.HasPrefix(ln, "SUMMARY:"):
			summary = strings.TrimSpace(strings.TrimPrefix(ln, "SUMMARY:"))
		case strings.HasPrefix(ln, "BLOCKERS:"):
			blockers = strings.TrimSpace(strings.TrimPrefix(ln, "BLOCKERS:"))
		}
	}
	return status, summary, blockers
}

// recordTask writes one implementer's digest (spec §7.4 step 3). A digest
// that does not belong to the active wave's current attempt is ignored, not
// an error: a crashed agent that reports late must never overwrite the
// result of the attempt that replaced it.
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
	if aw == nil || aw.Attempt != in.attempt || !slices.Contains(aw.Tasks, in.task) || t == nil {
		_ = bundle.AppendEvent(bdir, "digest_ignored", map[string]any{keyTask: in.task, keyAttempt: in.attempt})
		return printJSON(env, map[string]any{"ignored": true, keyReason: "not the active wave attempt"})
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
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	p := digestPath(bdir, waveN, in.task, in.attempt)
	if err = os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if err = os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	t.LastDigest = b
	return 0
}
