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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/deadline"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// rubricTask names the per-task review rubric and its brief template.
const rubricTask = "task"

// rubricTaskFollowup names the scoped confirming pass's rubric and brief
// template (two-layers design §3.5, D6) — the one more backend call a
// confirmed blocking internal finding buys when the blind pass missed it.
const rubricTaskFollowup = "task-followup"

// cmdCloseWave closes the active wave: scope verify, verify commands,
// review, and the wave commit (spec §7.4 step 4).
//
// It runs in two deadlines rather than one. Opening the run and asking
// whether this dispatch's close has already landed is bounded by
// [deadline.Bootstrap] — a little git and a few small reads — and a landed
// close is answered from there alone, with no plan index read at all, so a
// replayed `exec close-wave` stays the no-op review I1 asks for even for a
// bundle whose plan.index.json has since gone missing or malformed.
//
// Only a close that still has work to do reads the index, and the close it
// then runs is bounded by what that work actually costs
// ([deadline.Close] of [closeBudget]) instead of by a fixed constant that
// budgets nothing. The index is read once and threaded on, so the plan the
// budget was counted from is the plan the close is graded against.
func cmdCloseWave(env Env) int {
	fs := flag.NewFlagSet("close-wave", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline.Bootstrap)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	if tgt.st.ActiveWave == nil {
		return fail(env.Stderr, exitError, "no active wave", "run `takt next`")
	}
	c, err := landedClose(ctx, tgt, tgt.st.ActiveWave)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if c == nil {
		if c, err = closeWaveBudgeted(env, tgt); err != nil {
			return fail(env.Stderr, exitError, err.Error(), "")
		}
	}
	return printJSON(env, map[string]any{
		keyWave: c.Wave, keyAttempt: c.Attempt, keyCommitted: c.Committed, "commit": c.CommitSHA,
		"nothing_to_commit": c.NothingToCommit,
		"failed":            c.Failed, "blocked": c.Blocked, statusRework: c.Rework,
		"review_errors": c.ReviewErrors, "reverted": c.Reverted,
	})
}

