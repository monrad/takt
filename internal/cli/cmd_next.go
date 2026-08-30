package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// maxDecideIterations bounds the transition loop inside one `takt next`.
const maxDecideIterations = 8

// nextRun is one `takt next` invocation: the run it drives and the facts
// about this call that Decide needs.
type nextRun struct {
	env     Env
	ws      *workspace
	slug    string
	bdir    string
	st      *bundle.State
	now     time.Time
	session string
	genID   bool // takt invented r.session; nothing persisted it
	force   bool
	recover bool
	// lockBlocked answers a lock a live other session holds, and is the one
	// point at which a command that borrows [nextRun.acquireLock] may
	// diverge from the way `takt next` takes the run: the fields above are
	// what the lock is taken with, and this is all that changes when it
	// cannot be. Nil is next's own answer, the owner ask, which offers the
	// user a takeover; `takt retro --rewrite` is not an op loop and has no
	// turn to spend on a gate, so it fails instead (spec §7).
	lockBlocked func(held *bundle.Session) int
	// warnings names the optional writes this call lost without failing —
	// today only info/exclude (the warnings contract). Every op printed
	// after the lock is taken carries them, which is what [nextRun.emit] is
	// for.
	warnings []string
}

// opPrinter prints one op and returns the command's exit code. applyAndStop
// takes one rather than printing for itself because it serves two callers:
// the archive that ends a run, and every later `takt next` on the archived
// run. Both reach it holding the lock, so both hand it [nextRun.emit], and
// neither can drop a warning its call collected on the way there.
type opPrinter func(op.Op) int

// cmdNext implements the op trampoline (spec §5.1): take the session lock,
// then decide, perform any side effect whose preconditions are now met, and
// return the first real op.
func cmdNext(env Env) int {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run to drive")
	force := fs.Bool("force", false, "take over the session lock")
	recoverFlag := fs.Bool("recover", false, "treat an unrecorded wave as crashed")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	// The lock is taken before the phase is looked at, so every path that can
	// write to this bundle holds it — the archived one included. That path
	// once ran unlocked, on the grounds that stamping a fresh holder on a run
	// takt had just released would dirty a tracked file for nothing; the
	// holder has lived in the untracked logs/session.json since state version
	// 2, so it costs nothing, and `takt retro --rewrite` made it necessary.
	// A rewrite replaces two tracked files in sequence and takes this same
	// lock to make the pair a snapshot, and recommitArchive below commits
	// whatever in the bundle is dirty — so an unlocked archived `next` could
	// commit half a pair a rewrite was still writing, and applyAndStop's
	// ClearSession would then discard the rewrite's lock as well. A lock only
	// one of two writers respects is not one (spec §4.6, §7).
	id, generated := sessionID(env.Getenv)
	r := &nextRun{
		env: env, ws: tgt.ws, slug: tgt.slug, bdir: tgt.bdir, st: tgt.st, now: timeNow(),
		session: id, genID: generated, force: *force, recover: *recoverFlag,
	}
	if lockCode, done := r.acquireLock(ctx); done {
		return lockCode
	}
	// An archived run has nothing left to decide (spec §5.3 row 26). What the
	// disposition asked of git is re-derived instead of remembered, so an
	// archive whose merge could not be made — the primary worktree was busy,
	// the merge conflicted — makes it here on a later call, and one that is
	// fully done says so and does nothing. applyAndStop releases the lock
	// this call just took, so an archived run still holds nothing once the
	// call is over.
	if tgt.st.Phase == bundle.PhaseArchived {
		if code = recommitArchive(ctx, env, tgt); code != 0 {
			return code
		}
		return applyAndStop(ctx, env, tgt, r.emit)
	}
	if code = r.healFinish(ctx); code != 0 {
		return code
	}
	return r.loop(ctx)
}

