package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// maxDecideIterations bounds the transition loop inside one `takt next`.
const maxDecideIterations = 8

// nextRun is one `takt next` invocation: the run it drives and the facts
// about this call that Decide needs.
type nextRun struct {
	env     Env
	ws      *workspace
	slug    string
	bdir    string
	st      *bundle.State
	now     time.Time
	session string
	genID   bool // takt invented r.session; nothing persisted it
	force   bool
	recover bool
}

// cmdNext implements the op trampoline (spec §5.1): take the session lock,
// then decide, perform any side effect whose preconditions are now met, and
// return the first real op.
func cmdNext(env Env) int {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run to drive")
	force := fs.Bool("force", false, "take over the session lock")
	recoverFlag := fs.Bool("recover", false, "treat an unrecorded wave as crashed")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	// An archived run has nothing left to decide (spec §5.3 row 26), and no
	// lock is taken: acquireLock would stamp a fresh holder on the run takt
	// just released and rewrite state.json — a tracked file — leaving the
	// worktree dirty with nothing to commit, every time anyone asks a
	// finished run what is next. What the disposition asked of git is
	// re-derived instead of remembered, so an archive whose merge could not
	// be made — the primary worktree was busy, the merge conflicted — makes
	// it here on a later call, and one that is fully done says so and does
	// nothing.
	if tgt.st.Phase == bundle.PhaseArchived {
		return applyAndStop(ctx, env, tgt)
	}
	id, generated := sessionID(env.Getenv)
	r := &nextRun{
		env: env, ws: tgt.ws, slug: tgt.slug, bdir: tgt.bdir, st: tgt.st, now: timeNow(),
		session: id, genID: generated, force: *force, recover: *recoverFlag,
	}
	if lockCode, done := r.acquireLock(); done {
		return lockCode
	}
	return r.loop(ctx)
}

// acquireLock refreshes or takes the advisory lock; a live other session
// yields the owner ask (not persisted — it is transient). A holder that
// recorded generated=true is not a live session: nothing persisted its id,
// so it can never present it again and must not hold the run for a whole
// lock_ttl. That is read off the holder's own record, never guessed from
// the shape of its id (spec §4.6, review finding 1).
func (r *nextRun) acquireLock() (int, bool) {
	host, _ := os.Hostname()
	who := bundle.Identity{ID: r.session, Host: host, Generated: r.genID}
	prev := r.st.Session
	orphaned := prev != nil && prev.ID != r.session && prev.Generated
	outcome := bundle.Acquire(r.st, who, r.now, time.Duration(r.ws.Cfg.LockTTL), r.force || orphaned)
	if outcome == bundle.LockBlocked {
		q := decide.Question("owner", map[string]any{
			keySlug: r.slug, "holder": r.st.Session.ID, "host": r.st.Session.Host,
			"heartbeat": r.st.Session.Heartbeat.Format(time.RFC3339),
		})
		return printOp(r.env, q), true
	}
	// LockKept means the refreshed heartbeat is not worth a write of its
	// own: this session already holds the lock and the recorded heartbeat is
	// still current. Saving anyway would rewrite state.json — a tracked file
	// — on every `takt next`, so a call that only reads the run (a repeated
	// op, a `stop`) would leave the worktree dirty with nothing to commit.
	// The refreshed heartbeat is still in r.st, so any later write in this
	// call — a launch, a transition, a gate — carries it.
	if outcome != bundle.LockKept {
		if err := bundle.SaveState(r.bdir, r.st); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), ""), true
		}
	}
	// Every takeover is recorded: an expired heartbeat, an explicit --force,
	// and the silent takeover of a holder that recorded generated=true and
	// can therefore never come back. The last one is invisible to the user
	// by design, which is exactly why it belongs in the log — spec §4.6 has
	// takt record recovery as an event rather than repairing state silently
	// (review M7).
	switch {
	case outcome == bundle.LockStolen, outcome == bundle.LockForced && r.force:
		_ = bundle.AppendEvent(r.bdir, "lock_taken", map[string]any{
			"session": r.session, "outcome": string(outcome),
		})
	case outcome == bundle.LockForced && orphaned:
		_ = bundle.AppendEvent(r.bdir, "lock_taken", map[string]any{
			"session": r.session, "outcome": string(outcome), keyReason: "orphaned",
		})
	}
	return 0, false
}

