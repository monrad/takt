package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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

// The disposition-outcome keys of the stop op's context. They report what
// git looks like right now, re-read on every call — nothing about the git
// side of a disposition is remembered in state.
const (
	keyMerged  = "merged"
	keyDeleted = "deleted"
)

// The `git branch` flags the two destructive dispositions delete with. They
// name the git flag and nothing more: -d is git's own "already merged"
// check, -D is unconditional. Neither says anything about what takt checked
// before choosing one.
const (
	deleteMerged = "-d"
	deleteForce  = "-D"
)

// archive is row 25: the run's bookkeeping is finished and committed, and
// only then does takt do the git work the disposition asks for. That order
// is what makes a merge carry the archived bundle — the base branch can only
// ever receive this state.json through the merge itself, so `applied: true`
// is true by construction wherever the base can read it (spec §7.5 step 5;
// §4.7: takt never checks out another branch).
//
// `applied` means takt's own bookkeeping for the disposition is done, and is
// set for every choice before the commit. It is deliberately not a record of
// the git effects: whether the merge landed and whether the branch is gone
// are re-read from git on every call (see applyDisposition), because a state
// field claiming either would be a second source of truth that git can
// contradict at any moment.
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
	// The discard copy is bookkeeping too, and is taken here — from the
	// archived bundle, once — rather than by applyDisposition, which runs
	// again on every later call.
	if dispositionChoice(r.st) == dispositionDiscard {
		if err := discardCopy(r.ws, r.st, r.bdir); err != nil {
			return fail(r.env.Stderr, exitError, err.Error(), "")
		}
	}
	if _, _, err := commitBundle(ctx, r.ws, r.bdir, r.slug, "archive"); err != nil {
		return fail(r.env.Stderr, exitError, err.Error(), "")
	}
	return applyAndStop(ctx, r.env, &runTarget{ws: r.ws, slug: r.slug, bdir: r.bdir, st: r.st})
}

// applyAndStop does the disposition's git work and prints the run's stop op.
// It writes nothing: the archive commit is the run's last one, so the tree is
// clean for every choice by the time this runs — which is what makes the
// discard hand-off (`git checkout <base> && git branch -D <branch>`) a
// command the session can actually run. Row 25 and every later `takt next` on
// the archived run share it, so an effect that could not be applied the first
// time is simply applied the next.
func applyAndStop(ctx context.Context, env Env, tgt *runTarget) int {
	cleanup, details, err := applyDisposition(ctx, tgt.ws, tgt.st, tgt.bdir)
	if err != nil {
		// The run is archived either way. cleanup names what the session can
		// do about it now, and the next `takt next` tries again.
		details["error"] = err.Error()
	}
	return printOp(env, op.Op{
		Op: op.Stop, Reason: reasonArchived,
		Narration: fmt.Sprintf("run %s archived (%s)", tgt.slug, dispositionChoice(tgt.st)),
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

// discardCopy saves the bundle beside the live ones before the branch that
// holds its commits is deleted. An external bundle needs no copy: it is not
// on the branch in the first place.
func discardCopy(ws *workspace, st *bundle.State, bdir string) error {
	if bundleRel(ws, bdir) == "" {
		return nil
	}
	return copyBundle(bdir, ws.Dir.Discarded(st.Slug))
}

// applyDisposition does the git side of merge and discard and reports what
// is left for the session. Every question it asks is put to git, never to
// state, so calling it on an already-finished disposition is a no-op rather
// than a repeat. pr and keep ask for nothing: the pull request is already
// open, and keeping the branch is the absence of an action.
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
		if dir := ws.Dir.Discarded(st.Slug); dirExists(dir) {
			details["discarded_copy"] = dir
		}
		return deleteOrHandOff(ctx, ws, st, cleanup, details, deleteForce, discardSweep(ws, bdir))
	}
	return cleanup, details, nil
}

// applyMerge brings the run branch into the base in the primary worktree.
// Both halves are conditional on git, not on a flag: a branch the base
// already contains is not merged again, and one that is gone was merged and
// deleted by an earlier call.
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
	// Against the base *ref*, not the primary's HEAD: the primary may have
	// moved on to another branch since, and the question is whether the base
	// has the work, not what that worktree happens to be showing.
	merged, err := ws.Repo.IsAncestor(ctx, st.Branch, st.Base)
	if err != nil {
		return cleanup, details, err
	}
	if !merged {
		if cleanup, err = mergeIntoPrimary(ctx, ws, st, cleanup); err != nil {
			return cleanup, details, err
		}
	}
	if sha, serr := ws.Repo.Run(ctx, "rev-parse", st.Base); serr == nil {
		details[keyMerged] = sha
	}
	return deleteOrHandOff(ctx, ws, st, cleanup, details, deleteMerged, "")
}

