package cli

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/op"
)

// stepsHint lists the steps in the usage error for an unknown one.
var stepsHint = "steps: " + strings.Join(op.Steps(), ", ")

// dispositionPR is the branch_finish choice push_pr belongs to (spec §7.5).
const dispositionPR = "pr"

// doneStep is what closing a step leaves behind: the event `done` appends,
// and the artifact whose hash that event records. push_pr has no file, so
// its artifact is empty and the pull request URL on the disposition stands
// in for the hash (see pushPRRecorded).
type doneStep struct {
	event    string
	artifact string
}

// doneSteps is the receipt each step `done` can close leaves, keyed by step.
var doneSteps = map[string]doneStep{
	op.StepBrainstorm: {event: "spec_written", artifact: "spec.md"},
	op.StepGoals:      {event: "goals_frozen", artifact: "goals.md"},
	op.StepRetro:      {event: op.StepRetro, artifact: "retro.md"},
	op.StepPushPR:     {event: "pr_pushed"},
}

// cmdDone marks an LLM-side `run` step complete (spec §5.1). For `goals` it
// also freezes goals.md, which is what re-arms the spec gate on every later
// edit (spec §7.2). Closing a step that is already closed against the same
// artifact changes nothing and commits nothing (spec §5.4).
func cmdDone(env Env) int {
	fs := flag.NewFlagSet("done", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	step := fs.String("step", "", "brainstorm | goals | retro | push_pr")
	prURL := fs.String("url", "", "pull request URL (push_pr)")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	// The retro's own checks run before the replay decision, not inside
	// doneStepWork with the others: a retro.md that hashes to what an
	// earlier `done` recorded still has to be refused in the wrong phase,
	// and a run that recorded a skeleton verbatim before the prose-slot
	// guard existed must not be handed an `ignored` receipt for it. Every
	// other step's checks are implied by its own replay condition — a
	// matching hash means the artifact is there, and a recorded pull request
	// URL means the disposition names one.
	if *step == op.StepRetro {
		if code = doneRetroChecks(env, tgt); code != 0 {
			return code
		}
	}
	replayed, rerr := doneAlready(tgt, *step, *prURL)
	if rerr != nil {
		return fail(env.Stderr, exitError, rerr.Error(), "")
	}
	if replayed {
		return printJSON(env, map[string]any{keyStep: *step, "ok": true, keyIgnored: true})
	}
	if code = doneStepWork(env, tgt, *step, *prURL); code != 0 {
		return code
	}
	if _, _, err := commitBundle(ctx, tgt.ws, tgt.bdir, tgt.slug, *step+" done"); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{keyStep: *step, "ok": true})
}

// doneStepWork runs one step's own checks and records its receipt. The
// retro is the exception: cmdDone has already run its checks by the time
// this is reached, because they must hold for a replay too.
func doneStepWork(env Env, tgt *runTarget, step, prURL string) int {
	switch step {
	case op.StepBrainstorm:
		return doneBrainstorm(env, tgt.bdir)
	case op.StepGoals:
		return doneGoals(env, tgt.bdir, tgt.st)
	case op.StepRetro:
		return doneRetro(tgt)
	case op.StepPushPR:
		return donePushPR(env, tgt, prURL)
	}
	return fail(env.Stderr, exitUsage, "unknown step "+step, stepsHint)
}

// doneAlready reports whether this call replays a `done` that already
// landed: the step's event is on the log and the artifact still hashes to
// what that event recorded, so redoing the work would append a second
// receipt and take an empty commit (spec §5.4). Editing the artifact and
// closing the step again is a new done, not a replay, which is what keeps
// this compatible with the steps a session may legitimately redo.
func doneAlready(tgt *runTarget, step, prURL string) (bool, error) {
	d, known := doneSteps[step]
	if !known {
		return false, nil
	}
	if d.artifact == "" {
		return pushPRRecorded(tgt.st, prURL), nil
	}
	events, err := bundle.ReadEvents(tgt.bdir)
	if err != nil {
		return false, err
	}
	var last *bundle.Event
	for i := range events {
		if events[i].Type == d.event {
			last = &events[i]
		}
	}
	if last == nil {
		return false, nil
	}
	h, _ := last.Data[keyHash].(string)
	return h != "" && h == artifactHash(tgt.bdir, d.artifact), nil
}

// pushPRRecorded reports whether the disposition already names this exact
// pull request. push_pr has no file, so the URL the session passes in is
// its artifact: the same URL is a replay, and a different one — a re-opened
// or replaced pull request — is a new done, exactly as an edited spec.md
// is. The comparison is against the URL on the disposition rather than the
// one in the last pr_pushed event because the disposition is what row 24
// reads to decide the push happened: a state that lost the URL has to be
// allowed to record it again.
func pushPRRecorded(st *bundle.State, prURL string) bool {
	if st.Disposition == nil || st.Disposition.PRURL == "" {
		return false
	}
	return st.Disposition.PRURL == strings.TrimSpace(prURL)
}

// artifactHash is the hash a done event records for its step's artifact. A
// file that cannot be read hashes to "", which never equals a recorded
// hash — reporting a missing artifact is the step's own check to make.
func artifactHash(bdir, name string) string {
	b, err := os.ReadFile(filepath.Join(bdir, name))
	if err != nil {
		return ""
	}
	return goals.Hash(b)
}

