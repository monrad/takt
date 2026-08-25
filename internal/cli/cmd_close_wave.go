package cli

import (
	"context"
	"encoding/json"
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
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// closeWaveTimeout bounds the whole close — scope, verify commands and every
// review — matching the 1800 s the exec op tells the session to allow.
const closeWaveTimeout = 30 * time.Minute

// rubricTask names the per-task review rubric and its brief template.
const rubricTask = "task"

// cmdCloseWave closes the active wave: scope verify, verify commands,
// review, and the wave commit (spec §7.4 step 4).
func cmdCloseWave(env Env) int {
	fs := flag.NewFlagSet("close-wave", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeWaveTimeout)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	if tgt.st.ActiveWave == nil {
		return fail(env.Stderr, exitError, "no active wave", "run `takt next`")
	}
	c, err := closeWave(ctx, env, tgt)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{
		keyWave: c.Wave, keyAttempt: c.Attempt, keyCommitted: c.Committed, "commit": c.CommitSHA,
		"failed": c.Failed, "blocked": c.Blocked, statusRework: c.Rework,
		"review_errors": c.ReviewErrors, "reverted": c.Reverted,
	})
}

// closeWave grades every pending task of the active wave that reported, then
// commits the wave when all of them are done.
func closeWave(ctx context.Context, env Env, tgt *runTarget) (*wave.CloseResult, error) {
	aw := tgt.st.ActiveWave
	idx, err := readIndex(tgt.bdir)
	if err != nil {
		return nil, err
	}
	res := wave.CloseResult{
		Wave: aw.N, Attempt: aw.Attempt, ClosedAt: timeNow(),
		Failed: []int{}, Blocked: []int{}, Rework: []int{}, ReviewErrors: []int{},
	}
	sc, err := verifyWaveScope(ctx, tgt, &res)
	if err != nil {
		return nil, err
	}
	if err = resolveTaskResults(ctx, env, tgt, idx, sc, &res); err != nil {
		return nil, err
	}
	graded := gradedIDs(res.Tasks) // before persistClose carries earlier rounds forward
	applyTaskStatuses(tgt.st, &res)
	res.Committed = sliceDone(tgt.st, aw.N)
	// state.json, close.json and the event are written before the commit, so
	// the one commit spec §4.7 asks for carries them and leaves the tree
	// clean. That is also why close.json holds no commit sha: it is inside
	// the commit it would have to name.
	if err = persistClose(tgt, &res); err != nil {
		return nil, err
	}
	if err = commitWave(ctx, tgt, &res, graded); err != nil {
		return nil, err
	}
	return &res, nil
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
func resolveTaskResults(
	ctx context.Context, env Env, tgt *runTarget, idx plan.Index, sc wave.Scope, res *wave.CloseResult,
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
		if tr.Status == bundle.StatusDone && tgt.st.Config.Review.Tasks && !skipped[t.ID] {
			review = append(review, len(res.Tasks)-1)
		}
	}
	reviewTasks(ctx, env, tgt, idx, res, review)
	return nil
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

// applyTaskStatuses writes each result back onto the task and fills the close
// record's summary lists. A task sent back for rework stays pending: that is
// what lets the loop re-dispatch it without a gate (spec §5.3 row 16).
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
			res.Failed = append(res.Failed, tr.Task)
		case bundle.StatusBlocked:
			t.Status = bundle.StatusBlocked
			res.Blocked = append(res.Blocked, tr.Task)
		case statusRework:
			res.Rework = append(res.Rework, tr.Task)
		case statusReviewError:
			res.ReviewErrors = append(res.ReviewErrors, tr.Task)
		}
	}
}

// persistClose writes the task statuses, the close record and the event —
// everything the wave commit has to carry. A close that follows a retired
// one inherits its results for the tasks this round did not grade, so the
// record stays the whole wave's story rather than only its last round.
func persistClose(tgt *runTarget, res *wave.CloseResult) error {
	carryForward(tgt.bdir, res)
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return err
	}
	if err := wave.WriteClose(tgt.bdir, *res); err != nil {
		return err
	}
	_ = os.Remove(prevClosePath(tgt.bdir, res.Wave))
	return bundle.AppendEvent(tgt.bdir, "wave_closed", map[string]any{
		keyWave: res.Wave, keyAttempt: res.Attempt, keyCommitted: res.Committed,
		"failed": res.Failed, "blocked": res.Blocked, statusRework: res.Rework,
		"review_errors": res.ReviewErrors, "reverted": res.Reverted,
	})
}