// closeWaveBudgeted reads the plan index once and runs the close under the
// deadline the wave's own work asks for: verify_timeout per command across
// its pending tasks, the backend's timeout per review round, plus overhead.
//
// A readIndex failure is the command's failure. Falling back to a zero
// [deadline.Budget] would floor the close at [deadline.Floor] while it ran
// real work — the very containment this budget exists to establish — and
// closeWave cannot grade a task whose plan entry it does not have anyway.
func closeWaveBudgeted(env Env, tgt *runTarget) (*wave.CloseResult, error) {
	idx, err := readIndex(tgt.bdir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(
		context.Background(), deadline.Close(closeBudget(tgt.ws.Cfg, tgt.st, idx)))
	defer cancel()
	return closeWave(ctx, env, tgt, idx)
}

// closeBudget is the work this close-wave has to fit into, counted from the
// run's config and the plan it is about to grade against.
//
// The counted set is the active wave's PENDING tasks — exactly the set
// resolveTaskResults grades, which after a recovery can hold more tasks than
// the dispatch's own list, and which is also the set internal/decide counts
// for the session's matching deadline. A task the index does not hold
// contributes no verify commands (closeWave cannot run commands it cannot
// read); reviews are counted only when the run has review.tasks on, since a
// run without it makes no backend call at all.
func closeBudget(cfg config.Config, st *bundle.State, idx plan.Index) deadline.Budget {
	b := deadline.Budget{
		VerifyTimeout:  time.Duration(cfg.VerifyTimeout),
		BackendTimeout: time.Duration(cfg.Backends.ReviewBudgetTimeout()),
		MaxParallel:    st.Config.MaxParallel,
	}
	if st.ActiveWave == nil {
		return b
	}
	tasks := 0
	for _, t := range st.Tasks {
		if t.Wave != st.ActiveWave.N || t.Status != bundle.StatusPending {
			continue
		}
		tasks++
		if pt := idx.Task(t.ID); pt != nil {
			b.VerifyCommands += len(pt.Verify)
		}
	}
	if st.Config.Review.Tasks {
		b.ReviewTasks = tasks
	}
	return b
}

// closeWave grades every pending task of the active wave that reported, then
// commits the wave when all of them are done. Its caller has already
// established that this dispatch has no landed close and has parsed the plan
// index it is handed, so re-running `close-wave` after a successful close
// never reaches here: the record is reprinted and nothing is written, and an
// `exec close-wave` the session replays cannot grade a second time or make a
// second commit (review I1, spec §5.4).
func closeWave(ctx context.Context, env Env, tgt *runTarget, idx plan.Index) (*wave.CloseResult, error) {
	aw := tgt.st.ActiveWave
	internalRec, err := wave.ReadInternalRecord(tgt.bdir, aw.N, sliceOf(aw), aw.Attempt)
	if err != nil {
		return nil, err
	}
	internalByTask := map[int][]wave.InternalFinding{}
	if internalRec != nil {
		internalByTask = internalRec.ConfirmedByTask()
	}
	res := wave.CloseResult{
		Wave: aw.N, Slice: sliceOf(aw), Attempt: aw.Attempt, ClosedAt: timeNow(),
		Failed: []int{}, Blocked: []int{}, Rework: []int{}, ReviewErrors: []int{},
	}
	sc, err := verifyWaveScope(ctx, tgt, &res)
	if err != nil {
		return nil, err
	}
	if err = resolveTaskResults(ctx, env, tgt, idx, sc, &res, internalByTask); err != nil {
		return nil, err
	}
	res.ReviewFindings = reviewFindingsOf(res.Tasks) // this attempt's own reviews, before the carry-forward
	graded := gradedIDs(res.Tasks)                   // before persistClose carries earlier rounds forward
	applyTaskStatuses(tgt.st, &res)
	res.Committed = sliceDone(tgt.st, aw.N)
	// state.json and the close record are written before the commit, so the one
	// commit spec §4.7 asks for carries them. What the commit itself did is
	// recorded afterwards, by recordCloseOutcome: a sha cannot be written
	// into the commit that carries it, and a record written first would
	// claim a commit a crash could still prevent.
	if err = persistClose(tgt, &res); err != nil {
		return nil, err
	}
	ids, err := commitWave(ctx, tgt, &res, graded)
	if err != nil {
		return nil, err
	}
	if err = recordCloseOutcome(tgt, &res, ids); err != nil {
		return nil, err
	}
	return &res, nil
}

// landedClose returns the active dispatch's close record when its commit is
// already in HEAD, and nil when the wave still has to be closed. It is what
// makes a repeated `close-wave` a no-op rather than a second grading round
// that overwrites the record and makes a duplicate wave commit (review I1).
// A record from an earlier attempt or slice is not this dispatch's answer,
// and one that claims a commit git does not have is not trusted (spec §5.4).
//
//nolint:nilnil // documented "the wave still has to be closed" sentinel, like wave.ReadClose
func landedClose(ctx context.Context, tgt *runTarget, aw *bundle.ActiveWave) (*wave.CloseResult, error) {
	c, err := wave.ReadClose(tgt.bdir, aw.N, sliceOf(aw))
	if err != nil || !closeMatchesDispatch(c, aw) {
		return nil, err
	}
	if !waveCommitLanded(ctx, tgt.ws.Repo, c) {
		return nil, nil
	}
	return c, nil
}

// gradedIDs is the tasks this close round judged, in id order — the ids its
// commit subject names, so a sliced wave reads `wave 0 — tasks 1, 2` then
// `wave 0 — tasks 3` instead of repeating the whole wave each time.
func gradedIDs(tasks []wave.TaskResult) []int {
	ids := make([]int, 0, len(tasks))
	for _, tr := range tasks {
		ids = append(ids, tr.Task)
	}
	sort.Ints(ids)
	return ids
}

// reviewFindingsOf counts the findings across the task reviews this close
// round graded — tr.Review is the grading pass, the scoped one when a scoped
// pass ran. closeWave reads it before persistClose's carryForward merges the
// retired attempt's results in, so every task review is counted exactly once,
// in the attempt that ran it (#23).
func reviewFindingsOf(tasks []wave.TaskResult) int {
	n := 0
	for _, tr := range tasks {
		if tr.Review != nil {
			n += len(tr.Review.Findings)
		}
	}
	return n
}

// verifyWaveScope partitions what changed since the baseline by the wave's
// declared files and reverts whatever no task of it owns (D6). takt's own
// bundle tree is not part of the scope question — see bundleTreeRel.
func verifyWaveScope(ctx context.Context, tgt *runTarget, res *wave.CloseResult) (wave.Scope, error) {
	aw := tgt.st.ActiveWave
	scope := map[int][]string{}
	for _, t := range tgt.st.Tasks {
		if t.Wave == aw.N {
			scope[t.ID] = t.Files
		}
	}
	touched, err := wave.TouchedSince(ctx, tgt.ws.Repo, aw.Baseline)
	if err != nil {
		return wave.Scope{}, err
	}
	rel := bundleTreeRel(tgt.ws)
	touched = slices.DeleteFunc(touched, func(p wave.Touched) bool { return underBundle(rel, p.Path) })
	sc := wave.VerifyScope(touched, scope)
	for _, o := range sc.OutOfScope {
		res.OutOfScope = append(res.OutOfScope, o.Path)
	}
	reverted, err := wave.Revert(ctx, tgt.ws.Repo, sc.OutOfScope)
	res.Reverted = reverted
	return sc, err
}

// resolveTaskResults grades every pending task of the wave that has a digest
// at or below the active attempt, then reviews the ones still done. A task
// recorded in an earlier attempt (recovery re-dispatched only its neighbours)
// closes with the wave; a task with no digest at all belongs to a later slice.
//
// internalByTask's confirmed lens findings are attached to every graded task
// — not only the ones a backend reviews — so a task that fails verify, and a
// run with review.tasks off, still carry their lens findings into the record
// and the retry brief (two-layers design §3.7).
func resolveTaskResults(
	ctx context.Context, env Env, tgt *runTarget, idx plan.Index, sc wave.Scope, res *wave.CloseResult,
	internalByTask map[int][]wave.InternalFinding,
) error {
	aw := tgt.st.ActiveWave
	skipped := reviewSkips(tgt.bdir, aw.N, aw.Attempt)
	var review []int // indexes into res.Tasks, resolved to pointers once it stops growing
	for i := range tgt.st.Tasks {
		t := &tgt.st.Tasks[i]
		if t.Wave != aw.N || t.Status != bundle.StatusPending {
			continue
		}
		d, _, err := latestDigest(tgt.bdir, aw.N, t.ID, aw.Attempt)
		if err != nil {
			return err
		}
		if d == nil {
			continue
		}
		tr := taskOutcome(ctx, tgt.ws, idx, t.ID, sc.PerTask[t.ID], d)
		res.Tasks = append(res.Tasks, tr)
		res.Tasks[len(res.Tasks)-1].Internal = internalByTask[t.ID]
		if tr.Status == bundle.StatusDone && tgt.st.Config.Review.Tasks && !skipped[t.ID] {
			review = append(review, len(res.Tasks)-1)
		}
	}
	reviewTasks(ctx, env, tgt, idx, res, review)
	carryApprovedFindings(tgt.bdir, aw.N, res)
	return nil
}

// carryApprovedFindings runs every task's follow-up carry serially, once
// reviewTasks' concurrent fan-out (bounded by max_parallel) has finished
// entirely. gate.AppendFollowUps is an unsynchronized read-modify-write of
// follow-ups.json — running the carry inside reviewOne's per-task goroutine,
// as an earlier version of this function did, let two tasks approving
// concurrently silently lose one another's items (review finding, two-layers
// design §3.7, D11). One writer, one place: a task the backend approved
// carries its internal and backend findings; a task no backend graded at all
// (review.tasks off, or its review skipped) but that still holds confirmed
// internal findings carries those alone. A carry error grades that one task
// review_error, exactly as an error from any other part of its review would
// — it does not abort the other tasks' carries.
//
// Both cases require tr.Status == bundle.StatusDone, not just an approving
// tr.Review: a scoped pass that errors leaves tr.Review holding the blind
// pass's approve (informative in the close record) but sets tr.Status to
// review_error, and that task must not be carried — it was never actually
// approved, and a later close-wave retry that does succeed would otherwise
// carry the same findings a second time (review finding).
func carryApprovedFindings(bdir string, waveN int, res *wave.CloseResult) {
	for i := range res.Tasks {
		tr := &res.Tasks[i]
		switch {
		case tr.Status == bundle.StatusDone && tr.Review != nil && tr.Review.Verdict == backend.VerdictApprove:
			if err := carryTaskFindings(bdir, waveN, tr); err != nil {
				tr.Status, tr.Reason = statusReviewError, err.Error()
			}
		case tr.Status == bundle.StatusDone && tr.Review == nil && len(tr.Internal) > 0:
			// No backend graded this task (review.tasks off, or its review
			// was skipped) — the confirmed lens findings' only route to a
			// human is follow-ups (two-layers design §3.7).
			if err := carryInternalOnly(bdir, waveN, tr); err != nil {
				tr.Status, tr.Reason = statusReviewError, err.Error()
			}
		}
	}
}

// taskOutcome grades one task from its digest: a done digest that changed
// nothing fails as no_changes, and everything else the agent claims done has
// its verify commands run fresh, before any reviewer sees it.
func taskOutcome(
	ctx context.Context, ws *workspace, idx plan.Index, id int, changed []string, d *digest,
) wave.TaskResult {
	tr := wave.TaskResult{Task: id, FilesChanged: changed, Status: d.Status}
	switch d.Status {
	case bundle.StatusBlocked:
		tr.Reason = d.Blockers
	case bundle.StatusFailed:
		tr.Reason = "agent: " + d.Summary
	case bundle.StatusDone:
		if len(changed) == 0 {
			tr.Status, tr.Reason = bundle.StatusFailed, "no_changes"
			return tr
		}
		if pt := idx.Task(id); pt != nil {
			tr.Verify = wave.RunVerify(ctx, ws.Repo.Root, pt.Verify, time.Duration(ws.Cfg.VerifyTimeout))
		}
		for _, v := range tr.Verify {
			if !v.Passed {
				tr.Status, tr.Reason = bundle.StatusFailed, "verify"
				break
			}
		}
	}
	return tr
}

// applyTaskStatuses writes each freshly graded result back onto its task. A
// task sent back for rework stays pending: that is what lets the loop
// re-dispatch it without a gate (spec §5.3 row 16).
func applyTaskStatuses(st *bundle.State, res *wave.CloseResult) {
	for _, tr := range res.Tasks {
		t := st.Task(tr.Task)
		if t == nil {
			continue
		}
		switch tr.Status {
		case bundle.StatusDone:
			t.Status = bundle.StatusDone
		case bundle.StatusFailed:
			t.Status = bundle.StatusFailed
		case bundle.StatusBlocked:
			t.Status = bundle.StatusBlocked
		}
	}
}

// summarise rebuilds the close record's summary lists from the merged task
// results — the lists decide reads to raise the wave_failures gate and to
// name what is in it. It must run after carryForward: a close grades only
// pending tasks, so a task that failed in an earlier round is not graded
// again, yet it is exactly what the returning gate has to name (review
// finding N1). A task the user has since waived, or that is done, is left
// out: it is no longer holding the wave and must not re-raise its own gate.
func summarise(st *bundle.State, res *wave.CloseResult) {
	res.Failed, res.Blocked, res.Rework, res.ReviewErrors = []int{}, []int{}, []int{}, []int{}
	for _, tr := range res.Tasks {
		if t := st.Task(tr.Task); t != nil &&
			(t.Status == bundle.StatusDone || t.Status == bundle.StatusWaived) {
			continue
		}
		switch tr.Status {
		case bundle.StatusFailed:
			res.Failed = append(res.Failed, tr.Task)
		case bundle.StatusBlocked:
			res.Blocked = append(res.Blocked, tr.Task)
		case statusRework:
			res.Rework = append(res.Rework, tr.Task)
		case statusReviewError:
			res.ReviewErrors = append(res.ReviewErrors, tr.Task)
		}
	}
}

// persistClose writes the task statuses and the close record — everything
// the wave commit has to carry. A close that follows a retired one inherits
// its results for the tasks this round did not grade, so the record stays
// the whole wave's story rather than only its last round. What the commit
// did is not known yet; recordCloseOutcome adds it afterwards.
func persistClose(tgt *runTarget, res *wave.CloseResult) error {
	carryForward(tgt.bdir, res)
	summarise(tgt.st, res)
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return err
	}
	if err := wave.WriteClose(tgt.bdir, *res); err != nil {
		return err
	}
	_ = os.Remove(prevClosePath(tgt.bdir, res.Wave, res.Slice))
	return nil
}

