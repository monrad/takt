package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/plan"
)

// digestPath is waves/<n>/task-<id>.a<attempt>.digest.json.
func digestPath(bdir string, wave, task, attempt int) string {
	return filepath.Join(bdir, "waves", strconv.Itoa(wave), fmt.Sprintf("task-%d.a%d.digest.json", task, attempt))
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

// answerWaveGate resolves the execute-phase gates; wired in Task 7.
func answerWaveGate(_ context.Context, _ *workspace, _ string, _ *bundle.State, _, _, _ string) (bool, error) {
	return false, errors.New("wave gates are wired in Task 7")
}