// acquireLock refreshes or takes the advisory lock recorded in the bundle's
// untracked logs/session.json; a live other session yields whatever
// [nextRun.blockedBy] answers — for `takt next`, the owner ask (transient,
// not persisted). A holder that recorded generated=true is not a
// live session — nothing persisted its id, so it can never present it again
// — and is taken over silently; that is read off the holder's own record,
// never guessed from the shape of its id (spec §4.6, review finding 1).
// Every other outcome rewrites the sidecar with a fresh heartbeat: it is
// untracked, so the write neither dirties the worktree nor rides into a
// commit, and there is no lease to nurse.
func (r *nextRun) acquireLock(ctx context.Context) (int, bool) {
	host, _ := os.Hostname()
	who := bundle.Identity{ID: r.session, Host: host, Generated: r.genID}
	held, err := bundle.ReadSession(r.bdir)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(),
			"the lock file cannot be read; run `takt unlock --slug "+r.slug+"` to discard it"), true
	}
	orphaned := held != nil && held.ID != r.session && held.Generated
	outcome, next := bundle.Acquire(held, who, r.now, time.Duration(r.ws.Cfg.LockTTL), r.force || orphaned)
	if outcome == bundle.LockBlocked {
		return r.blockedBy(held), true
	}
	// Both rules that keep the bundle's untracked area out of git go in
	// first, every time. A bundle created before they existed has a logs/
	// with no .gitignore, and commitBundle stages the bundle directory
	// wholesale, so the lock written below would ride into this run's next
	// commit; and without the info/exclude pair the same lock shows as `??
	// docs/` the moment this worktree is checked back out on the base, which
	// is enough to hide the `merge` disposition at finish (§7.5). The
	// exclude is repository state, so init — which runs once, and already
	// ran — is the one place it could never be repaired from. Neither call
	// writes anything when the rule is already there.
	//
	// The two are not equally load-bearing, and they fail differently. The
	// tracked .gitignore is what keeps the lock out of the commit this run
	// is going to make, so losing it is fatal. info/exclude only keeps the
	// sidecar invisible from another branch — cosmetic — so its loss is
	// reported on the op this call is about to print and the run carries on
	// (the warnings contract).
	if err = writeLogsIgnore(r.bdir); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), ""), true
	}
	if err = excludeLogsDir(ctx, r.ws, r.bdir); err != nil {
		r.warnings = append(r.warnings, excludeWarning(err))
	}
	if err = bundle.WriteSession(r.bdir, next); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), ""), true
	}
	// A lock_taken records a takeover, so every arm below is gated on one
	// having happened. Acquire returns `acquired` when there was no holder
	// at all and `held-by-self` when the caller already holds the run, and
	// a --force passed in either situation takes the run from nobody:
	// grading on the flag would write an event for a takeover that never
	// happened — a false audit line, and churn in the tracked events.jsonl
	// on every forced `takt next` against a free lock.
	//
	// Every takeover of a session that could have been driving is
	// recorded: an expired heartbeat, an explicit --force, and the silent
	// takeover of a holder that recorded generated=true and can therefore
	// never come back. The last one is invisible to the user by design,
	// which is exactly why it belongs in the log — spec §4.6 has takt
	// record recovery as an event rather than repairing state silently
	// (review M7).
	//
	// With one exception, and only when the takeover was not asked for:
	// when the *acquirer's* id is generated too, nobody could have been
	// driving — neither id was ever handed to a second process — so there
	// is no takeover to report, and appending one would rewrite
	// events.jsonl, a tracked file, on every single `takt next` a session
	// without CLAUDE_CODE_SESSION_ID/TAKT_SESSION makes. A named session
	// taking over a generated holder still logs it, and so does an
	// explicit --force: r.force is set from the command line and nowhere
	// else, so the churn argument that justifies the exemption never
	// reaches it, and a takeover a user asked for is recorded whatever the
	// holder's kind.
	//
	// The exception is decided on the holder's record, not on Acquire's
	// outcome, and so is graded ahead of the outcome arms. Acquire grades
	// an expired heartbeat ahead of force, so a generated holder that has
	// simply been idle longer than lock_ttl comes back as `stolen` rather
	// than `forced` — reading the outcome instead put a lock_taken line in
	// the tracked events.jsonl after every pause, which is exactly the
	// dirty tree this exemption exists to prevent. The event carries
	// whatever Acquire graded, so a forced takeover of a long-idle holder
	// still reads `stolen`.
	takeover := outcome == bundle.LockStolen || outcome == bundle.LockForced
	switch {
	case !takeover: // the run was taken from nobody; there is nothing to record
	case orphaned && r.genID && !r.force: // nobody could have been driving
	case orphaned:
		_ = bundle.AppendEvent(r.bdir, "lock_taken", map[string]any{
			keySession: r.session, "outcome": string(outcome), keyReason: "orphaned",
		})
	default:
		_ = bundle.AppendEvent(r.bdir, "lock_taken", map[string]any{
			keySession: r.session, "outcome": string(outcome),
		})
	}
	return 0, false
}

// blockedBy answers a run a live other session is driving, and is the whole
// of what a borrower of [nextRun.acquireLock] may decide for itself. `takt
// next` is the op loop, so its answer is the owner ask: a transient question
// naming the holder, whose `takeover` choice the user can take. A command
// that is not a loop sets lockBlocked and answers in its own shape — for
// `takt retro --rewrite`, an error, because there is no next call to hand a
// gate to (spec §4.6, §7).
func (r *nextRun) blockedBy(held *bundle.Session) int {
	if r.lockBlocked != nil {
		return r.lockBlocked(held)
	}
	return r.emit(decide.Question("owner", map[string]any{
		keySlug: r.slug, "holder": held.ID, "host": held.Host,
		"heartbeat": held.Heartbeat.Format(time.RFC3339),
	}))
}

// emit prints one of this run's ops, carrying whatever optional write the
// call lost. Every op a `takt next` can print goes through here — including
// the two the archive path builds in archive.go, the stop op that ends the
// run and the one a later call on the archived run replays, which would
// otherwise drop a warning on exactly the calls that end well. Routing them
// all through one helper is what stops a future exit path from losing one.
//
// `takt retro --rewrite` prints its one op through here for the same
// reason: it borrows acquireLock, so it can lose the same optional write.
func (r *nextRun) emit(o op.Op) int {
	if len(r.warnings) > 0 {
		o.Warnings = append(slices.Clone(o.Warnings), r.warnings...)
	}
	return printOp(r.env, o)
}

// loop decides, performs the side effects whose preconditions are met, and
// returns as soon as one op is printed (spec §5.3 rows 7, 12, 19).
func (r *nextRun) loop(ctx context.Context) int {
	for range maxDecideIterations {
		facts, err := gatherFacts(ctx, r.ws, r.bdir, r.st, r.force, r.recover, r.now, r.waveSession())
		if err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), r.factsHint())
		}
		d, err := decide.Decide(r.st, facts)
		if err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "run `takt doctor`")
		}
		switch d.Action {
		case decide.ActTransition:
			if code := r.transition(ctx, d.Phase); code != 0 {
				return code
			}
		case decide.ActLoadPlan:
			if code := r.loadPlan(ctx); code != 0 {
				return code
			}
		case decide.ActClearWave:
			if code := r.clearWave(ctx, d.Wave); code != 0 {
				return code
			}
		case decide.ActLaunch:
			return launchWave(ctx, r, d)
		case decide.ActRecover:
			return recoverWave(ctx, r, d)
		case decide.ActDispatch:
			return r.dispatchAgent(ctx, d)
		case decide.ActDispatchLenses:
			return r.dispatchLenses(ctx, d)
		case decide.ActAsk:
			return r.ask(*d.Op)
		case decide.ActRun:
			return r.run(*d.Op)
		case decide.ActExec, decide.ActStop:
			return r.emit(*d.Op)
		case decide.ActArchive:
			return r.archive(ctx)
		default:
			return fail(r.env.Stderr, exitError, "unknown decision "+string(d.Action), "")
		}
	}
	return fail(r.env.Stderr, exitError, "decide loop did not converge", "run `takt doctor`")
}