// recordCloseOutcome records what the commit actually did, after it did it:
// the record is rewritten with the sha (a value that cannot exist before the
// commit it names) and the outcome is appended to the log. Both writes land
// after the wave commit and therefore sit uncommitted until the next takt
// commit picks them up — the next slice, or the execute → finish transition
// (spec §4.7). That is the price of never claiming a commit that did not
// happen: a crash between the commit and this call leaves committed:true
// with no sha, which waveCommitLanded reads as "not landed" and `next`
// reconciles by closing the wave again (spec §5.4).
func recordCloseOutcome(tgt *runTarget, res *wave.CloseResult, ids []int) error {
	if res.Committed {
		if err := wave.WriteClose(tgt.bdir, *res); err != nil {
			return err
		}
		// The wave has landed, so the baseline a retry parked for it is
		// spent: the next slice starts from the tree this commit left.
		_ = wave.DeleteBaseline(tgt.bdir, res.Wave)
	}
	if res.CommitSHA != "" {
		err := bundle.AppendEvent(tgt.bdir, "wave_committed", map[string]any{
			keyWave: res.Wave, keySlice: res.Slice, keyAttempt: res.Attempt,
			keySHA: res.CommitSHA, keyTasks: ids,
		})
		if err != nil {
			return err
		}
	}
	// slice and review_findings make the log the retro's source: the slice
	// pairs this close with its dispatch, and the count survives the record
	// a later attempt's close deletes (#23, #25).
	return bundle.AppendEvent(tgt.bdir, "wave_closed", map[string]any{
		keyWave: res.Wave, keySlice: res.Slice, keyAttempt: res.Attempt, keyCommitted: res.Committed,
		keySHA: res.CommitSHA, "nothing_to_commit": res.NothingToCommit,
		"review_findings": res.ReviewFindings, "failed": res.Failed,
		"blocked": res.Blocked, statusRework: res.Rework,
		"review_errors": res.ReviewErrors, "reverted": res.Reverted,
	})
}

