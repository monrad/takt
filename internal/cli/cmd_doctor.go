package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/monrad/takt/internal/doctor"
	"github.com/monrad/takt/internal/plan"
)

func cmdDoctor(env Env) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	all := fs.Bool("all", false, "include archived bundles")
	asJSON := fs.Bool("json", false, "print findings as JSON")
	if err := fs.Parse(env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	ws, err := openWorkspace(ctx, env, *dirFlag)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), workspaceHint)
	}
	findings := doctor.Run(ctx, ws.Dir, *all, doctor.Default,
		func(bdir string) plan.ValidateOpts { return validateOpts(ws, bdir) })
	errs := countErrors(findings)
	if *asJSON {
		if werr := writeJSON(env.Stdout, map[string]any{keyFindings: findings, "errors": errs}); werr != nil {
			return 1
		}
	} else {
		renderDoctor(env.Stdout, findings, errs)
	}
	if errs > 0 {
		return 1
	}
	return 0
}

// countErrors tallies findings at ERROR level.
func countErrors(findings []doctor.Finding) int {
	errs := 0
	for _, f := range findings {
		if f.Level == "ERROR" {
			errs++
		}
	}
	return errs
}

// renderDoctor prints the text form: one line per finding, an indented
// fix: line when present, and a one-line summary.
func renderDoctor(w io.Writer, findings []doctor.Finding, errs int) {
	for _, f := range findings {
		fmt.Fprintf(w, "%-5s %s: %s\n", f.Level, f.Check+" "+f.Slug, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "      fix: %s\n", f.Fix)
		}
	}
	fmt.Fprintf(w, "takt doctor: %d finding(s), %d error(s)\n", len(findings), errs)
}