// factsHint is the recovery advice for a fact-gathering failure that does
// not say which file it is about — healFinish's and the loop's, the two
// places one `takt next` gathers facts. A finish-phase call decodes
// finish/goals.json every time (goalFacts), and encoding/json names no file
// in its messages, so a truncated record reaches the user as "unexpected end
// of JSON input" with nothing to open. Re-reading the record once the call
// has already failed is what turns that into a path — the same
// after-the-fact diagnosis openTarget makes of a bundle it could not load
// (#8) — and a call that succeeds pays nothing for it.
//
// The advice is to restore the file, and only that. Removing it would clear
// the error without answering it: goals_checked_sha stays in state.json, so
// the goals still count as checked, nothing is reassessed, and the run walks
// on to a pull request whose every goal reads "not assessed".
func (r *nextRun) factsHint() string {
	if r.st.Phase != bundle.PhaseFinish {
		return ""
	}
	if _, err := finish.ReadGoals(r.bdir); err != nil {
		return finish.GoalsPath(r.bdir) + " cannot be read; restore it from git — deleting it " +
			"reassesses nothing, since state.json still records the goals as checked"
	}
	return ""
}

// waveSession is the session id the wave-liveness check (spec §5.3 row 13)
// compares the active wave's dispatcher against. When takt had to invent
// this process's id — nothing set CLAUDE_CODE_SESSION_ID or TAKT_SESSION —
// two calls of one real session are indistinguishable, so an in-flight wave
// is taken to be this session's own and `next` waits: waiting for an agent
// that is still working costs a turn, while recovering resets its files and
// cannot be undone. `takt next --recover` forces the other reading.
func (r *nextRun) waveSession() string {
	if r.genID && r.st.ActiveWave != nil {
		return r.st.ActiveWave.SessionID
	}
	return r.session
}

// transition records a phase change and commits it (spec §5.3 rows 7, 19).
// Leaving brainstorm for plan is exactly the transition that follows the
// spec gate passing, so this is where state.gates records it durably —
// `takt status`'s live gate.Compute reflects the receipt from the moment it
// is written, but state.gates is what a doctor index-staleness check
// compares a later edit against (spec §11).
func (r *nextRun) transition(ctx context.Context, to string) int {
	from := r.st.Phase
	r.st.Phase = to
	if to == bundle.PhasePlan {
		r.st.Gates[gate.Spec] = gateStateValue(r.bdir, r.st.Config.Review.Spec, gate.Spec)
	}
	if err := bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "phase", map[string]any{"from": from, "to": to})
	if _, _, err := commitBundle(ctx, r.ws, r.bdir, r.slug, from+" → "+to); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	return 0
}

// clearWave drops a wave whose close record says it was committed — but
// only once git agrees the commit is really in HEAD. The record is written
// before `git commit` runs, so a crash inside the commit leaves
// committed:true with no sha; clearing the wave on that word alone would
// strand the work uncommitted with nothing left pointing at it (review I2,
// spec §5.4). When the claim does not hold, the record is retired instead
// and the next turn of the loop re-issues `exec close-wave`, which re-grades
// nothing (the .prev carry-forward) and commits — unless the commit turns
// out to have landed after all, which backfillCommitSHA re-derives from git
// and repairs in place.
func (r *nextRun) clearWave(ctx context.Context, n int) int {
	aw := r.st.ActiveWave
	if aw == nil {
		return 0
	}
	c, err := wave.ReadClose(r.bdir, n, sliceOf(aw))
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	landed := closeMatchesDispatch(c, aw) &&
		(waveCommitLanded(ctx, r.ws.Repo, c) || r.backfillCommitSHA(ctx, c))
	if !landed {
		if err = dropClose(r.bdir, n, sliceOf(aw)); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
		_ = bundle.AppendEvent(r.bdir, "wave_close_unreconciled", map[string]any{
			keyWave: n, keyReason: "the close record claims a commit that is not in HEAD",
		})
		return 0
	}
	r.st.ActiveWave = nil
	if err = bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "wave_cleared", map[string]any{keyWave: n})
	return 0
}