// carryForward copies the retired record's task results for tasks this round
// did not grade. Nothing is overwritten: a task graded again keeps its fresh
// result.
func carryForward(bdir string, res *wave.CloseResult) {
	prev := readPrevClose(bdir, res.Wave, res.Slice)
	if prev == nil {
		return
	}
	for _, tr := range prev.Tasks {
		if !slices.ContainsFunc(res.Tasks, func(x wave.TaskResult) bool { return x.Task == tr.Task }) {
			res.Tasks = append(res.Tasks, tr)
		}
	}
	sort.Slice(res.Tasks, func(i, j int) bool { return res.Tasks[i].Task < res.Tasks[j].Task })
}

// commitWave commits the finished slice and returns the task ids its subject
// names. It guarantees the record never outlives a commit that did not
// happen: ANY failure on the way — resolving the paths, staging them, the
// commit itself — retires the record, so the next `takt next` closes the
// wave again instead of reading committed=true, clearing the wave and
// stranding the work uncommitted.
func commitWave(ctx context.Context, tgt *runTarget, res *wave.CloseResult, graded []int) ([]int, error) {
	if !res.Committed {
		return nil, nil
	}
	ids, err := commitWaveOnce(ctx, tgt, res, graded)
	if err != nil {
		return nil, errors.Join(err, dropClose(tgt.bdir, res.Wave, res.Slice))
	}
	return ids, nil
}

