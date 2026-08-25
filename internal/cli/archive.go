package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/op"
)

// The branch_finish choices (spec §7.5 step 4). dispositionPR lives in
// cmd_done.go, which is where push_pr reads it.
const (
	dispositionMerge   = "merge"
	dispositionKeep    = "keep"
	dispositionDiscard = "discard"
)

// reasonArchived is the stop reason (and the event) of a finished run.
const reasonArchived = "archived"

// The `git branch` flags the two destructive dispositions delete with.
const (
	safeDelete  = "-d"
	forceDelete = "-D"
)

// archive is row 25: the run is closed on disk first — phase, lock, the
// disposition marked applied — and committed, and only then does takt do the
// git work the disposition asks for. That order is what makes a merge carry
// the archived bundle, and it means a disposition that fails leaves a run
// that is properly archived rather than one stuck half-way. Whatever takt
// cannot do from this worktree comes back as cleanup commands for the
// session (spec §7.5 step 5; §4.7: takt never checks out another branch).
func (r *nextRun) archive(ctx context.Context) int {
	r.st.Phase = bundle.PhaseArchived
	r.st.Session = nil
	if r.st.Disposition != nil {
		r.st.Disposition.Applied = true
	}
	if err := bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, reasonArchived, map[string]any{keyChoice: dispositionChoice(r.st)})
	if _, _, err := commitBundle(ctx, r.ws, r.bdir, r.slug, "archive"); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	cleanup, details, err := applyDisposition(ctx, r.ws, r.st, r.bdir)
	if err != nil {
		// The run is archived; the disposition is now the user's to finish.
		details["error"] = err.Error()
	}
	return printOp(r.env, op.Op{
		Op: op.Stop, Reason: reasonArchived,
		Narration: fmt.Sprintf("run %s archived (%s)", r.slug, dispositionChoice(r.st)),
		Context:   details, Cleanup: cleanup,
	})
}

// dispositionChoice is what the run was archived as.
func dispositionChoice(st *bundle.State) string {
	if st.Disposition == nil {
		return "none"
	}
	return st.Disposition.Choice
}

// applyDisposition does the git side of merge and discard and reports what
// is left for the session. pr and keep ask for nothing: the pull request is
// already open, and keeping the branch is the absence of an action.
func applyDisposition(
	ctx context.Context, ws *workspace, st *bundle.State, bdir string,
) ([]string, map[string]any, error) {
	cleanup := []string{}
	details := map[string]any{}
	if st.Disposition == nil {
		return cleanup, details, nil
	}
	switch st.Disposition.Choice {
	case dispositionMerge:
		prim, err := ws.Repo.PrimaryWorktree(ctx)
		if err != nil {
			return cleanup, details, err
		}
		msg := fmt.Sprintf("Merge %s (takt run %s)", st.Branch, st.Slug)
		sha, err := ws.Repo.MergeNoFF(ctx, prim.Path, st.Branch, msg)
		if err != nil {
			return append(cleanup, fmt.Sprintf("git -C %s merge --no-ff %s", prim.Path, st.Branch)), details, err
		}
		details["merged"] = sha
		return deleteOrHandOff(ctx, ws, st, cleanup, safeDelete), details, nil
	case dispositionDiscard:
		if rel := bundleRel(ws, bdir); rel != "" {
			if err := copyBundle(bdir, ws.Dir.Discarded(st.Slug)); err != nil {
				return cleanup, details, err
			}
			details["discarded_copy"] = ws.Dir.Discarded(st.Slug)
		}
		return deleteOrHandOff(ctx, ws, st, cleanup, forceDelete), details, nil
	}
	return cleanup, details, nil
}

// deleteOrHandOff deletes the run branch when no worktree has it checked
// out; otherwise it appends the command the session must run instead. git
// refuses to delete a branch a worktree holds, and takt never checks out
// another branch or touches another worktree (spec §4.7), so this is the
// part of a disposition that can only ever be handed over.
func deleteOrHandOff(
	ctx context.Context, ws *workspace, st *bundle.State, cleanup []string, flag string,
) []string {
	held, checkedOut, err := ws.Repo.BranchCheckedOut(ctx, st.Branch)
	if err == nil && !checkedOut {
		if delErr := deleteBranch(ctx, ws, st.Branch, flag); delErr == nil {
			return cleanup
		}
	}
	return append(cleanup, handOff(ctx, ws, st, held, flag))
}

// deleteBranch is `git branch -d` or `git branch -D`, per flag.
func deleteBranch(ctx context.Context, ws *workspace, branch, flag string) error {
	if flag == forceDelete {
		return ws.Repo.DeleteBranchForce(ctx, branch)
	}
	return ws.Repo.DeleteBranch(ctx, branch)
}

// handOff is the deletion command for the session. When this very worktree
// is the one holding the branch and nothing else has the base checked out,
// letting go of it is one checkout away and the whole hand-off is a line the
// session can run where it stands. When the base is held elsewhere — the
// merge case, where the primary worktree is sitting on it — that checkout
// would fail, so the command is the bare deletion: it runs from another
// worktree, once this one has let the branch go.
func handOff(ctx context.Context, ws *workspace, st *bundle.State, held, flag string) string {
	del := "git branch " + flag + " " + st.Branch
	if filepath.Clean(held) != filepath.Clean(ws.Repo.Root) {
		return del
	}
	if _, taken, err := ws.Repo.BranchCheckedOut(ctx, st.Base); err != nil || taken {
		return del
	}
	return "git checkout " + st.Base + " && " + del
}

// copyBundle copies the bundle tree to dst and drops a .gitignore beside it
// so the copy never enters a commit. Discarding deletes the branch the
// bundle's own commits live on, so this untracked copy is all that is left
// of the run — which is exactly why it must not be committed to the base.
func copyBundle(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dst), ".gitignore"), []byte("*\n"), 0o600); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		//nolint:gosec // G304: p is produced by WalkDir over the run's own bundle directory
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		//nolint:gosec // G703: target is dst joined with a path WalkDir produced under src, so the relative
		// part can never begin with "..", and dst comes from a slug bundle.ValidSlug already accepted
		// (see selectSlug) — no caller can steer this write out of the bundle root.
		return os.WriteFile(target, b, 0o600)
	})
}
