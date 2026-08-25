package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// digestPath is waves/<n>/task-<id>.a<attempt>.digest.json.
func digestPath(bdir string, waveN, task, attempt int) string {
	return filepath.Join(waveDir(bdir, waveN), fmt.Sprintf("task-%d.a%d.digest.json", task, attempt))
}

// bundleTreeRel is takt's own directory, repo-relative, or "" when the
// bundle lives outside the repository. Everything under it — state, events,
// digests, briefs, close records, review logs — is takt's bookkeeping, which
// the wave commit stages wholesale and which no task may declare. It is
// therefore excluded from the wave baseline and from scope verification:
// without that, takt's own writes during a wave look like out-of-scope edits
// and the close would revert the very records it is writing.
func bundleTreeRel(ws *workspace) string {
	if !ws.Dir.InRepo {
		return ""
	}
	rel, err := ws.Dir.RelToRepo(ws.Dir.Base)
	if err != nil {
		return ""
	}
	return rel
}

// underBundle reports whether the repo-relative path p is inside the bundle
// tree rooted at rel. An empty rel matches nothing, so an external bundle
// excludes no path at all.
func underBundle(rel, p string) bool {
	return rel != "" && (p == rel || strings.HasPrefix(p, rel+"/"))
}

// waveCommitLanded reports whether the commit a close record claims is
// really in this branch's history. `committed` on its own cannot answer
// that: it is written before `git commit` runs, so a crash inside the
// commit — or a hook that rejected it, or a later reset — leaves a record
// claiming work that HEAD does not have. Reading the claim back off git is
// what makes `close-wave` idempotent and lets `next` reconcile instead of
// clearing the wave and stranding it (review I1/I2, spec §5.4). A close
// that had nothing of its own to stage has landed by definition.
func waveCommitLanded(ctx context.Context, repo *gitx.Repo, rec *wave.CloseResult) bool {
	if rec == nil || !rec.Committed {
		return false
	}
	if rec.NothingToCommit {
		return true
	}
	if rec.CommitSHA == "" {
		return false
	}
	if ok, err := repo.CommitExists(ctx, rec.CommitSHA); err != nil || !ok {
		return false
	}
	ok, err := repo.IsAncestor(ctx, rec.CommitSHA, "HEAD")
	return err == nil && ok
}