// backfillCommitSHA repairs the record a kill between the wave commit and
// recordCloseOutcome leaves behind: committed:true with no sha, for a commit
// that really is in HEAD (recordCloseOutcome names that window; nothing used
// to close it). Re-closing the wave cannot recover it — the work is already
// committed, so the re-close grades nothing, finds nothing of the wave's own
// to stage and records nothing_to_commit, a contentless second answer to a
// dispatch that had in fact succeeded.
//
// The claim is not taken on the record's word — that is exactly what
// waveCommitLanded refuses — but re-derived from git: HEAD's subject must be
// the one this close would have written *and* must name task ids, since the
// wave-wide fallbacks name no slice; and none of the wave's own files may
// still be outstanding, because a commit that carried them left them clean.
// Only then is the sha filled in from HEAD, the record rewritten, the parked
// baseline dropped and the repair recorded as a backfilled wave_committed.
// Anything else and the caller retires the record and closes the wave again,
// as before.
func (r *nextRun) backfillCommitSHA(ctx context.Context, c *wave.CloseResult) bool {
	if c == nil || !c.Committed || c.CommitSHA != "" || c.NothingToCommit {
		return false
	}
	tgt := &runTarget{ws: r.ws, slug: r.slug, bdir: r.bdir, st: r.st}
	files, done, err := doneWaveFiles(ctx, tgt, c.Wave)
	if err != nil {
		return false
	}
	graded, mine := gradedIDs(c.Tasks), inSlice(r.st.ActiveWave, done)
	if len(graded) == 0 && len(mine) == 0 {
		// With no task ids to name, waveSubject falls through to the wave's
		// waiver list — or to a bare "close" — and every slice of the wave
		// would write that same sentence. A subject that cannot tell one
		// slice's commit from another's is no evidence at all, so nothing is
		// repaired on it.
		return false
	}
	subj, err := r.ws.Repo.Run(ctx, "log", "-1", "--format=%s")
	if err != nil || subj != waveSubject(r.st, r.slug, c.Wave, graded, mine) {
		return false
	}
	if clean, cerr := pathsCommitted(ctx, r.ws.Repo, files); cerr != nil || !clean {
		return false
	}
	head, err := r.ws.Repo.HeadSHA(ctx)
	if err != nil || head == "" {
		return false
	}
	c.CommitSHA = head
	if err = wave.WriteClose(r.bdir, *c); err != nil {
		return false
	}
	// Everything the interrupted recordCloseOutcome would have done, not
	// just the record: the baseline a retry parked for this wave is spent
	// the moment the wave commits. Left behind, the next launch prefers it
	// over the tree the commit left — coming up as the slice it was parked
	// for, and closing over the record of the slice that has just landed.
	_ = wave.DeleteBaseline(r.bdir, c.Wave)
	ids := graded
	if len(ids) == 0 {
		ids = mine
	}
	_ = bundle.AppendEvent(r.bdir, "wave_committed", map[string]any{
		keyWave: c.Wave, keySlice: c.Slice, keyAttempt: c.Attempt, keySHA: head,
		keyTasks: ids, "backfilled": true,
	})
	return true
}

// healFinish completes the bookkeeping of a finish record whose state write
// did not land. markVerified and markGoalsChecked write the record first and
// state.json second, so a kill between the two leaves a passed verify.json —
// or an all-achieved goals.json — still covering HEAD with no verified_sha
// or goals_checked_sha behind it. Row 20 then asked verification_failed with
// an empty failed list and row 21 "Unmet goals: []": questions with no
// answer the user could give, persisted as gates that outlive the turn
// (review I2). backfillCommitSHA is the precedent — a re-derivation step
// that finishes what an interrupted write started, from what is already on
// disk.
//
// It is a pure function of that disk. It runs only in the finish phase, only
// for a record that still covers HEAD, and only where the record itself says
// the work passed — so the same bundle yields the same writes and the same
// op, and nothing is decided here that decideFinish would not decide the
// same way a moment later.
func (r *nextRun) healFinish(ctx context.Context) int {
	if r.st.Phase != bundle.PhaseFinish {
		return 0
	}
	fin, err := gatherFinishFacts(ctx, r.ws, r.bdir, r.st)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), r.factsHint())
	}
	tgt := &runTarget{ws: r.ws, slug: r.slug, bdir: r.bdir, st: r.st}
	if !fin.Verified && fin.Verify.Present && fin.Verify.Passed {
		if err = healVerified(ctx, tgt); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
	}
	if !fin.GoalsChecked && fin.Goals.Present && len(fin.Goals.Unmet) == 0 {
		if err = healGoalsChecked(tgt); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
	}
	return 0
}

// healVerified sets verified_sha from the passed record that is already on
// disk — the same record, re-written by the same code path a `takt verify`
// pass takes, so the repair leaves the bundle exactly as the interrupted
// call would have.
func healVerified(ctx context.Context, tgt *runTarget) error {
	rec, err := finish.ReadVerify(tgt.bdir)
	if err != nil || rec == nil {
		return err
	}
	return markVerified(ctx, tgt, *rec, map[string]any{keyHealed: true})
}

// healGoalsChecked is healVerified for goals_checked_sha: the all-achieved
// record on disk, re-recorded through the one path that declares HEAD's
// goals checked.
func healGoalsChecked(tgt *runTarget) error {
	rec, err := finish.ReadGoals(tgt.bdir)
	if err != nil || rec == nil {
		return err
	}
	return markGoalsChecked(tgt, *rec, map[string]any{keyHealed: true})
}

// loadPlan materialises state.tasks from the validated index, writes the
// waves back for display, and moves to execute (spec §7.3 Load).
func (r *nextRun) loadPlan(ctx context.Context) int {
	raw, err := os.ReadFile(filepath.Join(r.bdir, "plan.index.json"))
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	idx, err := plan.ParseIndex(raw)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	if len(idx.Tasks) == 0 {
		return fail(r.env.Stderr, exitError, "plan.index.json has no tasks", "re-run the planner")
	}
	waves, err := plan.AssignWaves(idx)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	maxWave := r.materialiseTasks(idx, waves)
	if err = writeIndex(r.bdir, idx); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	r.st.Phase = bundle.PhaseExecute
	r.st.Gates[gate.Plan] = gateStateValue(r.bdir, r.st.Config.Review.Plan, gate.Plan)
	if err = bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, "plan_loaded", map[string]any{"tasks": len(idx.Tasks), keyWaves: maxWave + 1})
	_ = bundle.AppendEvent(r.bdir, "phase", map[string]any{"from": bundle.PhasePlan, "to": bundle.PhaseExecute})
	msg := loadCommitMessage(r.bdir, r.slug, len(idx.Tasks), maxWave+1)
	if _, _, err = commitBundle(ctx, r.ws, r.bdir, r.slug, msg); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	return 0
}

// commitSubjectSoftLimit is the approximate rune budget for the load
// commit's git subject line before the alignment summary moves to the
// commit body instead (spec §7.3).
const commitSubjectSoftLimit = 100