// mergeIntoPrimary re-checks that the merge is still takt's to make before
// making it. The availability was judged when the question was asked, and
// the primary worktree may have changed branch or gone dirty since; merging
// anyway would put the run's work on whatever branch happened to be checked
// out there. A merge that cannot be made, or one that conflicts, is handed
// to the session as the exact command — and a conflict is aborted first, so
// the primary is never left mid-merge for someone else to find.
func mergeIntoPrimary(
	ctx context.Context, ws *workspace, st *bundle.State, cleanup []string,
) ([]string, error) {
	df, err := gatherDispositionFacts(ctx, ws, st)
	if err != nil {
		return cleanup, err
	}
	prim := df.Primary.Path
	if prim == "" {
		prim = ws.Repo.Root // unreachable for merge (adopted runs cannot choose it), but never print `git -C `
	}
	byHand := fmt.Sprintf("git -C %s merge --no-ff %s", shellQuote(prim), st.Branch)
	if !df.MergeAllowed {
		return append(cleanup, byHand), errors.New(df.MergeBlocked)
	}
	msg := fmt.Sprintf("Merge %s (takt run %s)", st.Branch, st.Slug)
	if _, err = ws.Repo.MergeNoFF(ctx, prim, st.Branch, msg); err != nil {
		_ = ws.Repo.MergeAbort(ctx, prim)
		return append(cleanup, byHand), err
	}
	return cleanup, nil
}

// deleteOrHandOff deletes the run branch when git will let takt do it, and
// otherwise appends the command that will. git refuses to delete a branch a
// worktree holds, and takt never checks out another branch or touches
// another worktree (spec §4.7), so this is the part of a disposition that
// can only ever be handed over.
func deleteOrHandOff(
	ctx context.Context, ws *workspace, st *bundle.State,
	cleanup []string, details map[string]any, flag, sweep string,
) ([]string, map[string]any, error) {
	exists, err := ws.Repo.BranchExists(ctx, st.Branch)
	if err != nil {
		return cleanup, details, err
	}
	if !exists {
		details[keyDeleted] = true
		return cleanup, details, nil
	}
	held, checkedOut, err := ws.Repo.BranchCheckedOut(ctx, st.Branch)
	if err != nil {
		return cleanup, details, err
	}
	if !checkedOut && deleteBranch(ctx, ws, st.Branch, flag) == nil {
		details[keyDeleted] = true
		return cleanup, details, nil
	}
	return append(cleanup, handOff(ctx, ws, st, held, flag, sweep)), details, nil
}

// deleteBranch is `git branch -d` or `git branch -D`, per flag. merge uses
// -d so git checks the work really is in the base before the branch goes;
// discard uses -D because unmerged is the whole point.
func deleteBranch(ctx context.Context, ws *workspace, branch, flag string) error {
	if flag == deleteForce {
		return ws.Repo.DeleteBranchForce(ctx, branch)
	}
	return ws.Repo.DeleteBranchSafe(ctx, branch)
}

// handOff is the deletion command for the session. When this very worktree
// is the one holding the branch and nothing else has the base checked out,
// letting go of it is one checkout away and the whole hand-off is a line the
// session can run where it stands. When the base is held elsewhere — the
// merge case, where the primary worktree is sitting on it — that checkout
// would fail, so the command is the bare deletion: it runs from another
// worktree, once this one has let the branch go.
func handOff(ctx context.Context, ws *workspace, st *bundle.State, held, flag, sweep string) string {
	del := "git branch " + flag + " " + st.Branch
	if filepath.Clean(held) != filepath.Clean(ws.Repo.Root) {
		return del
	}
	if _, taken, err := ws.Repo.BranchCheckedOut(ctx, st.Base); err != nil || taken {
		return del
	}
	cmd := "git checkout " + st.Base + " && " + del
	if sweep != "" {
		cmd += " && " + sweep
	}
	return cmd
}

// discardSweep is what the discard hand-off adds once this worktree has left
// the run branch: the checkout takes the bundle's tracked files with it, but
// the reviewer logs under it are ignored — by a .gitignore that is itself
// tracked, and therefore gone too — so they stay behind as untracked litter
// from a branch that no longer exists. Everything in them is already in the
// .discarded copy. Scoped to the run's own bundle by pathspec; empty for an
// external bundle, which the checkout never touches in the first place.
//
// -fd, not -fdx: with the run branch's .gitignore gone, the litter this
// exists for is plain untracked. All -x would add is the power to delete
// whatever the base branch ignores under that path — files this run never
// made and has no business removing.
func discardSweep(ws *workspace, bdir string) string {
	rel := bundleRel(ws, bdir)
	if rel == "" {
		return ""
	}
	return "git clean -fd -- " + shellQuote(rel)
}

// shellQuote wraps a path for the cleanup commands takt prints. They are
// handed to a session that runs them through a shell, verbatim, and a
// worktree path is whatever the user's filesystem says — a space in it would
// otherwise split the command into arguments git cannot use. Single quotes
// are literal in POSIX shells, so only an embedded single quote needs the
// close-escape-reopen dance; nothing inside them is expanded either, which
// double quotes would not give.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