// pathsCommitted reports whether git has nothing outstanding at any of
// paths — nothing staged, modified or untracked. It is the second half of
// the question "did that commit really carry this wave": a commit that
// staged and recorded these files left them clean, so anything still showing
// at one of them means HEAD is not holding what the close was recording.
// An empty path list is vacuously clean (a wave whose tasks were all waived
// with nothing to show for them declares no files).
func pathsCommitted(ctx context.Context, repo *gitx.Repo, paths []string) (bool, error) {
	if len(paths) == 0 {
		return true, nil
	}
	entries, err := repo.Porcelain(ctx)
	if err != nil {
		return false, err
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	for _, e := range entries {
		if want[e.Path] || (e.OrigPath != "" && want[e.OrigPath]) {
			return false, nil
		}
	}
	return true, nil
}

// closeMatchesDispatch reports whether a close record answers the dispatch
// that is on the table now. Attempt alone cannot say: a wave larger than
// max_parallel is dispatched in slices that all run at attempt 1, so the
// committed record of the previous slice would otherwise be read as this
// slice's answer — clearing the wave, or making `close-wave` a no-op, before
// the second slice was ever graded. (attempt, slice) is what a dispatch is
// identified by, and each of the two is written by the same launch the
// record's own numbers came from.
func closeMatchesDispatch(c *wave.CloseResult, aw *bundle.ActiveWave) bool {
	return c != nil && aw != nil && c.Attempt == aw.Attempt && c.Slice == sliceOf(aw)
}

// sliceOf is the active wave's slice number, healed. A wave dispatched by a
// build from before per-slice close records has slice 0 — the number the old
// waveBaseline returned — and a close record cannot be written under it
// (wave.WriteClose refuses it, which would leave `next` asking for a
// close-wave that always exits 1). Such a wave is by definition uncommitted,
// so slice 1 is the right answer for it: nothing has committed under this
// wave yet, and close-wave is idempotent. Every read of the active wave's
// slice goes through here so the healing is the same one everywhere —
// crucially including closeMatchesDispatch, which would otherwise refuse the
// slice-1 record the healed close had just written. `takt doctor` WARNs
// about the same state (doctor.StateSchema).
func sliceOf(aw *bundle.ActiveWave) int {
	if aw == nil || aw.Slice < 1 {
		return 1
	}
	return aw.Slice
}

// gateStateValue is what state.gates records for a gate at the transition
// that leaves its phase behind. It reads the receipt rather than taking the
// transition itself as proof: a gate can be satisfied by an evidenced skip
// or by an override, and a run whose review is switched off never takes a
// receipt at all. Recording `ok` for all of those made state.gates claim a
// review that never happened, and left `takt doctor` asking the user to
// re-run one they had disabled (review I6, spec §4.3, §9).
func gateStateValue(bdir string, enabled bool, g string) string {
	if !enabled {
		return gateDisabled
	}
	events, _ := bundle.ReadEvents(bdir)
	st, err := gate.Compute(bdir, g, events)
	switch {
	case err != nil:
		return gatePending
	case st.Verdict == gateSkipped:
		return gateSkipped
	case st.Satisfied:
		return gateOK
	}
	return gatePending
}

// dropClose retires a slice's close record so the next `takt next` runs
// close-wave again. The record is renamed, not deleted: the re-close grades
// only the tasks that are still pending, so the retired copy is where the
// results of the tasks it will not grade again — their verify output,
// review findings and files_changed — are carried forward from. A record
// that is already gone is not an error.
func dropClose(bdir string, waveN, slice int) error {
	p := wave.ClosePath(bdir, waveN, slice)
	if err := os.Rename(p, prevClosePath(bdir, waveN, slice)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// prevClosePath is the retired close record dropClose renames to. It is not
// a close record any command reads by itself — wave.AllCloses skips it —
// only the source persistClose carries results forward from.
func prevClosePath(bdir string, waveN, slice int) string {
	return wave.ClosePath(bdir, waveN, slice) + ".prev"
}

// briefPath is briefs/<name> (non-task briefs) — waves/<n>/… holds task briefs.
func briefPath(bdir, name string) string { return filepath.Join(bdir, "briefs", name) }

// indexPath is the bundle's plan.index.json.
func indexPath(bdir string) string { return filepath.Join(bdir, "plan.index.json") }

// writeIndex replaces plan.index.json atomically, so a crash mid-write can
// never leave the run with a half-written plan.
func writeIndex(bdir string, idx plan.Index) error {
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := indexPath(bdir) + ".tmp"
	//nolint:gosec // G703: tmp is inside the run's bundle dir, and the slug it comes from is validated by
	// bundle.ValidSlug before it ever reaches the filesystem (see selectSlug), so no caller can steer this write.
	if err = os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, indexPath(bdir))
}

// commitBundle stages the bundle directory when it is in-repo and commits
// exactly that directory; a bundle with nothing to commit or an external
// bundle is a no-op (committed=false). Both the "is there anything to do"
// question and the commit are scoped to the bundle, so a file the user
// staged themselves is never swept in (spec §4.7). The commit sha is part
// of the interface Tasks 7–9 build on.
//
//nolint:unparam // sha is the documented first result; no caller needs it yet
func commitBundle(ctx context.Context, ws *workspace, bdir, slug, msg string) (string, bool, error) {
	if !ws.Dir.InRepo {
		return "", false, nil
	}
	rel, err := ws.Dir.RelToRepo(bdir)
	if err != nil {
		return "", false, err
	}
	if err = ws.Repo.AddPathspec(ctx, rel); err != nil {
		return "", false, err
	}
	staged, err := ws.Repo.HasStagedIn(ctx, rel)
	if err != nil || !staged {
		return "", false, err
	}
	sha, err := ws.Repo.CommitPaths(ctx, "takt("+slug+"): "+msg, rel)
	return sha, err == nil, err
}

// openGate persists an ask op as the pending gate (spec §4.3).
func openGate(bdir string, st *bundle.State, o op.Op, now time.Time) error {
	payload, err := json.Marshal(o)
	if err != nil {
		return err
	}
	st.PendingGate = &bundle.PendingGate{ID: o.Gate, OpenedAt: now, Payload: payload}
	if err = bundle.SaveState(bdir, st); err != nil {
		return err
	}
	return bundle.AppendEvent(bdir, "gate_opened", map[string]any{keyGate: o.Gate})
}

// clearGate resolves the pending gate with the user's choice.
func clearGate(bdir string, st *bundle.State, choice string) error {
	id := ""
	if st.PendingGate != nil {
		id = st.PendingGate.ID
	}
	st.PendingGate = nil
	if err := bundle.SaveState(bdir, st); err != nil {
		return err
	}
	return bundle.AppendEvent(bdir, "gate_answered", map[string]any{keyGate: id, keyChoice: choice})
}

// printOp writes the op and returns 0.
func printOp(env Env, o op.Op) int {
	if err := writeJSON(env.Stdout, o); err != nil {
		return exitError
	}
	return 0
}

// printJSON writes any command's success document and returns its exit code.
func printJSON(env Env, v any) int {
	if err := writeJSON(env.Stdout, v); err != nil {
		return exitError
	}
	return 0
}

// timeNow is the clock every bundle write stamps itself with.
func timeNow() time.Time { return time.Now().UTC() }

// errorf builds an error the command layer turns into the JSON error contract.
func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

// answerWaveGate applies a wave_failures / review_error choice (spec §7.4
// step 5). It reports whether the gate must stay open — only `stop` does.
func answerWaveGate(bdir string, st *bundle.State, gate, choice, reason string) (bool, error) {
	aw := st.ActiveWave
	switch gate + "/" + choice {
	case "wave_failures/retry":
		for i := range st.Tasks {
			if st.Tasks[i].Status == bundle.StatusFailed || st.Tasks[i].Status == bundle.StatusBlocked {
				st.Tasks[i].Status = bundle.StatusPending
			}
		}
		// Clearing active_wave sends the retry back through the launch
		// path, but two things must survive it, so both are parked
		// together. The baseline: a failed attempt leaves its half-written
		// files in the tree, and a baseline captured now would record them
		// as pre-existing — so a retry that gets the file right without
		// touching it again reads as no_changes and fails a second time
		// (review M1). And the slice number: an uncommitted slice retried
		// is that slice again, not the next one, and active_wave is where
		// that number normally lives. waveBaseline picks both back up.
		if aw != nil {
			if err := wave.SaveBaseline(bdir, aw.N, sliceOf(aw), aw.Baseline); err != nil {
				return false, err
			}
			// And the round's own record is retired rather than left to be
			// overwritten: the re-close grades only what is still pending, so
			// the results of the tasks it will not judge again — their verify
			// output, review findings and files_changed — live in the retired
			// copy until carryForward merges them back (review M2's rule,
			// applied to the path that reaches a re-close through the gate).
			if err := dropClose(bdir, aw.N, sliceOf(aw)); err != nil {
				return false, err
			}
		}
		st.ActiveWave = nil
		return false, bundle.SaveState(bdir, st)
	case "wave_failures/waive":
		// The wave stays open: `takt waive` marks the chosen tasks, and the
		// next `takt next` re-runs close-wave, which then sees the slice
		// satisfied (a waived task counts as done) and commits the work that
		// is already in the tree (spec §7.4 step 5). Tasks the user leaves
		// unwaived simply bring the gate back.
		if aw == nil {
			return false, nil
		}
		return false, dropClose(bdir, aw.N, sliceOf(aw))
	case "review_error/retry":
		if aw == nil {
			return false, nil
		}
		return false, dropClose(bdir, aw.N, sliceOf(aw))
	case "review_error/skip":
		return false, skipTaskReviews(bdir, aw, reason)
	case "wave_failures/stop", "review_error/stop":
		return true, nil
	}
	return false, errorf("unknown choice %q for %s", choice, gate)
}

// skipTaskReviews records the evidenced skip that suppresses review for the
// tasks whose reviewer errored, and drops the close record so the wave is
// closed again — this time without asking the reviewer (spec §9: a skip is
// an outage the user can point at, never a convenience).
func skipTaskReviews(bdir string, aw *bundle.ActiveWave, reason string) error {
	if strings.TrimSpace(reason) == "" || aw == nil {
		return errors.New("skipping reviews needs --reason and an active wave")
	}
	tasks := []int{}
	if c, _ := wave.ReadClose(bdir, aw.N, sliceOf(aw)); c != nil {
		tasks = c.ReviewErrors
	}
	err := bundle.AppendEvent(bdir, "review_skipped", map[string]any{
		keyWave: aw.N, keyAttempt: aw.Attempt, keyTasks: tasks, keyReason: reason,
	})
	if err != nil {
		return err
	}
	return dropClose(bdir, aw.N, sliceOf(aw))
}
