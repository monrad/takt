package cli

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/goals"
)

// The run steps `takt done` closes (spec §5.2). stepGoals is spelled the
// same as the JSON key, so it aliases keyGoals instead of repeating it.
const (
	stepBrainstorm = "brainstorm"
	stepGoals      = keyGoals
	stepRetro      = "retro"
	stepPushPR     = "push_pr"
)

// stepsHint lists the steps in the usage error for an unknown one.
const stepsHint = "steps: brainstorm, goals, retro, push_pr"

// dispositionPR is the branch_finish choice push_pr belongs to (spec §7.5).
const dispositionPR = "pr"

// doneStep is what closing a step leaves behind: the event `done` appends,
// and the artifact whose hash that event records. push_pr has no artifact —
// a pull request is not a file — so its event alone is the receipt.
type doneStep struct {
	event    string
	artifact string
}

// doneSteps is the receipt each step `done` can close leaves, keyed by step.
var doneSteps = map[string]doneStep{
	stepBrainstorm: {event: "spec_written", artifact: "spec.md"},
	stepGoals:      {event: "goals_frozen", artifact: "goals.md"},
	stepRetro:      {event: stepRetro, artifact: "retro.md"},
	stepPushPR:     {event: "pr_pushed"},
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
	replayed, rerr := doneAlready(tgt.bdir, *step)
	if rerr != nil {
		return fail(env.Stderr, exitError, rerr.Error(), "")
	}
	if replayed {
		return printJSON(env, map[string]any{"step": *step, "ok": true, keyIgnored: true})
	}
	if code = doneStepWork(env, tgt, *step, *prURL); code != 0 {
		return code
	}
	if _, _, err := commitBundle(ctx, tgt.ws, tgt.bdir, tgt.slug, *step+" done"); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{"step": *step, "ok": true})
}

// doneStepWork runs one step's own checks and records its receipt.
func doneStepWork(env Env, tgt *runTarget, step, prURL string) int {
	switch step {
	case stepBrainstorm:
		return doneBrainstorm(env, tgt.bdir)
	case stepGoals:
		return doneGoals(env, tgt.bdir, tgt.st)
	case stepRetro:
		return doneRetro(env, tgt.bdir)
	case stepPushPR:
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
func doneAlready(bdir, step string) (bool, error) {
	d, known := doneSteps[step]
	if !known {
		return false, nil
	}
	events, err := bundle.ReadEvents(bdir)
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
	if d.artifact == "" {
		return true, nil
	}
	h, _ := last.Data[keyHash].(string)
	return h != "" && h == artifactHash(bdir, d.artifact), nil
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
func doneRetro(env Env, bdir string) int {
	if !fileNonEmpty(filepath.Join(bdir, "retro.md")) {
		return fail(env.Stderr, exitError, "retro.md is missing or empty",
			"write the retrospective to "+filepath.Join(bdir, "retro.md")+" first")
	}
	_ = bundle.AppendEvent(bdir, stepRetro, map[string]any{keyHash: artifactHash(bdir, "retro.md")})
	return 0
}

// donePushPR records the pull request the session opened. The URL is the
// only evidence takt has that the push happened, so row 24 keeps asking for
// it until one is recorded (spec §7.5).
func donePushPR(env Env, tgt *runTarget, prURL string) int {
	if tgt.st.Disposition == nil || tgt.st.Disposition.Choice != dispositionPR {
		return fail(env.Stderr, exitError, "push_pr is only valid after choosing the pr disposition",
			"answer the branch_finish gate with --choice pr first")
	}
	if strings.TrimSpace(prURL) == "" {
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