// loop decides, performs the side effects whose preconditions are met, and
// returns as soon as one op is printed (spec §5.3 rows 7, 12, 19).
func (r *nextRun) loop(ctx context.Context) int {
	for range maxDecideIterations {
		facts, err := gatherFacts(ctx, r.ws, r.bdir, r.st, r.force, r.recover, r.now, r.waveSession())
		if err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
		d, err := decide.Decide(r.st, facts)
		if err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "run `takt doctor`")
		}
		switch d.Action {
		case decide.ActTransition:
			if code := r.transition(ctx, d.Phase); code != 0 {
				return code
			}
		case decide.ActLoadPlan:
			if code := r.loadPlan(ctx); code != 0 {
				return code
			}
		case decide.ActClearWave:
			if code := r.clearWave(ctx, d.Wave); code != 0 {
				return code
			}
		case decide.ActLaunch:
			return launchWave(ctx, r, d)
		case decide.ActRecover:
			return recoverWave(ctx, r, d)
		case decide.ActDispatch:
			return r.dispatchAgent(ctx, d)
		case decide.ActAsk:
			return r.ask(*d.Op)
		case decide.ActRun:
			return r.run(*d.Op)
		case decide.ActExec, decide.ActStop:
			return printOp(r.env, *d.Op)
		case decide.ActArchive:
			return r.archive(ctx)
		default:
			return fail(r.env.Stderr, exitError, "unknown decision "+string(d.Action), "")
		}
	}
	return fail(r.env.Stderr, exitError, "decide loop did not converge", "run `takt doctor`")
}

// waveSession is the session id the wave-liveness check (spec §5.3 row 13)
// compares the active wave's dispatcher against. When takt had to invent
// this process's id — nothing set CLAUDE_CODE_SESSION_ID or TAKT_SESSION —
// two calls of one real session are indistinguishable, so an in-flight wave
// is taken to be this session's own and `next` waits: waiting for an agent
// that is still working costs a turn, while recovering resets its files and
// cannot be undone. `takt next --recover` forces the other reading.
func (r *nextRun) waveSession() string {
	if r.genID && r.st.ActiveWave != nil {
		return r.st.ActiveWave.SessionID
	}
	return r.session
}

// transition records a phase change and commits it (spec §5.3 rows 7, 19).
// Leaving brainstorm for plan is exactly the transition that follows the
// spec gate passing, so this is where state.gates records it durably —
// `takt status`'s live gate.Compute reflects the receipt from the moment it
// is written, but state.gates is what a doctor index-staleness check
// compares a later edit against (spec §11).
func (r *nextRun) transition(ctx context.Context, to string) int {
	from := r.st.Phase
	r.st.Phase = to
	if to == bundle.PhasePlan {
		r.st.Gates[gate.Spec] = gateStateValue(r.bdir, r.st.Config.Review.Spec, gate.Spec)
	}
	if err := bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "phase", map[string]any{"from": from, "to": to})
	if _, _, err := commitBundle(ctx, r.ws, r.bdir, r.slug, from+" → "+to); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	return 0
}

// clearWave drops a wave whose close record says it was committed — but
// only once git agrees the commit is really in HEAD. The record is written
// before `git commit` runs, so a crash inside the commit leaves
// committed:true with no sha; clearing the wave on that word alone would
// strand the work uncommitted with nothing left pointing at it (review I2,
// spec §5.4). When the claim does not hold, the record is retired instead
// and the next turn of the loop re-issues `exec close-wave`, which re-grades
// nothing (the .prev carry-forward) and commits.
func (r *nextRun) clearWave(ctx context.Context, n int) int {
	aw := r.st.ActiveWave
	if aw == nil {
		return 0
	}
	c, err := wave.ReadClose(r.bdir, n, sliceOf(aw))
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	if !closeMatchesDispatch(c, aw) || !waveCommitLanded(ctx, r.ws.Repo, c) {
		if err = dropClose(r.bdir, n, sliceOf(aw)); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
		_ = bundle.AppendEvent(r.bdir, "wave_close_unreconciled", map[string]any{
			keyWave: n, keyReason: "the close record claims a commit that is not in HEAD",
		})
		return 0
	}
	r.st.ActiveWave = nil
	if err = bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "wave_cleared", map[string]any{keyWave: n})
	return 0
}

