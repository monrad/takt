package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

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
	findings := doctor.RunWith(ctx, ws.Dir, doctorOptions(ctx, ws, *all), doctor.Default)
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

// doctorOptions builds the doctor.Options a `takt doctor` run judges every
// bundle against: today's clock, this workspace's staleness thresholds, the
// checked-out branch, and a Resolve that asks git whether a ref or sha is a
// real commit (spec §11). CurrentBranch is left "" when it cannot be read
// (e.g. a detached HEAD mid-rebase) so the branch check simply skips its
// WARN rather than misreporting a mismatch.
func doctorOptions(ctx context.Context, ws *workspace, all bool) doctor.Options {
	cur, _ := ws.Repo.CurrentBranch(ctx)
	return doctor.Options{
		All: all, Now: time.Now().UTC(),
		WaveStaleAfter: time.Duration(ws.Cfg.WaveStaleAfter), LockTTL: time.Duration(ws.Cfg.LockTTL),
		RepoRoot: ws.Repo.Root, CurrentBranch: cur,
		Resolve: func(ref string) bool {
			_, err := ws.Repo.Run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
			return err == nil
		},
		// A git failure reads as "nothing outstanding": doctor is a
		// read-only report, and a check that cannot ask git must not invent
		// an ERROR about the answer.
		Dirty: func(rel string) bool {
			dirty, err := bundleDirty(ctx, ws.Repo, rel)
			return err == nil && dirty
		},
		ValidateOpts: func(bdir string) plan.ValidateOpts { return validateOpts(ws, bdir) },
	}
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
