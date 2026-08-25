package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/monrad/takt/internal/bundle"
)

// cmdWaive accepts a failed or blocked task as it stands, with a recorded
// reason, so the run can proceed past it (spec §7.4 step 5).
func cmdWaive(env Env) int {
	fs := flag.NewFlagSet("waive", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	task := fs.Int("task", 0, "task to waive")
	reason := fs.String("reason", "", "why the task is accepted unfinished")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	if *task <= 0 || strings.TrimSpace(*reason) == "" {
		return fail(env.Stderr, exitUsage, "waive needs --task N and --reason",
			"a waiver is a decision the run records, never a silent skip")
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	t := tgt.st.Task(*task)
	if t == nil {
		return fail(env.Stderr, exitError, fmt.Sprintf("no task %d", *task), "run `takt status`")
	}
	if t.Status != bundle.StatusFailed && t.Status != bundle.StatusBlocked {
		return fail(env.Stderr, exitError,
			fmt.Sprintf("task %d is %s; only failed or blocked tasks can be waived", *task, t.Status), "")
	}
	t.Status = bundle.StatusWaived
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(tgt.bdir, "task_waived", map[string]any{keyTask: *task, keyReason: *reason})
	if _, _, err := commitBundle(ctx, tgt.ws, tgt.bdir, tgt.slug, fmt.Sprintf("task %d waived", *task)); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{keyTask: *task, keyStatus: bundle.StatusWaived})
}