// loadCommitMessage builds the plan→execute commit message: the existing
// "plan → execute (n tasks, w waves)" subject, plus — once the alignment
// audit has recorded verdicts — " — alignment: " and the same
// contraction/creep summary `status` shows after its own "alignment: "
// label, reusing statusAlignment/alignmentLine rather than re-deriving the
// bucketing (spec §7.3: "...are reported as contraction, widened as creep —
// in the load commit message and in status"), e.g.
// "plan → execute (2 tasks, 2 waves) — alignment: 1 covered, 1 narrowed
// (contraction: A2)". No verdict artifacts (the audit is disabled, skipped,
// or has not run yet) leaves the message unchanged. Appending the summary
// would sometimes push the subject well past a readable git subject line,
// so once the one-line form (with the "takt(<slug>): " prefix commitBundle
// adds) would exceed commitSubjectSoftLimit runes, the subject stays just
// "plan → execute (…)" and "alignment: <summary>" becomes the commit
// body's first line instead.
func loadCommitMessage(bdir, slug string, tasks, waves int) string {
	subject := fmt.Sprintf("plan → execute (%d tasks, %d waves)", tasks, waves)
	align := ""
	if a := statusAlignment(bdir); a != nil {
		align = alignmentLine(a)
	}
	if align == "" {
		return subject
	}
	oneLine := subject + " — alignment: " + align
	if utf8.RuneCountInString("takt("+slug+"): "+oneLine) <= commitSubjectSoftLimit {
		return oneLine
	}
	return subject + "\n\nalignment: " + align
}

// materialiseTasks replaces state.tasks with the index's tasks, stamps each
// index task with its computed wave for display, and returns the last wave.
func (r *nextRun) materialiseTasks(idx plan.Index, waves map[int]int) int {
	r.st.Tasks = r.st.Tasks[:0]
	maxWave := 0
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		w := waves[t.ID]
		t.Wave = new(w)
		if w > maxWave {
			maxWave = w
		}
		r.st.Tasks = append(r.st.Tasks, bundle.Task{
			ID: t.ID, Wave: w, Status: bundle.StatusPending,
			Files: append([]string{}, t.Files...), Class: t.Class,
		})
	}
	return maxWave
}

// dispatchAgent renders the planner / auditor / reviewer brief and prints
// the op.
func (r *nextRun) dispatchAgent(ctx context.Context, d decide.Decision) int {
	ag := *d.Agent
	ag.Cwd = r.ws.Repo.Root
	// The verifier's brief is wave-scoped — it lives under waves/<n>/, not
	// briefs/, the way the lens briefs do (two-layers design §3.2, §3.3) — so
	// its destination is spelled out here rather than left to
	// writeStableBrief's name-from-render convention. Its slice diff is
	// written here too, once, exactly as dispatchLenses writes it: a render
	// closure is called again to compare tokens, and rewriting the diff on
	// every render is work that comparison must not do (#51).
	dest, diffPath := "", ""
	if ag.Agent == op.AgentReviewer {
		aw := r.st.ActiveWave
		dest = filepath.Join(waveDir(r.bdir, aw.N), fmt.Sprintf("verify.s%d.a%d.md", sliceOf(aw), aw.Attempt))
		var derr error
		if diffPath, derr = r.ensureSliceDiff(ctx); derr != nil {
			return fail(r.env.Stderr, exitError, derr.Error(), "")
		}
	}
	render := func(tok string) (string, string, error) {
		switch ag.Agent {
		case op.AgentPlanner:
			return r.plannerBrief(ctx, &ag, tok)
		case op.AgentAlignmentAuditor:
			return r.auditorBrief(&ag, tok)
		case op.AgentGoalAssessor:
			return r.assessorBrief(ctx, &ag, tok)
		case op.AgentReviewer:
			return r.verifyBrief(&ag, tok, diffPath)
		}
		return "", "", errors.New("unknown agent " + ag.Agent)
	}
	p, err := r.writeDispatchBrief(dest, render)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	ag.Brief = p
	record := fmt.Sprintf("takt record --agent %s --from <file> --slug %s", ag.Agent, r.slug)
	if ag.Mode != "" {
		record += " --mode " + ag.Mode
	}
	o := op.Op{Op: op.Dispatch, Narration: ag.Label, Agents: []op.Agent{ag}, Record: record}
	if ag.Agent == op.AgentReviewer {
		aw := r.st.ActiveWave
		record += fmt.Sprintf(" --attempt %d", aw.Attempt)
		o.Record = record
		// The verify dispatch names its wave and attempt exactly as the lens
		// fan-out's op does (dispatchLenses), both for the op to be
		// self-describing and because a driver answering it needs
		// o["attempt"] to build the `takt record` call — omitted here, it was
		// absent from the JSON (op.Op.Attempt is omitempty) and any caller
		// reading it panicked instead of running (review finding).
		o.Wave, o.Attempt = new(aw.N), aw.Attempt
	}
	return r.emit(o)
}

// writeDispatchBrief writes the brief dispatchAgent is about to name. dest
// is "" for the agents whose brief lives under briefs/ under the name their
// own render reports; for the wave-scoped verifier it is the path, and the
// fresh render is made here so writeStableBriefAt is handed text rather than
// a closure to render again (#51).
func (r *nextRun) writeDispatchBrief(
	dest string, render func(tok string) (text, name string, err error),
) (string, error) {
	if dest == "" {
		return writeStableBrief(r.bdir, render)
	}
	text, _, err := renderFresh(render)
	if err != nil {
		return "", err
	}
	return writeStableBriefAt(dest, text, render)
}

// writeStableBrief renders a non-task brief under briefs/, reusing the
// delimiter token of the file already there when the text is otherwise
// unchanged, so a replayed `next` leaves the brief byte-identical instead
// of churning a fresh random token through a tracked file (spec §5.4). A
// brief whose content did change is rewritten with the old token, so the
// diff shows the change and nothing else; if the old token now collides
// with the content (Quote refuses it) the fresh render is written instead.
func writeStableBrief(bdir string, render func(tok string) (text, name string, err error)) (string, error) {
	text, name, err := renderFresh(render)
	if err != nil {
		return "", err
	}
	return writeStableBriefAt(briefPath(bdir, name), text, render)
}