// commitWaveOnce stages the files of every done or waived task of the wave
// plus the bundle and commits exactly those (spec §4.7, §7.4 step 5). A wave
// with nothing left to record makes no commit rather than crashing on an
// empty pathspec — a decided outcome, not a failure, so it stays committed
// and says so with nothing_to_commit.
func commitWaveOnce(ctx context.Context, tgt *runTarget, res *wave.CloseResult, graded []int) ([]int, error) {
	paths, done, err := doneWaveFiles(ctx, tgt, res.Wave)
	if err != nil {
		return nil, err
	}
	rel := ""
	if tgt.ws.Dir.InRepo {
		if rel, err = tgt.ws.Dir.RelToRepo(tgt.bdir); err != nil {
			return nil, err
		}
	}
	staged, err := stageWave(ctx, tgt.ws.Repo, paths, rel)
	if err != nil {
		return nil, err
	}
	if !staged {
		// Nothing of this wave's making is left in the tree — every file is
		// already in HEAD, or an external bundle's wave was waived away.
		// There is nothing to commit, which is not a failure: recording it
		// as committed:false would raise a wave_failures gate naming nobody
		// (review M2), so the record says committed with no sha instead.
		res.NothingToCommit = true
		_ = bundle.AppendEvent(tgt.bdir, "wave_commit_skipped", map[string]any{
			keyWave: res.Wave, keyReason: "nothing staged under the wave's pathspec",
		})
		return nil, nil
	}
	// The commit stages the whole wave's done and waived files — an earlier
	// slice's are already in HEAD, so staging them costs nothing — but what
	// this commit is *about* is this slice, so the subject and the recorded
	// task list are narrowed to the tasks that went out with it.
	mine := inSlice(tgt.st.ActiveWave, done)
	ids := graded
	if len(ids) == 0 {
		ids = mine
	}
	msg := waveSubject(tgt.st, tgt.slug, res.Wave, graded, mine)
	sha, err := wave.CommitWave(ctx, tgt.ws.Repo, paths, rel, msg)
	if err != nil {
		return nil, err
	}
	res.CommitSHA = sha
	return ids, nil
}

// waveSubject names what the commit records: the tasks this close graded,
// else — for a re-close that graded nothing — the done tasks its caller
// hands it (this slice's, see commitWaveOnce), else the tasks it waived,
// because a wave whose dispatched work was all waived still has its bundle
// to commit and should say so rather than trail off after "tasks".
func waveSubject(st *bundle.State, slug string, waveN int, graded, done []int) string {
	prefix := fmt.Sprintf("takt(%s): wave %d — ", slug, waveN)
	switch {
	case len(graded) > 0:
		return prefix + "tasks " + joinInts(graded)
	case len(done) > 0:
		return prefix + "tasks " + joinInts(done)
	}
	var waived []int
	for _, t := range st.Tasks {
		if t.Wave == waveN && t.Status == bundle.StatusWaived {
			waived = append(waived, t.ID)
		}
	}
	if len(waived) > 0 {
		return prefix + "waived " + joinInts(waived)
	}
	return prefix + "close"
}

// inSlice narrows a list of the wave's task ids to the ones the dispatch on
// the table went out with. A wave larger than max_parallel is closed once
// per slice, so a question asked about "the wave's done tasks" is answered
// across every slice that has already committed — which is exactly what a
// later slice must not claim as its own. With no active wave (nothing to
// narrow against) the list is unchanged.
func inSlice(aw *bundle.ActiveWave, ids []int) []int {
	if aw == nil {
		return ids
	}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if slices.Contains(aw.Tasks, id) {
			out = append(out, id)
		}
	}
	return out
}

// stageWave stages the wave's pathspec and reports whether it holds anything
// to commit. Both questions are asked about that pathspec alone, so a file
// the user staged themselves is neither counted nor committed (spec §4.7).
func stageWave(ctx context.Context, repo *gitx.Repo, paths []string, bundleRel string) (bool, error) {
	spec := slices.Clone(paths)
	if bundleRel != "" {
		spec = append(spec, bundleRel)
	}
	if len(spec) == 0 {
		return false, nil
	}
	if err := repo.AddPathspec(ctx, spec...); err != nil {
		return false, err
	}
	return repo.HasStagedIn(ctx, spec...)
}