// loadPlan materialises state.tasks from the validated index, writes the
// waves back for display, and moves to execute (spec §7.3 Load).
func (r *nextRun) loadPlan(ctx context.Context) int {
	raw, err := os.ReadFile(filepath.Join(r.bdir, "plan.index.json"))
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	idx, err := plan.ParseIndex(raw)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	if len(idx.Tasks) == 0 {
		return fail(r.env.Stderr, exitError, "plan.index.json has no tasks", "re-run the planner")
	}
	waves, err := plan.AssignWaves(idx)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	maxWave := r.materialiseTasks(idx, waves)
	if err = writeIndex(r.bdir, idx); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	r.st.Phase = bundle.PhaseExecute
	r.st.Gates[gate.Plan] = gateStateValue(r.bdir, r.st.Config.Review.Plan, gate.Plan)
	if err = bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "plan_loaded", map[string]any{"tasks": len(idx.Tasks), keyWaves: maxWave + 1})
	_ = bundle.AppendEvent(r.bdir, "phase", map[string]any{"from": bundle.PhasePlan, "to": bundle.PhaseExecute})
	msg := loadCommitMessage(r.bdir, r.slug, len(idx.Tasks), maxWave+1)
	if _, _, err = commitBundle(ctx, r.ws, r.bdir, r.slug, msg); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	return 0
}

// commitSubjectSoftLimit is the approximate rune budget for the load
// commit's git subject line before the alignment summary moves to the
// commit body instead (spec §7.3).
const commitSubjectSoftLimit = 100

// loadCommitMessage builds the plan→execute commit message: the existing
// "plan → execute (n tasks, w waves)" subject, plus — once the alignment
// audit has recorded verdicts — " — alignment: " and the same
// contraction/creep summary `status` shows after its own "alignment: "
// label, reusing statusAlignment/alignmentLine rather than re-deriving the
// bucketing (spec §7.3: "...are reported as contraction, widened as creep —
// in the load commit message and in status"), e.g.
// "plan → execute (2 tasks, 2 waves) — alignment: 1 covered, 1 narrowed
// (contraction: A2)". No verdict artifacts (the audit is disabled, skipped,
// or has not run yet) leaves the message unchanged. Appending the summary
// would sometimes push the subject well past a readable git subject line,
// so once the one-line form (with the "takt(<slug>): " prefix commitBundle
// adds) would exceed commitSubjectSoftLimit runes, the subject stays just
// "plan → execute (…)" and "alignment: <summary>" becomes the commit
// body's first line instead.
func loadCommitMessage(bdir, slug string, tasks, waves int) string {
	subject := fmt.Sprintf("plan → execute (%d tasks, %d waves)", tasks, waves)
	align := ""
	if a := statusAlignment(bdir); a != nil {
		align = alignmentLine(a)
	}
	if align == "" {
		return subject
	}
	oneLine := subject + " — alignment: " + align
	if utf8.RuneCountInString("takt("+slug+"): "+oneLine) <= commitSubjectSoftLimit {
		return oneLine
	}
	return subject + "\n\nalignment: " + align
}

// materialiseTasks replaces state.tasks with the index's tasks, stamps each
// index task with its computed wave for display, and returns the last wave.
func (r *nextRun) materialiseTasks(idx plan.Index, waves map[int]int) int {
	r.st.Tasks = r.st.Tasks[:0]
	maxWave := 0
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		w := waves[t.ID]
		t.Wave = new(w)
		if w > maxWave {
			maxWave = w
		}
		r.st.Tasks = append(r.st.Tasks, bundle.Task{
			ID: t.ID, Wave: w, Status: bundle.StatusPending,
			Files: append([]string{}, t.Files...), Class: t.Class,
		})
	}
	return maxWave
}

// dispatchAgent renders the planner / auditor brief and prints the op.
func (r *nextRun) dispatchAgent(ctx context.Context, d decide.Decision) int {
	tok, err := brief.Token()
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	ag := *d.Agent
	ag.Cwd = r.ws.Repo.Root
	var text, name string
	switch ag.Agent {
	case "planner":
		text, name, err = r.plannerBrief(ctx, &ag, tok)
	case "alignment-auditor":
		text, name, err = r.auditorBrief(&ag, tok)
	case "goal-assessor":
		text, name, err = r.assessorBrief(ctx, &ag, tok)
	default:
		return fail(r.env.Stderr, exitError, "unknown agent "+ag.Agent, "")
	}
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	p := briefPath(r.bdir, name)
	if err = os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	if err = os.WriteFile(p, []byte(text), 0o600); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	ag.Brief = p
	record := fmt.Sprintf("takt record --agent %s --from <file> --slug %s", ag.Agent, r.slug)
	if ag.Mode != "" {
		record += " --mode " + ag.Mode
	}
	return printOp(r.env, op.Op{Op: op.Dispatch, Narration: ag.Label, Agents: []op.Agent{ag}, Record: record})
}