// renderFresh renders a brief with a newly minted delimiter token: the text
// that is written when there is no brief on disk to take a token from. It is
// the one fresh render a call makes — the only other one is
// reuseBriefToken's, which re-renders with the token already on disk and is
// the byte comparison itself (#51).
func renderFresh(render func(tok string) (text, name string, err error)) (string, string, error) {
	fresh, err := brief.Token()
	if err != nil {
		return "", "", err
	}
	return render(fresh)
}

// writeStableBriefAt is writeStableBrief with the destination and the
// fresh-token render spelled by the caller — wave-scoped reviewer briefs
// live under waves/<n>/, not briefs/ (two-layers design §3.2), and the
// caller that has already rendered must not pay for a second render.
func writeStableBriefAt(
	p, text string, render func(tok string) (text, name string, err error),
) (string, error) {
	reused, unchanged := reuseBriefToken(p, render)
	if unchanged {
		return p, nil
	}
	if reused != "" {
		text = reused
	}
	return p, bundle.WriteFileAtomic(p, []byte(text))
}

// reuseBriefToken re-renders the brief at p with the delimiter token already
// on disk there, so writeStableBrief can compare bytes instead of writing a
// fresh token on every call. The second return reports the re-render
// reproduced the file byte-for-byte (a replay: nothing to write). Otherwise
// the first return, when non-empty, is the re-render to write in place of
// the fresh-token one, so the diff shows only the real change. Any failure
// along the way — no file yet, no token in it, or the old token now
// colliding with the content (Quote refuses it) — reports ("", false): the
// caller's fresh render is used as-is.
func reuseBriefToken(p string, render func(tok string) (text, name string, err error)) (string, bool) {
	old, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	tok, ok := brief.TokenOf(string(old))
	if !ok {
		return "", false
	}
	same, _, err := render(tok)
	if err != nil {
		return "", false
	}
	return same, same == string(old)
}

// sliceDiffPath is the untracked diff file the lens and verify briefs point
// at (two-layers design §3.1); logs/ never rides into a commit.
func sliceDiffPath(bdir string, waveN, slice, attempt int) string {
	return filepath.Join(bdir, "logs", fmt.Sprintf("wave-%d.s%d.a%d.diff", waveN, slice, attempt))
}

// ensureSliceDiff writes the slice's diff — the done tasks' declared files,
// rendered exactly as taskDiff renders one task's — and returns its path.
// A replay rewrites the same bytes.
func (r *nextRun) ensureSliceDiff(ctx context.Context) (string, error) {
	aw := r.st.ActiveWave
	var files []string
	for _, id := range aw.Tasks {
		d, _, err := latestDigest(r.bdir, aw.N, id, aw.Attempt)
		if err != nil {
			return "", err
		}
		if d == nil || d.Status != bundle.StatusDone {
			continue
		}
		if t := r.st.Task(id); t != nil {
			files = append(files, t.Files...)
		}
	}
	p := sliceDiffPath(r.bdir, aw.N, sliceOf(aw), aw.Attempt)
	return p, bundle.WriteFileAtomic(p, []byte(taskDiff(ctx, r.ws, files)))
}

// dispatchLenses emits row 15a: one reviewer agent per unrecorded lens over
// the slice's diff, all in one op so the session runs them in parallel
// (two-layers design §3.2).
func (r *nextRun) dispatchLenses(ctx context.Context, d decide.Decision) int {
	aw := r.st.ActiveWave
	diffPath, err := r.ensureSliceDiff(ctx)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	idx, err := readIndex(r.bdir)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	tasks := lensTasks(idx, aw)
	agents := make([]op.Agent, 0, len(d.Lenses))
	for _, lens := range d.Lenses {
		rubric, rerr := brief.LensRubric(lens)
		if rerr != nil {
			return fail(r.env.Stderr, exitError, rerr.Error(), "")
		}
		p := filepath.Join(waveDir(r.bdir, aw.N),
			fmt.Sprintf("lens-%s.s%d.a%d.md", lens, sliceOf(aw), aw.Attempt))
		lensBrief := func(tok string) (string, string, error) {
			text, terr := brief.Render("review-lens", brief.LensData{
				Slug: r.slug, Wave: aw.N, Slice: sliceOf(aw), Attempt: aw.Attempt,
				Lens: lens, Rubric: rubric, DiffPath: diffPath, Tasks: tasks, Token: tok,
			})
			return text, "", terr
		}
		if p, err = r.writeDispatchBrief(p, lensBrief); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
		agents = append(agents, op.Agent{
			Agent: op.AgentReviewer, Mode: lens, Model: r.ws.Cfg.Agents.Reviewer.Model,
			Brief: p, Cwd: r.ws.Repo.Root, Label: "lens: " + lens,
		})
	}
	return r.emit(op.Op{
		Op:        op.Dispatch,
		Narration: fmt.Sprintf("wave %d: internal review, %d lenses", aw.N, len(agents)),
		Wave:      new(aw.N), Attempt: aw.Attempt, Agents: agents,
		Record: fmt.Sprintf("takt record --agent reviewer --mode <mode> --attempt %d --from <file> --slug %s",
			aw.Attempt, r.slug),
	})
}

// lensTasks is the slice's tasks as the lens brief quotes them.
func lensTasks(idx plan.Index, aw *bundle.ActiveWave) []brief.LensTask {
	out := make([]brief.LensTask, 0, len(aw.Tasks))
	for _, id := range aw.Tasks {
		if pt := idx.Task(id); pt != nil {
			out = append(out, brief.LensTask{ID: id, Title: pt.Title, Description: pt.Description, Files: pt.Files})
		}
	}
	return out
}

