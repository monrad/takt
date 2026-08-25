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

// The disposition-outcome keys, shared by the stop op's context and the
// disposition_applied event.
const (
	keyMerged  = "merged"
	keyDeleted = "deleted"
)

// The `git branch` flags the two destructive dispositions delete with.
const (
	safeDelete  = "-d"
	forceDelete = "-D"
)

// archive is row 25: the run is closed on disk first — phase, lock — and
// committed, and only then does takt do the git work the disposition asks
// for. That order is what makes a merge carry the archived bundle, and it
// means a disposition that fails leaves a run that is properly archived
// rather than one stuck half-way. Whatever takt cannot do from this worktree
// comes back as cleanup commands for the session (spec §7.5 step 5; §4.7:
// takt never checks out another branch).
func (r *nextRun) archive(ctx context.Context) int {
	r.st.Phase = bundle.PhaseArchived
	r.st.Session = nil
	// `applied` means the disposition's git work has happened, never that
	// takt intends it to. keep and pr have no git work of their own, so
	// archiving them applies them, and the archive commit says so. merge and
	// discard are committed unapplied and marked only once their work has
	// really landed, so a crash in between leaves a run whose state says what
	// is still owed instead of one claiming a merge nobody made — and the
	// next `takt next` finishes the job (see applyAndStop).
	if r.st.Disposition != nil && !dispositionHasGitWork(r.st.Disposition.Choice) {
		r.st.Disposition.Applied = true
	}
	if err := bundle.SaveState(r.bdir, r.st); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(r.bdir, reasonArchived, map[string]any{keyChoice: dispositionChoice(r.st)})
	if _, _, err := commitBundle(ctx, r.ws, r.bdir, r.slug, "archive"); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	return applyAndStop(ctx, r.env, &runTarget{ws: r.ws, slug: r.slug, bdir: r.bdir, st: r.st})
}

// applyAndStop does the disposition's git work — for the first time on the
// heels of the archive commit, or over again for an archived run whose
// archive never got that far — records that it happened, and prints the
// run's stop op. Both entry points share it so a re-applied disposition is
// answered with the same op the archive itself would have printed.
func applyAndStop(ctx context.Context, env Env, tgt *runTarget) int {
	st := tgt.st
	cleanup, details, err := applyDisposition(ctx, tgt.ws, st, tgt.bdir)
	switch {
	case err != nil:
		// The run is archived either way; `applied` stays false, so the next
		// `takt next` picks the disposition back up, and cleanup names what
		// the session can do about it in the meantime.
		details["error"] = err.Error()
	case st.Disposition != nil && !st.Disposition.Applied:
		st.Disposition.Applied = true
		if serr := bundle.SaveState(tgt.bdir, st); serr != nil {
			return fail(env.Stderr, exitError, serr.Error(), "")
		}
		// Deliberately not committed. The archive commit is the run's last
		// one: for a merge it has already been carried into the base, and a
		// receipt committed now would sit on the run branch *after* the merge
		// — leaving the base behind the branch it just integrated, on a
		// branch that is being deleted. This worktree is being retired; the
		// record that matters is the event log and the archived bundle.
		_ = bundle.AppendEvent(tgt.bdir, "disposition_applied", appliedEvent(st, details))
	}
	return printOp(env, op.Op{
		Op: op.Stop, Reason: reasonArchived,
		Narration: fmt.Sprintf("run %s archived (%s)", tgt.slug, dispositionChoice(st)),
		Context:   details, Cleanup: cleanup,
	})
}

// appliedEvent records the choice and whatever git work it turned out to
// involve: the merge commit, and whether the run branch is gone.
func appliedEvent(st *bundle.State, details map[string]any) map[string]any {
	ev := map[string]any{keyChoice: dispositionChoice(st)}
	for _, k := range []string{keyMerged, keyDeleted} {
		if v, ok := details[k]; ok {
			ev[k] = v
		}
	}
	return ev
}

