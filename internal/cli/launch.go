package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// digest is waves/<n>/task-<id>.a<attempt>.digest.json: the only thing takt
// trusts from an implementer (spec §7.4 step 3).
type digest struct {
	Task       int       `json:"task"`
	Attempt    int       `json:"attempt"`
	Status     string    `json:"status"`
	Summary    string    `json:"summary"`
	Blockers   string    `json:"blockers"`
	Model      string    `json:"model"`
	RecordedAt time.Time `json:"recorded_at"`
}

// tierUp is the escalation ladder (spec D22). Fable is never reached: a
// model is escalated to, never chosen by, a retry.
var tierUp = map[string]string{"haiku": modelSonnet, modelSonnet: modelOpus, modelOpus: modelOpus}

// modelSonnet and modelOpus are rungs of the escalation ladder above.
const (
	modelSonnet = "sonnet"
	modelOpus   = "opus"
)

// verifyTailLines is how much of a failed verify command's output a retry
// brief quotes back at the implementer.
const verifyTailLines = 5

// readDigest returns the digest of one attempt, or nil when it was never
// recorded (a crashed agent, or an attempt that never ran).
//
//nolint:nilnil // documented "no digest for this attempt" sentinel, like wave.ReadClose
func readDigest(bdir string, waveN, task, attempt int) (*digest, error) {
	b, err := os.ReadFile(digestPath(bdir, waveN, task, attempt))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d digest
	if err = json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", digestPath(bdir, waveN, task, attempt), err)
	}
	return &d, nil
}

// latestDigest returns the newest digest with attempt ≤ maxAttempt and the
// attempt it was found at, or nil, 0 when the task has none. A task recorded
// in an earlier attempt of the same wave still closes with the wave, which
// is what makes recovery re-dispatch only the tasks that never reported.
func latestDigest(bdir string, waveN, task, maxAttempt int) (*digest, int, error) {
	for a := maxAttempt; a >= 1; a-- {
		d, err := readDigest(bdir, waveN, task, a)
		if err != nil {
			return nil, 0, err
		}
		if d != nil {
			return d, a, nil
		}
	}
	return nil, 0, nil
}

// modelForAttempt implements spec D22: class → model on attempt 1, one tier
// up from the previous attempt's model on a retry when escalation is on;
// never Fable automatically, and opus stays opus.
func modelForAttempt(cfg config.Config, class string, attempt int, prev string) string {
	if attempt <= 1 || prev == "" {
		return cfg.ImplementerModel(class)
	}
	if !cfg.Agents.Implementer.EscalateOnRetry {
		return prev
	}
	if up, ok := tierUp[prev]; ok {
		return up
	}
	return prev
}

// digestModel re-resolves the model the launch picked for the task's current
// attempt, so the digest records what actually ran (spec §7.4; `takt status`
// reads it back from the digest).
func digestModel(cfg config.Config, bdir string, waveN int, t *bundle.Task) string {
	prev := ""
	if t.Attempt > 1 {
		prev, _, _ = previousAttempt(bdir, waveN, t.ID, t.Attempt-1)
	}
	return modelForAttempt(cfg, t.Class, t.Attempt, prev)
}

// launchWave dispatches up to max_parallel tasks of d.Wave (spec §7.4 step 1):
// it captures the baseline, renders one brief per task, and writes
// active_wave before printing the dispatch op.
func launchWave(ctx context.Context, r *nextRun, d decide.Decision) int {
	ids := slices.Clone(d.Tasks)
	sort.Ints(ids)
	if len(ids) > r.st.Config.MaxParallel {
		ids = ids[:r.st.Config.MaxParallel]
	}
	baseline, slice, err := waveBaseline(ctx, r, d.Wave)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	idx, err := readIndex(r.bdir)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	attempt := waveAttempt(r.st, ids, d.Attempt)
	agents := make([]op.Agent, 0, len(ids))
	for _, id := range ids {
		t, pt := r.st.Task(id), idx.Task(id)
		if t == nil || pt == nil {
			return fail(r.env.Stderr, exitError,
				fmt.Sprintf("task %d missing from state or index", id), "run `takt doctor`")
		}
		ag, aerr := renderTaskBrief(r, pt, t, d.Wave, attempt)
		if aerr != nil {
			return fail(r.env.Stderr, exitError, aerr.Error(), "")
		}
		agents = append(agents, ag)
	}
	r.st.ActiveWave = &bundle.ActiveWave{
		N: d.Wave, Slice: slice, Attempt: attempt, StartedAt: r.now,
		SessionID: r.session, Tasks: ids, Baseline: baseline,
	}
	if err = bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "wave_dispatched", map[string]any{
		keyWave: d.Wave, keyAttempt: attempt, keyTasks: ids,
	})
	return printOp(r.env, dispatchOp(r, d.Wave, attempt, agents))
}

