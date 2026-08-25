package cli

import (
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/plan"
)

func cmdPlan(env Env) int {
	if len(env.Args) == 0 || env.Args[0] != "validate" {
		return fail(env.Stderr, exitUsage, "usage: takt plan validate [path]", "")
	}
	ws, bdir, path, code := planValidateTarget(env)
	if code != 0 {
		return code
	}
	return runPlanValidate(env, ws, bdir, path)
}

// planValidateTarget parses `plan validate`'s flags and resolves the
// workspace, the selected bundle dir, and the plan.index.json path to read
// — the bundle's own unless a positional path override is given.
func planValidateTarget(env Env) (*workspace, string, string, int) {
	fs := flag.NewFlagSet("plan validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slug := fs.String("slug", "", "run whose plan to validate")
	positional, err := parseInterspersed(fs, env.Args[1:])
	if err != nil {
		return nil, "", "", usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	ws, err := openWorkspace(ctx, env, *dirFlag)
	if err != nil {
		return nil, "", "", fail(env.Stderr, 1, err.Error(), workspaceHint)
	}
	s, err := selectSlug(ws, *slug)
	if err != nil {
		return nil, "", "", failSelectSlug(env, err)
	}
	bdir := ws.Dir.Bundle(s)
	path := filepath.Join(bdir, "plan.index.json")
	if len(positional) > 0 {
		path = positional[0]
	}
	return ws, bdir, path, 0
}

// runPlanValidate reads and validates the plan at path, printing the JSON
// document to stdout regardless of validity and returning the process exit
// code (0 valid, 1 invalid — spec §9).
func runPlanValidate(env Env, ws *workspace, bdir, path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	idx, err := plan.ParseIndex(raw)
	if err != nil {
		_ = writeJSON(env.Stdout, map[string]any{
			"valid": false, "problems": []string{err.Error()}, keyTasks: 0, "waves": map[string]int{},
		})
		return 1
	}
	problems := plan.Validate(idx, validateOpts(ws, bdir))
	doc := map[string]any{
		"valid": len(problems) == 0, "problems": problemStrings(problems),
		keyTasks: len(idx.Tasks), "waves": planWaves(idx),
	}
	if werr := writeJSON(env.Stdout, doc); werr != nil {
		return 1
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}

// problemStrings renders each plan.Problem as its display string.
func problemStrings(problems []plan.Problem) []string {
	msgs := make([]string, 0, len(problems))
	for _, p := range problems {
		msgs = append(msgs, p.String())
	}
	return msgs
}

// planWaves computes the wave assignment for the --json document, keyed by
// task id as a string; a dependency cycle (already reported among problems
// by plan.Validate) leaves it empty rather than failing here.
func planWaves(idx plan.Index) map[string]int {
	waves := map[string]int{}
	w, err := plan.AssignWaves(idx)
	if err != nil {
		return waves
	}
	for id, n := range w {
		waves[strconv.Itoa(id)] = n
	}
	return waves
}

// validateOpts assembles the context plan.Validate needs from the bundle.
func validateOpts(ws *workspace, bdir string) plan.ValidateOpts {
	o := plan.ValidateOpts{
		RepoRoot:        ws.Repo.Root,
		MaxFilesPerTask: ws.Cfg.MaxFilesPerTask,
		LookPath:        func(tok string) bool { _, err := exec.LookPath(tok); return err == nil },
	}
	if b, err := os.ReadFile(filepath.Join(bdir, "spec.md")); err == nil {
		o.SpecHash = goals.Hash(b)
	}
	if b, err := os.ReadFile(filepath.Join(bdir, "goals.md")); err == nil {
		if g, gerr := goals.Parse(b); gerr == nil {
			o.GoalIDs = g.IDs()
		}
	}
	return o
}
