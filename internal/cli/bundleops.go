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

// dropClose retires a wave's close record so the next `takt next` runs
// close-wave again. The record is renamed, not deleted: the re-close grades
// only the tasks that are still pending, so the retired copy is where the
// results of the tasks it will not grade again — their verify output,
// review findings and files_changed — are carried forward from. A record
// that is already gone is not an error.
func dropClose(bdir string, waveN int) error {
	p := wave.ClosePath(bdir, waveN)
	if err := os.Rename(p, prevClosePath(bdir, waveN)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// prevClosePath is the retired close record dropClose renames to. It is not
// a close record any command reads by itself — wave.ReadClose looks for
// close.json — only the source persistClose carries results forward from.
func prevClosePath(bdir string, waveN int) string { return wave.ClosePath(bdir, waveN) + ".prev" }

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
func answerWaveGate(
	_ context.Context, _ *workspace, bdir string, st *bundle.State, gate, choice, reason string,
) (bool, error) {
	aw := st.ActiveWave
	switch gate + "/" + choice {
	case "wave_failures/retry":
		for i := range st.Tasks {
			if st.Tasks[i].Status == bundle.StatusFailed || st.Tasks[i].Status == bundle.StatusBlocked {
				st.Tasks[i].Status = bundle.StatusPending
			}
		}
		st.ActiveWave = nil // the next launch captures a fresh baseline
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
		return false, dropClose(bdir, aw.N)
	case "review_error/retry":
		if aw == nil {
			return false, nil
		}
		return false, dropClose(bdir, aw.N)
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
	if c, _ := wave.ReadClose(bdir, aw.N); c != nil {
		tasks = c.ReviewErrors
	}
	err := bundle.AppendEvent(bdir, "review_skipped", map[string]any{
		keyWave: aw.N, keyAttempt: aw.Attempt, keyTasks: tasks, keyReason: reason,
	})
	if err != nil {
		return err
	}
	return dropClose(bdir, aw.N)
}