// dispatchOp is the wave's dispatch op: one agent per task, and the exact
// `takt record` line the session must run for each of them.
func dispatchOp(r *nextRun, waveN, attempt int, agents []op.Agent) op.Op {
	return op.Op{
		Op:        op.Dispatch,
		Narration: fmt.Sprintf("wave %d (attempt %d): %d tasks", waveN, attempt, len(agents)),
		Wave:      new(waveN),
		Attempt:   attempt,
		Agents:    agents,
		Record: fmt.Sprintf("takt record --task <N> --attempt %d --from <file> --slug %s",
			attempt, r.slug),
	}
}

// waveAttempt is the attempt the whole slice runs at: the decision's own
// attempt raised to one past the highest any of its tasks has already had.
// Retrying from the wave_failures gate clears active_wave, so Decide can
// only offer attempt 1 there, while the tasks themselves remember they have
// already run — and the digest name, the brief name and the model tier all
// key off this number, so it has to be the tasks'.
func waveAttempt(st *bundle.State, ids []int, decided int) int {
	attempt := decided
	for _, id := range ids {
		if t := st.Task(id); t != nil && t.Attempt+1 > attempt {
			attempt = t.Attempt + 1
		}
	}
	return attempt
}

// waveBaseline is the wave's baseline: kept as captured when this is another
// attempt of the same wave (rework or recovery must measure against the same
// tree the wave started from), taken from the copy a wave_failures retry
// parked when that retry cleared active_wave, and freshly captured
// otherwise. takt's own bundle tree is excluded — see bundleTreeRel.
func waveBaseline(ctx context.Context, r *nextRun, waveN int) ([]bundle.BaselineEntry, int, error) {
	if aw := r.st.ActiveWave; aw != nil && aw.N == waveN {
		return aw.Baseline, aw.Slice, nil
	}
	if waveHasRun(r.st, waveN) {
		parked, err := wave.ReadBaseline(r.bdir, waveN)
		if err != nil {
			return nil, 0, err
		}
		if parked != nil {
			return parked, 0, nil
		}
	}
	base, err := wave.Baseline(ctx, r.ws.Repo)
	if err != nil {
		return nil, 0, err
	}
	rel := bundleTreeRel(r.ws)
	return slices.DeleteFunc(base, func(e bundle.BaselineEntry) bool { return underBundle(rel, e.Path) }), 0, nil
}

// waveHasRun reports whether any task of the wave has already been
// dispatched. Only then can a parked baseline belong to this wave rather
// than to an earlier, already-committed run of it.
func waveHasRun(st *bundle.State, waveN int) bool {
	for _, t := range st.Tasks {
		if t.Wave == waveN && t.Attempt > 0 {
			return true
		}
	}
	return false
}

// renderTaskBrief writes one task's brief for this attempt and returns the
// agent that runs it. It stamps the attempt onto the task and drops the
// previous digest, so state never claims a result the new attempt has not
// produced yet.
func renderTaskBrief(r *nextRun, pt *plan.Task, t *bundle.Task, waveN, attempt int) (op.Agent, error) {
	prev, failure, findings := previousAttempt(r.bdir, waveN, t.ID, t.Attempt)
	model := modelForAttempt(r.ws.Cfg, t.Class, attempt, prev)
	text, err := renderImplementer(r, pt, t, attempt, prev, failure, findings)
	if err != nil {
		return op.Agent{}, err
	}
	p := filepath.Join(waveDir(r.bdir, waveN), fmt.Sprintf("task-%d.a%d.md", t.ID, attempt))
	if err = os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return op.Agent{}, err
	}
	if err = os.WriteFile(p, []byte(text), 0o600); err != nil {
		return op.Agent{}, err
	}
	t.Attempt = attempt
	t.LastDigest = nil
	return op.Agent{
		Task: t.ID, Agent: "implementer", Class: t.Class, Model: model, Brief: p,
		Cwd: r.ws.Repo.Root, Label: fmt.Sprintf("task %d: %s", t.ID, pt.Title),
	}, nil
}

