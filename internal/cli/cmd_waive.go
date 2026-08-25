package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/wave"
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
	if !waivable(tgt.bdir, t) {
		return fail(env.Stderr, exitError,
			fmt.Sprintf("task %d is %s; only failed, blocked or rework-exhausted tasks can be waived",
				*task, t.Status),
			"run `takt status`; a task still being retried is not waivable yet")
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

// waivable reports whether a task may be accepted as it stands. A failed or
// blocked task always is. A task the reviewer sent back for rework is not
// failed — spec §4.3 keeps it `pending` so the loop can re-dispatch it — so
// once it has run out of rework attempts the wave_failures gate names it
// under `exhausted` and the user is told to waive it, but `waive` used to
// refuse (review I3). Only the wave's close record can say a pending task is
// one of those, and it says so about a task that has actually run.
func waivable(bdir string, t *bundle.Task) bool {
	if t.Status == bundle.StatusFailed || t.Status == bundle.StatusBlocked {
		return true
	}
	if t.Status != bundle.StatusPending || t.Attempt == 0 {
		return false
	}
	c := latestClose(bdir, t.Wave)
	return c != nil && slices.Contains(c.Rework, t.ID)
}

// latestClose is the wave's close record, falling back to the copy dropClose
// retired: answering the wave_failures gate with `waive` retires the record
// before `takt waive` ever runs, and the retired copy is the only place the
// rework verdict that made the task waivable still exists.
func latestClose(bdir string, waveN int) *wave.CloseResult {
	if c, err := wave.ReadClose(bdir, waveN); err == nil && c != nil {
		return c
	}
	b, err := os.ReadFile(prevClosePath(bdir, waveN))
	if err != nil {
		return nil
	}
	var c wave.CloseResult
	if uerr := json.Unmarshal(b, &c); uerr != nil {
		return nil
	}
	return &c
}
