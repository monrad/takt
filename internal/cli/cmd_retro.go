package cli

import (
	"flag"
	"io"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/op"
)

// cmdRetro re-derives the retro's two finish artifacts and re-emits the
// retro `run` op, in the finish and archived phases (spec §7). The archived
// case is the motivating one: a retrospective read months later and found
// wanting must be redoable, and it is the only way the disposition reaches
// the page at all — decideFinish emits the retro one row before it asks
// branch_finish, so on the first pass there is nothing to render but "not
// yet chosen".
//
// Everything the op names is derived by retroRunOp, the helper `takt next`
// emits the same op through, so the two commands write the same pair from
// one derivation and this command performs none of its own. No state is
// written and no commit is taken: the pair is bundle files, swept by
// whichever command next commits the bundle — `takt done --step retro`,
// which accepts the archived phase for exactly this reason. The one other
// tracked file a rewrite can leave modified is events.jsonl, and only when
// the lock it took had been orphaned or left stale: acquireLock records that
// takeover as a lock_taken event, which the same later commit sweeps along
// with the pair.
func cmdRetro(env Env) int {
	fs := flag.NewFlagSet("retro", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run to rewrite the retrospective of")
	rewrite := fs.Bool("rewrite", false, "re-derive the retro artifacts and re-emit the retro op")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	// Re-deriving is harmless — every artifact is a pure function of the
	// bundle — so the flag guards nothing. The verb states its intent
	// instead: `takt retro` alone reads like a query, and this one writes.
	if !*rewrite {
		return fail(env.Stderr, exitUsage, "retro needs --rewrite",
			"run `takt retro --rewrite --slug <slug>` to re-derive the retro artifacts and re-emit the op")
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	// A run whose branch a `merge` or `discard` disposition deleted has no
	// checked-out bundle to rewrite, and openTarget's existing "no run named
	// <slug>" is the right answer there (spec §7).
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	if code = finishOrArchivedOnly(env, tgt.st, "retro --rewrite"); code != 0 {
		return code
	}
	// The run lock, taken exactly as `takt next` takes it. Purity makes the
	// retro's *content* reproducible; it does not make the *pair* a
	// snapshot. This command replaces two tracked files in sequence, and a
	// concurrent `next` on an archived run calls recommitArchive, which
	// stages and commits whatever in the bundle is dirty — a commit that can
	// land between the two writes and capture a half-updated pair. Per-file
	// atomic renames cannot close that window; the lock is what does, which
	// is why cmdNext takes it on the archived path too. A lock only one of
	// two writers respects is not one (spec §7).
	//
	// The one divergence, lockBlocked: `takt next` can afford to ask a live
	// holder's owner gate, because the op it prints is a question the next
	// call answers. This command is not an op loop — it has one thing to do
	// and does not do half of it — so a live holder is a failure naming who
	// holds the run and when they last called. The holder is reported and
	// never written through: the pair this command would replace is exactly
	// what the other session may be committing (spec §4.6).
	id, generated := sessionID(env.Getenv)
	r := &nextRun{
		env: env, ws: tgt.ws, slug: tgt.slug, bdir: tgt.bdir, st: tgt.st, now: timeNow(),
		session: id, genID: generated,
		lockBlocked: func(held *bundle.Session) int {
			return fail(env.Stderr, exitError,
				"the run is held by "+held.ID+" (heartbeat "+held.Heartbeat.Format(time.RFC3339)+")",
				"run `takt unlock --slug "+tgt.slug+"` if the session is gone")
		},
	}
	if lockCode, done := r.acquireLock(ctx); done {
		return lockCode
	}
	o, err := retroRunOp(op.Op{
		Op: op.Run, Narration: "rewrite the retrospective", Step: op.StepRetro,
		Done: "takt done --step " + op.StepRetro + " --slug " + tgt.slug,
	}, tgt.bdir, tgt.st)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	// Through emit, which is printOp plus whatever optional write taking the
	// lock lost — this command takes it, so it can lose one (the warnings
	// contract).
	return r.emit(o)
}
