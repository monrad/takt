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

// cmdGoals implements `takt goals amend` (spec §5.1): re-freeze an edited
// goals.md. The new hash re-arms the spec gate, because goals.md is one of
// the spec gate's hashed artifacts (spec §9).
func cmdGoals(env Env) int {
	if len(env.Args) == 0 || env.Args[0] != "amend" {
		return fail(env.Stderr, exitUsage, "usage: takt goals amend", "")
	}
	fs := flag.NewFlagSet("goals amend", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	if _, err := parseInterspersed(fs, env.Args[1:]); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	b, err := os.ReadFile(filepath.Join(tgt.bdir, "goals.md"))
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	g, err := goals.Parse(b)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if strings.TrimSpace(g.Anchor) != strings.TrimSpace(tgt.st.Topic) {
		return fail(env.Stderr, exitError, "an amendment must not change the anchor",
			"restore the ## Anchor block to the run's topic verbatim")
	}
	old := ""
	if tgt.st.GoalsHash != nil {
		old = *tgt.st.GoalsHash
	}
	h := goals.Hash(b)
	tgt.st.GoalsHash = &h
	if err = bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(tgt.bdir, "goals_amended", map[string]any{"old": old, "new": h, keyCount: len(g.Items)})
	if _, _, err = commitBundle(ctx, tgt.ws, tgt.bdir, tgt.slug, "goals amended"); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{keyGoals: len(g.Items), keyHash: h, "spec_gate": "re-armed"})
}