// previousAttempt collects what a retry brief needs from the task's last
// attempt: the model it ran on, the failure the close recorded, and the
// review findings to address. All three are empty on a first attempt.
func previousAttempt(bdir string, waveN, task, lastAttempt int) (string, string, []string) {
	if lastAttempt == 0 {
		return "", "", nil
	}
	model := ""
	if d, _ := readDigest(bdir, waveN, task, lastAttempt); d != nil {
		model = d.Model
	}
	failure, findings := previousFailure(bdir, waveN, task)
	return model, failure, findings
}

// previousFailure renders the close record's verdict for one task as the
// sentence its retry brief quotes back, plus that review's findings.
func previousFailure(bdir string, waveN, task int) (string, []string) {
	c, _ := wave.ReadClose(bdir, waveN)
	if c == nil {
		return "", nil
	}
	i := slices.IndexFunc(c.Tasks, func(tr wave.TaskResult) bool { return tr.Task == task })
	if i < 0 {
		return "", nil
	}
	tr := c.Tasks[i]
	var failure strings.Builder
	failure.WriteString(tr.Status)
	if tr.Reason != "" {
		failure.WriteString(": " + tr.Reason)
	}
	for _, v := range tr.Verify {
		if !v.Passed {
			fmt.Fprintf(&failure, " — %s exited %d: %s",
				v.Command, v.Exit, lastLines(v.Tail, verifyTailLines))
		}
	}
	var findings []string
	if tr.Review != nil {
		for _, f := range tr.Review.Findings {
			findings = append(findings,
				fmt.Sprintf("%s %s:%d — %s: %s", f.Severity, f.File, f.Line, f.Title, f.Detail))
		}
	}
	return failure.String(), findings
}

// lastLines keeps the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// renderImplementer renders one task's brief from the embedded template; the
// spec excerpt travels as quoted data, never as instructions (spec §7.4).
func renderImplementer(
	r *nextRun, pt *plan.Task, t *bundle.Task, attempt int, prev, failure string, findings []string,
) (string, error) {
	tok, err := brief.Token()
	if err != nil {
		return "", err
	}
	rel, _ := r.ws.Dir.RelToRepo(r.bdir)
	return brief.Render("implementer", brief.ImplementerData{
		Slug: r.slug, Task: t.ID, Total: len(r.st.Tasks), Title: pt.Title, Description: pt.Description,
		Files: pt.Files, Verify: pt.Verify, Goals: goalLines(r.bdir, pt.Goals),
		SpecExcerpt: readArtifact(r.bdir, "spec.md"), Attempt: attempt, PreviousModel: prev,
		PreviousFailure: failure, Findings: findings, Token: tok, BundleDirRel: rel,
	})
}

// goalLines resolves the goal ids a task serves against goals.md; a run
// without goals simply contributes none.
func goalLines(bdir string, ids []string) []brief.GoalLine {
	b, err := os.ReadFile(filepath.Join(bdir, "goals.md"))
	if err != nil {
		return nil
	}
	g, err := goals.Parse(b)
	if err != nil {
		return nil
	}
	var out []brief.GoalLine
	for _, it := range g.Items {
		if slices.Contains(ids, it.ID) {
			out = append(out, brief.GoalLine{ID: it.ID, Text: it.Text})
		}
	}
	return out
}

// readIndex parses the bundle's plan.index.json.
func readIndex(bdir string) (plan.Index, error) {
	raw, err := os.ReadFile(indexPath(bdir))
	if err != nil {
		return plan.Index{}, err
	}
	return plan.ParseIndex(raw)
}

// waveDir is bundleDir/waves/<n>, where a wave's briefs, digests and close
// record live.
func waveDir(bdir string, n int) string {
	return filepath.Join(bdir, "waves", strconv.Itoa(n))
}

// recoverWave resets the files of the tasks that never reported and
// re-dispatches exactly those at attempt+1, against the same baseline
// (spec §5.4). A task that did report keeps its work and its digest, and
// closes with the wave.
func recoverWave(ctx context.Context, r *nextRun, d decide.Decision) int {
	aw := r.st.ActiveWave
	var files []string
	for _, id := range d.Tasks {
		if t := r.st.Task(id); t != nil {
			files = append(files, t.Files...)
		}
	}
	reset, err := wave.ResetForRecovery(ctx, r.ws.Repo, files, aw.Baseline)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "recovered", map[string]any{
		keyWave: aw.N, keyTasks: d.Tasks, "reset": reset, "from_session": aw.SessionID,
	})
	return launchWave(ctx, r, d)
}