// doneWaveFiles is every declared file of the wave's done and waived tasks,
// plus the ids of the done ones. The files of a task that finished in an
// earlier attempt are included: its edits are still uncommitted, so the wave
// commit is what records them. A waived task's files are included too —
// spec §7.4 step 5 commits them "as they stand", and leaving them behind
// strands half-finished work in the tree for the next wave's scope check to
// revert (review I8) — but a waiver is not an achievement, so it adds no id
// to the commit subject's task list. A declared file that was never created
// is dropped: `git add` fails on a pathspec that matches nothing at all.
func doneWaveFiles(ctx context.Context, tgt *runTarget, waveN int) ([]string, []int, error) {
	var files []string
	var ids []int
	for _, t := range tgt.st.Tasks {
		if t.Wave != waveN || (t.Status != bundle.StatusDone && t.Status != bundle.StatusWaived) {
			continue
		}
		if t.Status == bundle.StatusDone {
			ids = append(ids, t.ID)
		}
		for _, f := range t.Files {
			present, err := existsOrTracked(ctx, tgt.ws.Repo, f)
			if err != nil {
				return nil, nil, err
			}
			if present {
				files = append(files, f)
			}
		}
	}
	sort.Ints(ids)
	return files, ids, nil
}

// existsOrTracked reports whether a repo-relative path is something git can
// be given as a pathspec: it is in the working tree, or in HEAD (and so
// possibly deleted by the task).
func existsOrTracked(ctx context.Context, repo *gitx.Repo, rel string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repo.Root, filepath.FromSlash(rel))); err == nil {
		return true, nil
	}
	return repo.InHead(ctx, rel)
}

// sliceDone reports whether everything the wave has actually dispatched is
// finished: every task of it that has ever run (attempt > 0) is done or
// waived. A wave larger than max_parallel is dispatched in slices, and spec
// §7.4 commits once per slice, so a task of a later slice — never dispatched,
// still at attempt 0 — must not hold the finished slice's work hostage;
// a task that did run and failed must (it is graded by this close even when
// recovery re-dispatched only its neighbours).
func sliceDone(st *bundle.State, waveN int) bool {
	for _, t := range st.Tasks {
		if t.Wave != waveN || t.Attempt == 0 {
			continue
		}
		if t.Status != bundle.StatusDone && t.Status != bundle.StatusWaived {
			return false
		}
	}
	return true
}

// joinInts renders task ids for a commit subject.
func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ", ")
}

// reviewSkips returns the task ids a review_skipped event covers for this
// wave and attempt.
func reviewSkips(bdir string, waveN, attempt int) map[int]bool {
	out := map[int]bool{}
	events, _ := bundle.ReadEvents(bdir)
	for _, e := range events {
		if e.Type != "review_skipped" || toInt(e.Data[keyWave]) != waveN || toInt(e.Data[keyAttempt]) != attempt {
			continue
		}
		if list, ok := e.Data[keyTasks].([]any); ok {
			for _, v := range list {
				out[toInt(v)] = true
			}
		}
	}
	return out
}

// toInt reads a number out of decoded event data, which is float64 after a
// JSON round trip; -1 for anything else, so a malformed event matches nothing.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return -1
}

// reviewTasks runs the per-task reviews concurrently, bounded by
// max_parallel. A reviewer that cannot be selected at all fails every task's
// review the same way one that errored would: fail closed, ask the user.
func reviewTasks(
	ctx context.Context, env Env, tgt *runTarget, idx plan.Index, res *wave.CloseResult, at []int,
) {
	if len(at) == 0 {
		return
	}
	reviewer, be, err := reviewerFor(tgt.ws, env)
	if err != nil {
		for _, i := range at {
			res.Tasks[i].Status, res.Tasks[i].Reason = statusReviewError, err.Error()
		}
		return
	}
	sem := make(chan struct{}, tgt.st.Config.MaxParallel)
	var wg sync.WaitGroup
	for _, i := range at {
		wg.Add(1)
		go func(tr *wave.TaskResult) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			reviewOne(ctx, tgt, idx, reviewer, be, res.Wave, tr, tr.Internal)
		}(&res.Tasks[i])
	}
	wg.Wait()
}