// doneBrainstorm records that the approved spec was written.
func doneBrainstorm(env Env, bdir string) int {
	if !fileNonEmpty(filepath.Join(bdir, "spec.md")) {
		return fail(env.Stderr, exitError, "spec.md is missing or empty",
			"write the approved spec to "+filepath.Join(bdir, "spec.md")+" first")
	}
	_ = bundle.AppendEvent(bdir, "spec_written", map[string]any{keyHash: artifactHash(bdir, "spec.md")})
	return 0
}

// doneGoals freezes goals.md after checking its anchor is the run's topic
// verbatim — the anchor is what the finish-time audit measures drift against.
func doneGoals(env Env, bdir string, st *bundle.State) int {
	b, err := os.ReadFile(filepath.Join(bdir, "goals.md"))
	if err != nil {
		return fail(env.Stderr, exitError, "goals.md is missing", "write it to "+filepath.Join(bdir, "goals.md"))
	}
	g, err := goals.Parse(b)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if strings.TrimSpace(g.Anchor) != strings.TrimSpace(st.Topic) {
		return fail(env.Stderr, exitError, "goals.md anchor does not match the run's topic verbatim",
			"copy the topic from state.json into the ## Anchor block exactly")
	}
	h := goals.Hash(b)
	st.GoalsHash = &h
	if err = bundle.SaveState(bdir, st); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(bdir, "goals_frozen", map[string]any{keyHash: h, keyCount: len(g.Items)})
	return 0
}

// doneRetro records the retrospective the session wrote (spec §7.5 step 3).
// The checks it needs are doneRetroChecks, which cmdDone has already run —
// they guard a replay as well as a first recording, so they cannot live
// here. doneAlready still hash-compares retro.md, so a rewritten one
// re-records on an archived run as an ordinary bundle commit and an
// unchanged one commits nothing (design §7.5 step 5 already contemplates
// post-archive bundle writes).
func doneRetro(tgt *runTarget) int {
	_ = bundle.AppendEvent(tgt.bdir, op.StepRetro, map[string]any{keyHash: artifactHash(tgt.bdir, "retro.md")})
	return 0
}

// doneRetroChecks refuses a `done --step retro` that must not be recorded.
// The phase is checked before the file, so a session that runs this early is
// told what is actually wrong rather than being sent to write a
// retrospective row 22 has not asked for yet (review M2). The archived phase
// is allowed alongside finish, so a `takt retro --rewrite` months later can
// record the rewritten retrospective the same way (spec §7). The prose-slot
// guard exists because the skeleton introduces the copy-it-verbatim failure
// mode: a retro.md still carrying a `<!-- prose: … -->` marker has recorded
// the render, not an account of the run.
func doneRetroChecks(env Env, tgt *runTarget) int {
	if code := finishOrArchivedOnly(env, tgt.st, "done --step "+op.StepRetro); code != 0 {
		return code
	}
	p := filepath.Join(tgt.bdir, "retro.md")
	b, err := os.ReadFile(p)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return fail(env.Stderr, exitError, "retro.md is missing or empty",
			"write the retrospective to "+p+" first")
	}
	if slot, ok := unfilledProseSlot(b); ok {
		return fail(env.Stderr, exitError,
			"retro.md still contains an unfilled prose slot: "+slot,
			"fill every `<!-- prose: … -->` slot the skeleton rendered")
	}
	return 0
}

// unfilledProseSlot reports the first `<!-- prose: … -->` marker still in b,
// verbatim through its closing `-->`, so the error names the exact slot a
// session left unfilled rather than only the fact that one remains. A marker
// an edit broke open has no closing `-->`; the slot is then the rest of its
// line, which still names it without pasting the tail of the file into an
// error message.
func unfilledProseSlot(b []byte) (string, bool) {
	const marker = "<!-- prose:"
	s := string(b)
	i := strings.Index(s, marker)
	if i < 0 {
		return "", false
	}
	rest := s[i:]
	if j := strings.Index(rest, "-->"); j >= 0 {
		return rest[:j+len("-->")], true
	}
	line, _, _ := strings.Cut(rest, "\n")
	return line, true
}

// donePushPR records the pull request the session opened. The URL is the
// only evidence takt has that the push happened, so row 24 keeps asking for
// it until one is recorded, and re-running the step with a different URL
// replaces the one on the record (spec §7.5).
func donePushPR(env Env, tgt *runTarget, prURL string) int {
	if tgt.st.Disposition == nil || tgt.st.Disposition.Choice != dispositionPR {
		return fail(env.Stderr, exitError, "push_pr is only valid after choosing the pr disposition",
			"answer the branch_finish gate with --choice pr first")
	}
	prURL = strings.TrimSpace(prURL)
	if prURL == "" {
		return fail(env.Stderr, exitUsage, "--url is required",
			"pass the pull request URL: takt done --step push_pr --url <pr-url>")
	}
	tgt.st.Disposition.PRURL = prURL
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(tgt.bdir, "pr_pushed", map[string]any{"url": prURL})
	return 0
}
