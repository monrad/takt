package cli

import (
	"flag"
	"io"

	"github.com/monrad/takt/internal/bundle"
)

// cmdUnlock clears a stale session lock (spec §5.1).
func cmdUnlock(env Env) int {
	fs := flag.NewFlagSet("unlock", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	// A lock file that cannot be read is exactly what unlock exists to
	// discard, so the read error is ignored rather than fatal — the holder
	// is simply unknown, and reported as "" (spec §4.6).
	held, _ := bundle.ReadSession(tgt.bdir)
	holder := ""
	if held != nil {
		holder = held.ID
	}
	if err := bundle.ClearSession(tgt.bdir); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(tgt.bdir, "lock_released", map[string]any{"holder": holder, "by": "unlock"})
	return printJSON(env, map[string]any{"released": holder})
}
