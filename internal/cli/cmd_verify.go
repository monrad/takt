package cli

import (
	"context"
	"flag"
	"io"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/deadline"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/wave"
)

// cmdVerify runs the union of the plan's verify commands at HEAD and
// records the result (spec §7.5 step 1). A failing command is a normal
// result (exit 0, passed:false); only takt's own failures exit 1.
func cmdVerify(env Env) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
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
	if code = finishPhaseOnly(env, tgt.st, "verify"); code != 0 {
		return code
	}
	rec, err := verifyAtHead(ctx, tgt)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, verifyJSON(*rec))
}

// finishPhaseOnly refuses a finish-phase command outside that phase. The
// finish verbs are driven by a session, not by takt, so a stale op or a
// hand-typed command can reach one early — and half-doing the work is worse
// than saying no: `verify` would record a verification of a HEAD the run has
// not finished at, `record --agent goal-assessor` would check goals against
// a tree still being built, and `done --step retro` would take a receipt row
// 22 has not asked for. All three refuse in the one shape, so a session
// learns it once (review M2, spec §5.1).
func finishPhaseOnly(env Env, st *bundle.State, what string) int {
	if st.Phase == bundle.PhaseFinish {
		return 0
	}
	return fail(env.Stderr, exitError,
		what+" runs in the finish phase (now "+st.Phase+")", "run `takt next`")
}

// verifyAtHead assembles the command union, runs it from the repo root and
// writes the record. A pass goes through markVerified, so `takt verify` and
// the two gate answers that declare HEAD verified all set verified_sha the
// same way.
func verifyAtHead(ctx context.Context, tgt *runTarget) (*finish.VerifyRecord, error) {
	idx, err := readIndex(tgt.bdir)
	if err != nil {
		return nil, err
	}
	extra, err := finish.ReadExtra(tgt.bdir)
	if err != nil {
		return nil, err
	}
	cmds := finish.UnionCommands(idx, extra)
	head, err := tgt.ws.Repo.HeadSHA(ctx)
	if err != nil {
		return nil, err
	}
	rec := finish.VerifyRecord{SHA: head, Commands: cmds, At: timeNow()}
	if len(cmds) == 0 {
		rec.NoCommands = true
		if err = finish.WriteVerify(tgt.bdir, rec); err != nil {
			return nil, err
		}
		_ = bundle.AppendEvent(tgt.bdir, "verify", map[string]any{keySHA: head, keyNoCommands: true})
		return &rec, nil
	}
	rec.Results = runVerifyCommands(tgt, cmds)
	rec.Passed = allPassed(rec.Results)
	if rec.Passed {
		if err = markVerified(ctx, tgt, rec, map[string]any{"commands": len(cmds)}); err != nil {
			return nil, err
		}
		return &rec, nil
	}
	if err = finish.WriteVerify(tgt.bdir, rec); err != nil {
		return nil, err
	}
	_ = bundle.AppendEvent(tgt.bdir, "verify", map[string]any{
		keySHA: head, keyPassed: false, keyFailed: failedList(rec.Results),
	})
	return &rec, nil
}

// runVerifyCommands runs the union under its own deadline: verify_timeout
// applies per command, so the whole run gets what [deadline.Verify] sizes
// for n of them. It is deliberately not derived from the caller's context,
// whose budget is the git timeout — a two-minute git deadline must not kill
// a ten-minute test suite.
func runVerifyCommands(tgt *runTarget, cmds []string) []wave.VerifyResult {
	per := time.Duration(tgt.ws.Cfg.VerifyTimeout)
	runCtx, cancel := context.WithTimeout(context.Background(), deadline.Verify(per, len(cmds)))
	defer cancel()
	return wave.RunVerify(runCtx, tgt.ws.Repo.Root, cmds, per)
}

// allPassed reports whether every command succeeded.
func allPassed(rs []wave.VerifyResult) bool {
	for _, r := range rs {
		if !r.Passed {
			return false
		}
	}
	return true
}

// failedList is the ask context shape: {command, exit, tail}.
func failedList(rs []wave.VerifyResult) []map[string]any {
	out := []map[string]any{}
	for _, r := range rs {
		if !r.Passed {
			out = append(out, map[string]any{"command": r.Command, "exit": r.Exit, "tail": r.Tail})
		}
	}
	return out
}

// verifyJSON is what `takt verify` prints.
func verifyJSON(rec finish.VerifyRecord) map[string]any {
	return map[string]any{
		keySHA: rec.SHA, keyPassed: rec.Passed, keyNoCommands: rec.NoCommands,
		"commands": rec.Commands, keyFailed: failedList(rec.Results),
	}
}
