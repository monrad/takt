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
)

// cmdDone marks an LLM-side `run` step complete (spec §5.1). For `goals` it
// also freezes goals.md, which is what re-arms the spec gate on every later
// edit (spec §7.2).
func cmdDone(env Env) int {
	fs := flag.NewFlagSet("done", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	step := fs.String("step", "", "brainstorm | goals")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	switch *step {
	case stepBrainstorm:
		if code = doneBrainstorm(env, tgt.bdir); code != 0 {
			return code
		}
	case stepGoals:
		if code = doneGoals(env, tgt.bdir, tgt.st); code != 0 {
			return code
		}
	default:
		return fail(env.Stderr, exitUsage, "unknown step "+*step, "steps: brainstorm, goals")
	}
	if _, _, err := commitBundle(ctx, tgt.ws, tgt.bdir, tgt.slug, *step+" done"); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{"step": *step, "ok": true})
}

// doneBrainstorm records that the approved spec was written.
func doneBrainstorm(env Env, bdir string) int {
	if !fileNonEmpty(filepath.Join(bdir, "spec.md")) {
		return fail(env.Stderr, exitError, "spec.md is missing or empty",
			"write the approved spec to "+filepath.Join(bdir, "spec.md")+" first")
	}
	_ = bundle.AppendEvent(bdir, "spec_written", nil)
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