// carryForward copies the retired record's task results for tasks this round
// did not grade. Nothing is overwritten: a task graded again keeps its fresh
// result.
func carryForward(bdir string, res *wave.CloseResult) {
	b, err := os.ReadFile(prevClosePath(bdir, res.Wave))
	if err != nil {
		return
	}
	var prev wave.CloseResult
	if err = json.Unmarshal(b, &prev); err != nil {
		return
	}
	for _, tr := range prev.Tasks {
		if !slices.ContainsFunc(res.Tasks, func(x wave.TaskResult) bool { return x.Task == tr.Task }) {
			res.Tasks = append(res.Tasks, tr)
		}
	}
	sort.Slice(res.Tasks, func(i, j int) bool { return res.Tasks[i].Task < res.Tasks[j].Task })
}

// commitWave commits the finished slice, and guarantees the record never
// outlives a commit that did not happen: ANY failure on the way — resolving
// the paths, staging them, the commit itself — retires close.json, so the
// next `takt next` closes the wave again instead of reading committed=true,
// clearing the wave and stranding the work uncommitted.
func commitWave(ctx context.Context, tgt *runTarget, res *wave.CloseResult, graded []int) error {
	if !res.Committed {
		return nil
	}
	if err := commitWaveOnce(ctx, tgt, res, graded); err != nil {
		return errors.Join(err, dropClose(tgt.bdir, res.Wave))
	}
	return nil
}

// commitWaveOnce stages the files of every done task of the wave plus the
// bundle and commits exactly those (spec §4.7). A wave with nothing left to
// record is not committed rather than crashing on an empty pathspec — that
// is a decided outcome, not a failure, so it returns nil and keeps its
// record.
func commitWaveOnce(ctx context.Context, tgt *runTarget, res *wave.CloseResult, graded []int) error {
	paths, ids, err := doneWaveFiles(ctx, tgt, res.Wave)
	if err != nil {
		return err
	}
	rel := ""
	if tgt.ws.Dir.InRepo {
		if rel, err = tgt.ws.Dir.RelToRepo(tgt.bdir); err != nil {
			return err
		}
	}
	staged, err := stageWave(ctx, tgt.ws.Repo, paths, rel)
	if err != nil {
		return err
	}
	if !staged {
		// Nothing of this wave's making is left in the tree — every file is
		// already in HEAD, or the wave was waived away. Recording it as
		// committed would claim a commit that never happened, so the close
		// stands with committed=false and the reason in the log.
		res.Committed = false
		_ = bundle.AppendEvent(tgt.bdir, "wave_commit_skipped", map[string]any{
			keyWave: res.Wave, keyReason: "nothing staged under the wave's pathspec",
		})
		return wave.WriteClose(tgt.bdir, *res)
	}
	named := graded
	if len(named) == 0 {
		named = ids // a re-close that graded nothing still names what it records
	}
	msg := fmt.Sprintf("takt(%s): wave %d — tasks %s", tgt.slug, res.Wave, joinInts(named))
	sha, err := wave.CommitWave(ctx, tgt.ws.Repo, paths, rel, msg)
	if err != nil {
		return err
	}
	res.CommitSHA = sha
	return nil
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

// doneWaveFiles is every declared file of the wave's done tasks, and their
// ids. The files of a task that finished in an earlier attempt are included:
// its edits are still uncommitted, so the wave commit is what records them.
// A declared file that was never created is dropped — `git add` fails on a
// pathspec that matches nothing at all.
func doneWaveFiles(ctx context.Context, tgt *runTarget, waveN int) ([]string, []int, error) {
	var files []string
	var ids []int
	for _, t := range tgt.st.Tasks {
		if t.Wave != waveN || t.Status != bundle.StatusDone {
			continue
		}
		ids = append(ids, t.ID)
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
			reviewOne(ctx, tgt, idx, reviewer, be, res.Wave, tr)
		}(&res.Tasks[i])
	}
	wg.Wait()
}

// reviewOne reviews one task's diff and turns the verdict into its status:
// approve keeps it done, rework sends it back pending with findings, reject
// fails it, and anything else is a review error the user resolves.
func reviewOne(
	ctx context.Context, tgt *runTarget, idx plan.Index,
	reviewer backend.Reviewer, be config.Backend, waveN int, tr *wave.TaskResult,
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
	_ = writeFindings(
		filepath.Join(tgt.bdir, "reviews", "wave-"+strconv.Itoa(waveN), fmt.Sprintf("task-%d.md", tr.Task)),
		fmt.Sprintf("%s task %d", tgt.slug, tr.Task), res)
	switch res.Verdict {
	case backend.VerdictApprove:
	case backend.VerdictRework:
		tr.Status, tr.Reason = statusRework, res.Summary
	case backend.VerdictReject:
		tr.Status, tr.Reason = bundle.StatusFailed, "review: "+res.Summary
	default:
		tr.Status, tr.Reason = statusReviewError, res.Reason
	}
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