// reviewOne reviews one task's diff and turns the verdict into its status:
// approve keeps it done, rework sends it back pending with findings, reject
// fails it, and anything else is a review error the user resolves. When the
// blind pass approves over a confirmed blocking internal finding, a scoped
// pass grades instead (two-layers design §3.5, D6): its verdict, not the
// blind one, decides the task, and the blind result is kept aside as
// tr.BlindReview.
func reviewOne(
	ctx context.Context, tgt *runTarget, idx plan.Index,
	reviewer backend.Reviewer, be config.Backend, waveN int, tr *wave.TaskResult, internal []wave.InternalFinding,
) {
	pt := idx.Task(tr.Task)
	if pt == nil {
		tr.Status, tr.Reason = statusReviewError, fmt.Sprintf("task %d is not in the plan index", tr.Task)
		return
	}
	prompt, err := reviewPrompt(ctx, tgt, pt, tr)
	if err != nil {
		tr.Status, tr.Reason = statusReviewError, err.Error()
		return
	}
	res, err := reviewer.Review(ctx, backend.ReviewRequest{
		Rubric: rubricTask, Title: pt.Title, Prompt: prompt, RepoRoot: tgt.ws.Repo.Root,
		Model: be.Model, Effort: be.Effort, Timeout: time.Duration(be.Timeout),
		LogDir: filepath.Join(tgt.bdir, "logs"),
		LogID:  fmt.Sprintf("review-task-%d-%d", tr.Task, time.Now().Unix()),
	})
	if err != nil {
		tr.Status, tr.Reason = statusReviewError, err.Error()
		return
	}
	tr.Review = &res
	tr.Internal = internal
	findingsPath := filepath.Join(tgt.bdir, "reviews", "wave-"+strconv.Itoa(waveN), fmt.Sprintf("task-%d.md", tr.Task))
	findingsTitle := fmt.Sprintf("%s task %d", tgt.slug, tr.Task)
	if res.Verdict == backend.VerdictApprove && hasBlockingInternal(internal) {
		scoped, serr := scopedTaskReview(ctx, tgt, reviewer, be, pt, tr)
		if serr != nil {
			tr.Status, tr.Reason = statusReviewError, serr.Error()
			// The blind pass genuinely completed — tr.Review still holds it,
			// left there deliberately (see carryApprovedFindings) — so the
			// human resolving this review_error still gets the full story:
			// the blind verdict and the confirmed internal findings that
			// bought the scoped pass which then failed to answer.
			_ = writeTaskFindings(findingsPath, findingsTitle, tr)
			return
		}
		_ = bundle.AppendEvent(tgt.bdir, "review_scoped", map[string]any{
			keyWave: waveN, keyTask: tr.Task, "blind_verdict": res.Verdict, keyVerdict: scoped.Verdict,
		})
		tr.BlindReview = tr.Review
		tr.Review = &scoped
	}
	_ = writeTaskFindings(findingsPath, findingsTitle, tr)
	switch tr.Review.Verdict {
	case backend.VerdictApprove:
		// Carried by carryApprovedFindings, serially, after every task's
		// review has finished (see its doc comment) — not here, where this
		// function runs concurrently across the wave's reviewed tasks.
	case backend.VerdictRework:
		tr.Status, tr.Reason = statusRework, tr.Review.Summary
	case backend.VerdictReject:
		tr.Status, tr.Reason = bundle.StatusFailed, "review: "+tr.Review.Summary
	default:
		tr.Status, tr.Reason = statusReviewError, tr.Review.Reason
	}
}

// writeTaskFindings renders one task's review findings as markdown for a
// human: renderFindings' familiar "# Review" section for the pass a reader
// still wants to see in full — tr.Review normally, or tr.BlindReview when a
// scoped pass replaced it — plus, when a scoped pass ran, a "## Scoped pass"
// section naming the confirmed claims it was asked about and the verdict
// that actually graded the task, and a "## Internal findings (confirmed)"
// section listing every confirmed lens finding with its lens attribution.
// Unlike the scoped pass's own prompt (design D6), this file is not
// adversarial input a prompt-injection can reach, so naming the lens here is
// safe. The plain case — no scoped pass, no confirmed findings — is
// byte-for-byte what writeFindings alone produces.
//
// The whole document is built first and written once, atomically: an
// earlier version wrote the review section and then re-opened the file to
// append the rest, which left a half-written file behind on any failure
// between the two (#51).
func writeTaskFindings(path, title string, tr *wave.TaskResult) error {
	display := tr.Review
	if tr.BlindReview != nil {
		display = tr.BlindReview
	}
	var b strings.Builder
	b.WriteString(renderFindings(title, *display))
	if tr.BlindReview != nil {
		b.WriteString("\n## Scoped pass\n\nConfirmed claims put to the scoped pass:\n")
		for _, f := range tr.Internal {
			fmt.Fprintf(&b, "- %s %s:%d — %s: %s\n", f.Severity, f.File, f.Line, f.Title, f.Detail)
		}
		fmt.Fprintf(&b, "\nVerdict: %s — %s\n", tr.Review.Verdict, tr.Review.Summary)
	}
	if len(tr.Internal) > 0 {
		b.WriteString("\n## Internal findings (confirmed)\n\n")
		for _, f := range tr.Internal {
			fmt.Fprintf(&b, "- [lens:%s] %s %s:%d — %s: %s\n",
				strings.Join(f.Lenses, ","), f.Severity, f.File, f.Line, f.Title, f.Detail)
		}
	}
	return bundle.WriteFileAtomic(path, []byte(b.String()))
}

// hasBlockingInternal reports whether any confirmed lens finding for the
// task is blocking — the one disagreement that buys a scoped second backend
// pass (two-layers design D6).
func hasBlockingInternal(fs []wave.InternalFinding) bool {
	for _, f := range fs {
		if f.Severity == "blocking" {
			return true
		}
	}
	return false
}