// plannerBrief pins the planner's model and renders its brief, appending the
// problems of the previous attempt when there was one (spec §5.3 row 8).
func (r *nextRun) plannerBrief(ctx context.Context, ag *op.Agent, tok string) (string, string, error) {
	ag.Model = r.ws.Cfg.Agents.Planner.Model
	ag.Label = "plan the run"
	facts, err := gatherFacts(ctx, r.ws, r.bdir, r.st, false, false, r.now, r.session)
	if err != nil {
		return "", "", err
	}
	attempt := facts.PlanAttempts + 1
	text, err := brief.Render("planner", brief.PlannerData{
		Slug: r.slug, Topic: r.st.Topic, SpecText: readArtifact(r.bdir, "spec.md"),
		GoalsText: readArtifact(r.bdir, "goals.md"), Schema: plannerSchema, RepoRoot: r.ws.Repo.Root,
		Token: tok, MaxFiles: r.ws.Cfg.MaxFilesPerTask, Problems: facts.IndexProblems, Attempt: attempt,
	})
	if err != nil {
		return "", "", err
	}
	return text, fmt.Sprintf("planner.a%d.md", attempt), nil
}

// auditorBrief pins the auditor's model and renders the brief for its mode;
// the verdicts pass also carries the confirmed clauses and the plan.
func (r *nextRun) auditorBrief(ag *op.Agent, tok string) (string, string, error) {
	ag.Model = r.ws.Cfg.Agents.AlignmentAuditor.Model
	data := brief.AlignmentData{Mode: ag.Mode, Anchor: r.st.Topic, Token: tok}
	if ag.Mode == "verdicts" {
		if a, err := readAlignment(r.bdir); err == nil && a != nil {
			data.Clauses = a.Clauses
		}
		data.SpecText = readArtifact(r.bdir, "spec.md")
		data.PlanText = readArtifact(r.bdir, "plan.md")
		data.IndexText = readArtifact(r.bdir, "plan.index.json")
	}
	text, err := brief.Render("alignment-"+ag.Mode, data)
	if err != nil {
		return "", "", err
	}
	return text, "alignment-" + ag.Mode + ".md", nil
}

// assessorBrief renders goal-assessor.md from goals.md, the base..HEAD
// diff stat and the verify record (spec §7.5 step 2).
func (r *nextRun) assessorBrief(ctx context.Context, ag *op.Agent, tok string) (string, string, error) {
	gb, err := os.ReadFile(filepath.Join(r.bdir, "goals.md"))
	if err != nil {
		return "", "", err
	}
	g, err := goals.Parse(gb)
	if err != nil {
		return "", "", err
	}
	stat, err := r.ws.Repo.DiffStat(ctx, r.st.BaseSHA, "HEAD")
	if err != nil {
		return "", "", err
	}
	rec, err := finish.ReadVerify(r.bdir)
	if err != nil {
		return "", "", err
	}
	ag.Model = r.ws.Cfg.Agents.GoalAssessor.Model
	ag.Label = "assess the goals at HEAD"
	lines := make([]brief.GoalLine, 0, len(g.Items))
	for _, it := range g.Items {
		lines = append(lines, brief.GoalLine{ID: it.ID, Text: it.Text})
	}
	text, err := brief.Render("goal-assessor", brief.GoalAssessorData{
		Slug: r.slug, Token: tok, GoalsText: string(gb), DiffStat: stat,
		VerifySummary: verifySummary(rec), Goals: lines,
	})
	return text, "goal-assessor.md", err
}

// verifySummary is one line per verify command for the assessor.
func verifySummary(rec *finish.VerifyRecord) string {
	if rec == nil {
		return "(no verification record)"
	}
	var b strings.Builder
	for _, res := range rec.Results {
		outcome := "FAIL"
		if res.Passed {
			outcome = "pass"
		}
		fmt.Fprintf(&b, "%s → exit %d (%s)\n", res.Command, res.Exit, outcome)
	}
	if rec.Overridden != "" {
		fmt.Fprintf(&b, "overridden by the user: %s\n", rec.Overridden)
	}
	if rec.Skipped {
		b.WriteString("no verify commands; the user proceeded without verification\n")
	}
	return b.String()
}