// plannerBrief pins the planner's model and renders its brief, appending the
// problems of the previous attempt when there was one (spec §5.3 row 8).
func (r *nextRun) plannerBrief(ctx context.Context, ag *op.Agent, tok string) (string, string, error) {
	ag.Model = r.ws.Cfg.Agents.Planner.Model
	ag.Label = "plan the run"
	facts, err := gatherFacts(ctx, r.ws, r.bdir, r.st, false, false, r.now, r.session)
	if err != nil {
		return "", "", err
	}
	attempt := facts.PlanAttempts + 1
	text, err := brief.Render("planner", brief.PlannerData{
		Slug: r.slug, Topic: r.st.Topic, SpecText: readArtifact(r.bdir, "spec.md"),
		GoalsText: readArtifact(r.bdir, "goals.md"), Schema: plannerSchema, RepoRoot: r.ws.Repo.Root,
		Token: tok, MaxFiles: r.ws.Cfg.MaxFilesPerTask, Problems: facts.IndexProblems, Attempt: attempt,
	})
	if err != nil {
		return "", "", err
	}
	return text, fmt.Sprintf("planner.a%d.md", attempt), nil
}

// auditorBrief pins the auditor's model and renders the brief for its mode;
// the verdicts pass also carries the confirmed clauses and the plan.
func (r *nextRun) auditorBrief(ag *op.Agent, tok string) (string, string, error) {
	ag.Model = r.ws.Cfg.Agents.AlignmentAuditor.Model
	data := brief.AlignmentData{Mode: ag.Mode, Anchor: r.st.Topic, Token: tok,
		Problems: lastProblemsIn(r.bdir, evAlignmentInvalid, evAlignmentReset)}
	if ag.Mode == "verdicts" {
		if a, err := readAlignment(r.bdir); err == nil && a != nil {
			data.Clauses = a.Clauses
		}
		data.SpecText = readArtifact(r.bdir, "spec.md")
		data.PlanText = readArtifact(r.bdir, "plan.md")
		data.IndexText = readArtifact(r.bdir, "plan.index.json")
	}
	text, err := brief.Render("alignment-"+ag.Mode, data)
	if err != nil {
		return "", "", err
	}
	return text, "alignment-" + ag.Mode + ".md", nil
}

// assessorBrief renders goal-assessor.md from goals.md, the base..HEAD
// diff stat and the verify record (spec §7.5 step 2).
func (r *nextRun) assessorBrief(ctx context.Context, ag *op.Agent, tok string) (string, string, error) {
	gb, err := os.ReadFile(filepath.Join(r.bdir, "goals.md"))
	if err != nil {
		return "", "", err
	}
	g, err := goals.Parse(gb)
	if err != nil {
		return "", "", err
	}
	stat, err := r.ws.Repo.DiffStat(ctx, r.st.BaseSHA, "HEAD")
	if err != nil {
		return "", "", err
	}
	rec, err := finish.ReadVerify(r.bdir)
	if err != nil {
		return "", "", err
	}
	ag.Model = r.ws.Cfg.Agents.GoalAssessor.Model
	ag.Label = "assess the goals at HEAD"
	lines := make([]brief.GoalLine, 0, len(g.Items))
	for _, it := range g.Items {
		lines = append(lines, brief.GoalLine{ID: it.ID, Text: it.Text})
	}
	text, err := brief.Render("goal-assessor", brief.GoalAssessorData{
		Slug: r.slug, Token: tok, GoalsText: string(gb), DiffStat: stat,
		VerifySummary: verifySummary(rec), Goals: lines,
		Problems: lastProblemsIn(r.bdir, evGoalsInvalid, evGoalsReset),
	})
	return text, "goal-assessor.md", err
}

// verifyBrief renders the verifier's brief over the recomputed candidates
// (two-layers design §3.3, §7.3). diffPath is the slice diff its caller
// wrote before building the render closure this runs inside.
func (r *nextRun) verifyBrief(ag *op.Agent, tok, diffPath string) (string, string, error) {
	aw := r.st.ActiveWave
	ag.Model = r.ws.Cfg.Agents.Reviewer.Model
	lenses := r.st.Config.Review.Lenses
	records := map[string]*wave.LensRecord{}
	for _, l := range lenses {
		rec, rerr := wave.ReadLensRecord(r.bdir, aw.N, sliceOf(aw), aw.Attempt, l)
		if rerr != nil {
			return "", "", rerr
		}
		records[l] = rec
	}
	cands := wave.MergeCandidates(lenses, records)
	vc := make([]brief.VerifyCandidate, 0, len(cands))
	for _, c := range cands {
		vc = append(vc, brief.VerifyCandidate{
			ID: c.ID, Severity: c.Severity, File: c.File, Line: c.Line, Title: c.Title, Detail: c.Detail,
		})
	}
	text, err := brief.Render("review-verify", brief.VerifyData{
		Slug: r.slug, Wave: aw.N, Slice: sliceOf(aw), Attempt: aw.Attempt,
		DiffPath: diffPath, Token: tok, Candidates: vc,
	})
	return text, "", err
}

// lastProblemsIn reads the bundle's events for lastProblems; an unreadable
// log yields no problems — the brief is still valid, just without them.
func lastProblemsIn(bdir, invalid, reset string) []string {
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		return nil
	}
	return lastProblems(events, invalid, reset)
}

// verifySummary is one line per verify command for the assessor.
func verifySummary(rec *finish.VerifyRecord) string {
	if rec == nil {
		return "(no verification record)"
	}
	var b strings.Builder
	for _, res := range rec.Results {
		outcome := "FAIL"
		if res.Passed {
			outcome = "pass"
		}
		fmt.Fprintf(&b, "%s → exit %d (%s)\n", res.Command, res.Exit, outcome)
	}
	if rec.Overridden != "" {
		fmt.Fprintf(&b, "overridden by the user: %s\n", rec.Overridden)
	}
	if rec.Skipped {
		b.WriteString("no verify commands; the user proceeded without verification\n")
	}
	return b.String()
}