// dispositionChoice is what the run was archived as.
func dispositionChoice(st *bundle.State) string {
	if st.Disposition == nil {
		return "none"
	}
	return st.Disposition.Choice
}

// dispositionHasGitWork reports whether a choice asks takt to touch git at
// all: keep leaves the branch alone and pr was pushed by the session.
func dispositionHasGitWork(choice string) bool {
	return choice == dispositionMerge || choice == dispositionDiscard
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
		return applyMerge(ctx, ws, st, cleanup, details)
	case dispositionDiscard:
		if rel := bundleRel(ws, bdir); rel != "" {
			if err := copyBundle(bdir, ws.Dir.Discarded(st.Slug)); err != nil {
				return cleanup, details, err
			}
			details["discarded_copy"] = ws.Dir.Discarded(st.Slug)
		}
		var deleted bool
		cleanup, deleted = deleteOrHandOff(ctx, ws, st, cleanup, forceDelete)
		if deleted {
			details[keyDeleted] = true
		}
		return cleanup, details, nil
	}
	return cleanup, details, nil
}

// applyMerge merges the run branch into the base in the primary worktree.
// Everything it does is conditional on not having been done already: an
// archive that crashed after its merge must not make a second, empty merge
// commit when it is picked back up, and one that crashed after deleting the
// branch has nothing left to merge into anything.
func applyMerge(
	ctx context.Context, ws *workspace, st *bundle.State, cleanup []string, details map[string]any,
) ([]string, map[string]any, error) {
	exists, err := ws.Repo.BranchExists(ctx, st.Branch)
	if err != nil {
		return cleanup, details, err
	}
	if !exists {
		details[keyDeleted] = true
		return cleanup, details, nil
	}
	prim, err := ws.Repo.PrimaryWorktree(ctx)
	if err != nil {
		return cleanup, details, err
	}
	merged, err := ws.Repo.IsAncestor(ctx, st.Branch, prim.Head)
	if err != nil {
		return cleanup, details, err
	}
	sha := prim.Head
	if !merged {
		msg := fmt.Sprintf("Merge %s (takt run %s)", st.Branch, st.Slug)
		if sha, err = ws.Repo.MergeNoFF(ctx, prim.Path, st.Branch, msg); err != nil {
			return append(cleanup, fmt.Sprintf("git -C %s merge --no-ff %s", prim.Path, st.Branch)), details, err
		}
	}
	details[keyMerged] = sha
	var deleted bool
	cleanup, deleted = deleteOrHandOff(ctx, ws, st, cleanup, safeDelete)
	if deleted {
		details[keyDeleted] = true
	}
	return cleanup, details, nil
}

// deleteOrHandOff deletes the run branch when no worktree has it checked
// out; otherwise it appends the command the session must run instead. git
// refuses to delete a branch a worktree holds, and takt never checks out
// another branch or touches another worktree (spec §4.7), so this is the
// part of a disposition that can only ever be handed over. It reports
// whether the branch is gone once it returns — a branch that was already
// deleted (a re-applied disposition) counts, and is not asked for again.
func deleteOrHandOff(
	ctx context.Context, ws *workspace, st *bundle.State, cleanup []string, flag string,
) ([]string, bool) {
	if exists, err := ws.Repo.BranchExists(ctx, st.Branch); err == nil && !exists {
		return cleanup, true
	}
	held, checkedOut, err := ws.Repo.BranchCheckedOut(ctx, st.Branch)
	if err == nil && !checkedOut {
		if delErr := deleteBranch(ctx, ws, st.Branch, flag); delErr == nil {
			return cleanup, true
		}
	}
	return append(cleanup, handOff(ctx, ws, st, held, flag)), false
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
	// A slug can be reused, so a copy may already be there. It is replaced
	// wholesale rather than written over: a file the new run does not have
	// would otherwise survive from the old one and read as part of this run.
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
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