// ask persists the gate on first rendering, then prints it; a re-rendered
// gate comes from the stored payload and is byte-identical (spec §4.3).
func (r *nextRun) ask(o op.Op) int {
	if r.st.PendingGate == nil || r.st.PendingGate.ID != o.Gate {
		if err := openGate(r.bdir, r.st, o, r.now); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
	}
	return printOp(r.env, o)
}

// run fills a run op's instructions from the step's template (spec §5.2)
// and adds whatever else that step needs to do its work.
func (r *nextRun) run(o op.Op) int {
	data := brief.RunData{
		Slug: r.slug, Topic: r.st.Topic,
		SpecPath: filepath.Join(r.bdir, "spec.md"), GoalsPath: filepath.Join(r.bdir, "goals.md"),
		Branch: r.st.Branch, Base: r.st.Base,
		RetroPath: filepath.Join(r.bdir, "retro.md"), InputsPath: finish.RetroInputsPath(r.bdir),
	}
	inputs := map[string]any{
		keySlug: r.slug, "topic": r.st.Topic, "spec_path": data.SpecPath, "goals_path": data.GoalsPath,
	}
	switch o.Step {
	case stepRetro:
		// The inputs are re-derived on every call that emits this op: they
		// are a pure function of what is on disk, so a repeated `next`
		// writes the same bytes and hands back the same op (spec §5.4).
		if err := r.writeRetroInputs(); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
		inputs["inputs_path"] = data.InputsPath
		inputs["retro_path"] = data.RetroPath
	case stepPushPR:
		inputs[keyBranch] = data.Branch
		inputs[keyBase] = data.Base
		o.Done = "takt done --step " + stepPushPR + " --url <pr-url> --slug " + r.slug
	}
	text, err := brief.Render("run-"+o.Step, data)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	o.Instructions = text
	o.Inputs = inputs
	return printOp(r.env, o)
}

// writeRetroInputs re-derives finish/retro-inputs.json from the run's own
// records, so the retro op always names a file that describes the run as it
// stands (spec §7.5 step 3).
func (r *nextRun) writeRetroInputs() error {
	idx, err := readIndex(r.bdir)
	if err != nil {
		return err
	}
	events, err := bundle.ReadEvents(r.bdir)
	if err != nil {
		return err
	}
	closes, err := readCloses(r.bdir, r.st.Tasks)
	if err != nil {
		return err
	}
	v, err := finish.ReadVerify(r.bdir)
	if err != nil {
		return err
	}
	g, err := finish.ReadGoals(r.bdir)
	if err != nil {
		return err
	}
	return finish.WriteRetroInputs(r.bdir, finish.BuildRetroInputs(r.st, idx, events, closes, v, g))
}

// readCloses collects every slice record of every wave the run has tasks in,
// in wave then slice order; a wave that never wrote one is skipped rather
// than reported, because a run can reach finish with a wave whose tasks were
// all waived. A sliced wave contributes one record per slice, and the retro
// wants all of them: each slice graded different tasks.
func readCloses(bdir string, tasks []bundle.Task) ([]wave.CloseResult, error) {
	var waves []int
	for _, t := range tasks {
		if !slices.Contains(waves, t.Wave) {
			waves = append(waves, t.Wave)
		}
	}
	slices.Sort(waves)
	out := make([]wave.CloseResult, 0, len(waves))
	for _, n := range waves {
		all, err := wave.AllCloses(bdir, n)
		if err != nil {
			return nil, err
		}
		out = append(out, all...)
	}
	return out, nil
}

// plannerSchema is quoted into the planner brief (spec §7.3).
const plannerSchema = `{ "schema": 1, "spec_hash": "sha256:<sha256 of spec.md>", "tasks": [ { "id": 1, ` +
	`"title": "…", "description": "…", "files": ["path/relative/to/repo"], "verify": ["go test ./pkg/..."], ` +
	`"depends_on": [], "goals": ["G1"], "class": "implement" } ] }`

// reviewerFor selects the configured reviewer and its backend settings.
func reviewerFor(ws *workspace, env Env) (backend.Reviewer, config.Backend, error) {
	reg := backend.Registry(env.Getenv)
	r, err := backend.Select(context.Background(), ws.Cfg.Backends.Reviewer, reg)
	if err != nil {
		return nil, config.Backend{}, err
	}
	switch r.Name() {
	case "copilot":
		return r, ws.Cfg.Backends.Copilot, nil
	case "claude":
		return r, ws.Cfg.Backends.Claude, nil
	}
	return r, config.Backend{Model: "fake", Effort: "low", Timeout: config.Duration(time.Minute)}, nil
}