// ask persists the gate on first rendering, then prints it; a re-rendered
// gate comes from the stored payload and is byte-identical (spec §4.3).
func (r *nextRun) ask(o op.Op) int {
	if r.st.PendingGate == nil || r.st.PendingGate.ID != o.Gate {
		if err := openGate(r.bdir, r.st, o, r.now); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
	}
	return r.emit(o)
}

// run fills a run op's instructions from the step's template (spec §5.2)
// and adds whatever else that step needs to do its work.
func (r *nextRun) run(o op.Op) int {
	data := brief.RunData{
		Slug: r.slug, Topic: r.st.Topic,
		SpecPath: filepath.Join(r.bdir, "spec.md"), GoalsPath: filepath.Join(r.bdir, "goals.md"),
		Branch: r.st.Branch, Base: r.st.Base,
	}
	inputs := map[string]any{
		keySlug: r.slug, "topic": r.st.Topic, "spec_path": data.SpecPath, "goals_path": data.GoalsPath,
	}
	switch o.Step {
	case op.StepRetro:
		// The retro op is filled whole by the helper `takt retro` shares
		// with this branch, artifacts and all, so the two commands derive
		// and emit exactly the same thing (spec §7).
		filled, ferr := retroRunOp(o, r.bdir, r.st)
		if ferr != nil {
			return fail(r.env.Stderr, exitError, ferr.Error(), "")
		}
		return r.emit(filled)
	case op.StepPushPR:
		// The body is re-derived on every call that emits this op, exactly
		// as the retro inputs are: a replayed `next` writes the same bytes
		// and hands back the same op (spec §5.4).
		if err := r.preparePushPR(&data, inputs); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
		inputs[keyBranch] = data.Branch
		inputs[keyBase] = data.Base
		o.Done = "takt done --step " + op.StepPushPR + " --url <pr-url> --slug " + r.slug
	}
	text, err := brief.Render("run-"+o.Step, data)
	if err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	o.Instructions = text
	o.Inputs = inputs
	return r.emit(o)
}

// preparePushPR writes finish/pr.md and fills the push_pr op's title and
// body path from it (#36). Nothing here is best-effort: a run that reaches
// the pull request has a spec, and a goals-on run has goals.md and — unless
// it never wrote one — a readable finish/goals.json, so a read that fails is
// a broken bundle, not a body with a section quietly missing. The one
// absence that is not an error is a goals record that does not exist:
// finish.ReadGoals reports it as (nil, nil) and every goal is then rendered
// "not assessed" — which a record that exists but cannot be decoded is
// never rendered as. That last one is stopped before this: the finish facts
// decode the same file on the way to deciding this op, so the call has
// already failed with the path factsHint names. The check below is what
// makes the rule hold at this end too, whichever reader gets there first.
func (r *nextRun) preparePushPR(data *brief.RunData, inputs map[string]any) error {
	spec, err := os.ReadFile(filepath.Join(r.bdir, "spec.md"))
	if err != nil {
		return err
	}
	items, err := r.prGoals()
	if err != nil {
		return err
	}
	rec, err := finish.ReadGoals(r.bdir)
	if err != nil {
		return err
	}
	// An external bundle is not in the repository, so it has no relative
	// path to point a reader at; its own directory is the best pointer.
	rel := bundleRel(r.ws, r.bdir)
	if rel == "" {
		rel = r.bdir
	}
	pr := finish.BuildPR(string(spec), r.st.Topic, items, rec, rel)
	if err = finish.WritePR(r.bdir, pr.Body); err != nil {
		return err
	}
	data.PRTitle, data.PRBodyPath = pr.Title, finish.PRPath(r.bdir)
	inputs["pr_title"] = pr.Title
	inputs["pr_body_path"] = data.PRBodyPath
	return nil
}

// prGoals is the goal list the pull request body renders, or nil when the
// run has no goals — the one case in which the body omits the section
// entirely. A goals-on run whose goals.md is unreadable or unparsable is an
// error: a body silently missing its goals is exactly what this op must not
// produce. Both errors name the file on their own — the one [os.ReadFile]
// returns carries the path, every goals.Parse message opens with "goals.md:"
// — so neither is wrapped, exactly as the other callers of Parse leave them.
func (r *nextRun) prGoals() ([]goals.Goal, error) {
	if !r.st.Config.Goals {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(r.bdir, "goals.md"))
	if err != nil {
		return nil, err
	}
	g, err := goals.Parse(b)
	if err != nil {
		return nil, err
	}
	return g.Items, nil
}

// plannerSchema is quoted into the planner brief (spec §7.3). spec_hash is
// left blank: the planner has no Bash and no way to compute a sha256, so
// takt stamps the real hash of spec.md when the plan is recorded, replacing
// whatever the agent wrote here (review fix round 1).
const plannerSchema = `{ "schema": 1, "spec_hash": "", "tasks": [ { "id": 1, ` +
	`"title": "…", "description": "…", "files": ["path/relative/to/repo"], "verify": ["go test ./pkg/..."], ` +
	`"depends_on": [], "goals": ["G1"], "class": "implement" } ] }`

// reviewerFor selects the configured reviewer and its backend settings.
func reviewerFor(ws *workspace, env Env) (backend.Reviewer, config.Backend, error) {
	reg := backend.Registry(env.Getenv)
	r, err := backend.Select(context.Background(), ws.Cfg.Backends.Reviewer, reg)
	if err != nil {
		return nil, config.Backend{}, err
	}
	switch r.Name() {
	case "copilot":
		return r, ws.Cfg.Backends.Copilot, nil
	case "claude":
		return r, ws.Cfg.Backends.Claude, nil
	}
	return r, config.Backend{Model: "fake", Effort: "low", Timeout: config.Duration(time.Minute)}, nil
}