// scopedTaskReview runs the one scoped pass: the same diff, the confirmed
// findings as distilled claims — severity file:line — title: detail, no lens
// names, no verifier evidence (design D6) — and the ordinary verdict
// semantics. Its result replaces the blind pass's as the grader.
func scopedTaskReview(
	ctx context.Context, tgt *runTarget,
	reviewer backend.Reviewer, be config.Backend, pt *plan.Task, tr *wave.TaskResult,
) (backend.ReviewResult, error) {
	tok, err := brief.Token()
	if err != nil {
		return backend.ReviewResult{}, err
	}
	prior := make([]brief.PriorFinding, 0, len(tr.Internal))
	for _, f := range tr.Internal {
		prior = append(prior, brief.PriorFinding{
			Severity: f.Severity, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
		})
	}
	var vout strings.Builder
	for _, v := range tr.Verify {
		fmt.Fprintf(&vout, "$ %s (exit %d)\n%s\n", v.Command, v.Exit, v.Tail)
	}
	prompt, err := brief.Render("review-task-followup", brief.ReviewData{
		Gate: rubricTaskFollowup, Title: pt.Title, Token: tok, Schema: backend.ResultSchema,
		Diff: taskDiff(ctx, tgt.ws, tr.FilesChanged), TaskDescription: pt.Description,
		VerifyOutput: vout.String(), PriorFindings: prior,
	})
	if err != nil {
		return backend.ReviewResult{}, err
	}
	res, err := reviewer.Review(ctx, backend.ReviewRequest{
		Rubric: rubricTaskFollowup, Title: pt.Title, Prompt: prompt, RepoRoot: tgt.ws.Repo.Root,
		Model: be.Model, Effort: be.Effort, Timeout: time.Duration(be.Timeout),
		LogDir: filepath.Join(tgt.bdir, "logs"),
		LogID:  fmt.Sprintf("review-task-%d-scoped-%d", tr.Task, time.Now().Unix()),
	})
	if err != nil {
		return backend.ReviewResult{}, err
	}
	if res.Verdict == backend.VerdictError {
		return backend.ReviewResult{}, fmt.Errorf("scoped review failed: %s", res.Reason)
	}
	return res, nil
}

// carryInternalOnly carries a task's confirmed internal findings alone: the
// half of carryTaskFindings that applies even when no backend graded the
// task at all (spec §3.7) — review.tasks off, or its review skipped — which
// is the only route those findings have to a human without it.
func carryInternalOnly(bdir string, waveN int, tr *wave.TaskResult) error {
	items := make([]gate.FollowUp, 0, len(tr.Internal))
	for _, f := range tr.Internal {
		items = append(items, gate.FollowUp{
			Severity: f.Severity, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
			Source: gate.SourceInternal, Wave: new(waveN), Task: tr.Task, TS: timeNow(),
		})
	}
	return gate.AppendFollowUps(bdir, items...)
}

// carryTaskFindings carries what an approving verdict leaves unacted-on: the
// confirmed internal findings and the backend's own findings, each with wave
// and task, so neither dies in reviews/wave-<n>/ (design D11, D15).
func carryTaskFindings(bdir string, waveN int, tr *wave.TaskResult) error {
	if err := carryInternalOnly(bdir, waveN, tr); err != nil {
		return err
	}
	items := make([]gate.FollowUp, 0, len(tr.Review.Findings))
	for _, f := range tr.Review.Findings {
		items = append(items, gate.FollowUp{
			Severity: f.Severity, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
			Source: gate.SourceApprove, Wave: new(waveN), Task: tr.Task, TS: timeNow(),
		})
	}
	return gate.AppendFollowUps(bdir, items...)
}

// reviewPrompt renders the task reviewer's brief: the task text, the verify
// output it already passed, and its diff, all as quoted data.
func reviewPrompt(ctx context.Context, tgt *runTarget, pt *plan.Task, tr *wave.TaskResult) (string, error) {
	tok, err := brief.Token()
	if err != nil {
		return "", err
	}
	var vout strings.Builder
	for _, v := range tr.Verify {
		fmt.Fprintf(&vout, "$ %s (exit %d)\n%s\n", v.Command, v.Exit, v.Tail)
	}
	return brief.Render("review-"+rubricTask, brief.ReviewData{
		Gate: rubricTask, Title: pt.Title, Token: tok, Schema: backend.ResultSchema,
		Diff: taskDiff(ctx, tgt.ws, tr.FilesChanged), TaskDescription: pt.Description,
		VerifyOutput: vout.String(),
	})
}

// taskDiff is `git diff -- <files>` plus the full content of files that are
// not in HEAD yet, which a diff of the working tree would not show at all.
func taskDiff(ctx context.Context, ws *workspace, files []string) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	out, _ := ws.Repo.Run(ctx, append([]string{"diff", "--"}, files...)...)
	b.WriteString(out)
	for _, f := range files {
		if in, _ := ws.Repo.InHead(ctx, f); in {
			continue
		}
		if content, err := os.ReadFile(filepath.Join(ws.Repo.Root, filepath.FromSlash(f))); err == nil {
			fmt.Fprintf(&b, "\n=== new file %s ===\n%s\n", f, content)
		}
	}
	return b.String()
}
