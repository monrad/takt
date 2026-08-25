package cli

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"

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
		return recordTask(env, tgt.ws, tgt.bdir, tgt.slug, tgt.st, *task, *attempt, *from, *status, *summary, *blockers)
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

// recordTask records an implementer digest; wired in Task 7.
func recordTask(env Env, _ *workspace, _, _ string, _ *bundle.State, _, _ int, _, _, _, _ string) int {
	return fail(env.Stderr, exitError, "task digests are wired in Task 7", "")
}
