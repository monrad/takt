package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gitx"
)

// bundleRel is this run's bundle directory relative to the repo root, or
// "" for an external bundle (nothing takt writes there is in git). The
// InRepo guard is what makes "external" mean the same thing here as in
// bundleTreeRel: an absolute --dir that happens to land under the work tree
// is still a bundle takt never commits, so it must not be excluded from a
// commit-to-commit diff as though takt had written it.
func bundleRel(ws *workspace, bdir string) string {
	if !ws.Dir.InRepo {
		return ""
	}
	rel, err := ws.Dir.RelToRepo(bdir)
	if err != nil {
		return ""
	}
	return rel
}

// headCovered says whether sha still stands for HEAD: it is HEAD, or an
// ancestor of HEAD that differs from it only inside the bundle directory
// (takt's own answer/done commits must not re-arm verify — plan 3 decision 3).
// It reads HEAD itself, for the callers that only ask this one question.
func headCovered(ctx context.Context, ws *workspace, bdir, sha string) (bool, error) {
	head, err := ws.Repo.HeadSHA(ctx)
	if err != nil {
		return false, err
	}
	return headCoveredAt(ctx, ws, bdir, head, sha)
}

// headCoveredAt is headCovered against a HEAD the caller already read. One
// `takt next` asks the question up to four times (verified_sha, the verify
// record, goals_checked_sha, the goal record) and the answer must be
// against one HEAD, not four reads of it — so gatherFinishFacts resolves
// HEAD once and threads it through here (task-3 review).
func headCoveredAt(ctx context.Context, ws *workspace, bdir, head, sha string) (bool, error) {
	if sha == head {
		return true, nil
	}
	rel := bundleRel(ws, bdir)
	if rel == "" {
		return false, nil
	}
	anc, err := ws.Repo.IsAncestor(ctx, sha, head)
	if err != nil || !anc {
		return false, err
	}
	return ws.Repo.DiffQuietExcluding(ctx, sha, head, rel)
}

// gatherFinishFacts fills rows 20–26's inputs.
func gatherFinishFacts(ctx context.Context, ws *workspace, bdir string, st *bundle.State) (decide.FinishFacts, error) {
	var fin decide.FinishFacts
	head, err := ws.Repo.HeadSHA(ctx)
	if err != nil {
		return fin, err
	}
	if st.VerifiedSHA != nil {
		if fin.Verified, err = headCoveredAt(ctx, ws, bdir, head, *st.VerifiedSHA); err != nil {
			return fin, err
		}
	}
	if fin.Verify, err = verifyFacts(ctx, ws, bdir, head); err != nil {
		return fin, err
	}
	if st.GoalsCheckedSHA != nil {
		if fin.GoalsChecked, err = headCoveredAt(ctx, ws, bdir, head, *st.GoalsCheckedSHA); err != nil {
			return fin, err
		}
	}
	if fin.Goals, err = goalFacts(ctx, ws, bdir, head); err != nil {
		return fin, err
	}
	fin.HasRetro = fileNonEmpty(filepath.Join(bdir, "retro.md"))
	if st.Disposition != nil {
		fin.Disposition = st.Disposition.Choice
		fin.PRPushed = st.Disposition.PRURL != ""
		return fin, nil
	}
	// Row 23 is the only consumer of the availability facts, and answering
	// them costs three git calls, so they are gathered only when that row is
	// the one about to be reached.
	if !fin.HasRetro {
		return fin, nil
	}
	df, err := gatherDispositionFacts(ctx, ws, st)
	if err != nil {
		return fin, err
	}
	fin.MergeAllowed, fin.MergeBlocked = df.MergeAllowed, df.MergeBlocked
	fin.DiscardAllowed = df.DiscardAllowed
	return fin, nil
}

// dispositionFacts is what branch_finish may offer (spec §7.5 step 4).
// An unavailable option needs its reason — the question renders the blocked
// string verbatim under the greyed-out option, so an empty one is a disabled
// choice with no explanation — but there is one reason field, not one per
// option. Merge is the only choice a run can lose on its own (a primary
// worktree that has moved or gone dirty), and the single thing that blocks
// discard, an adopted branch, blocks merge with the very same sentence. A
// second copy of it could never be rendered anyway: an adopted run is
// offered pr and keep alone (review M9).
type dispositionFacts struct {
	MergeAllowed   bool
	MergeBlocked   string
	DiscardAllowed bool
	Primary        gitx.Worktree
}

// gatherDispositionFacts decides what takt can do itself: merge needs the
// primary worktree on the base branch and clean; discard needs a branch takt
// created. takt never checks out another branch (spec §4.7), so anything
// else is handed to the session as cleanup commands.
func gatherDispositionFacts(ctx context.Context, ws *workspace, st *bundle.State) (dispositionFacts, error) {
	var f dispositionFacts
	if st.BranchAdopted {
		// takt created neither the branch nor its history, so neither
		// destructive choice is takt's to take, and both report this.
		f.MergeBlocked = "the run adopted branch " + st.Branch + "; integrate it yourself"
		return f, nil
	}
	f.DiscardAllowed = true
	prim, err := ws.Repo.PrimaryWorktree(ctx)
	if err != nil {
		return f, err
	}
	f.Primary = prim
	if prim.Branch != st.Base {
		f.MergeBlocked = fmt.Sprintf("primary worktree %s is on %s, not %s; merge by hand after archiving",
			prim.Path, prim.Branch, st.Base)
		return f, nil
	}
	clean, err := ws.Repo.IsCleanIn(ctx, prim.Path)
	if err != nil {
		return f, err
	}
	if !clean {
		f.MergeBlocked = "primary worktree " + prim.Path + " has uncommitted changes"
		return f, nil
	}
	f.MergeAllowed = true
	return f, nil
}

// verifyFacts reads finish/verify.json and reports it only while it still
// covers HEAD; a record left behind by an earlier commit is no record at all.
func verifyFacts(ctx context.Context, ws *workspace, bdir, head string) (decide.VerifyFacts, error) {
	rec, err := finish.ReadVerify(bdir)
	if err != nil || rec == nil {
		return decide.VerifyFacts{}, err
	}
	covered, err := headCoveredAt(ctx, ws, bdir, head, rec.SHA)
	if err != nil || !covered {
		return decide.VerifyFacts{}, err
	}
	return decide.VerifyFacts{
		Present: true, Passed: rec.Passed, NoCommands: rec.NoCommands, Failed: failedList(rec.Results),
	}, nil
}

// goalFacts reads finish/goals.json and reports it only while it still
// covers HEAD, exactly as verifyFacts does for the verification record.
func goalFacts(ctx context.Context, ws *workspace, bdir, head string) (decide.GoalFacts, error) {
	rec, err := finish.ReadGoals(bdir)
	if err != nil || rec == nil {
		return decide.GoalFacts{}, err
	}
	covered, err := headCoveredAt(ctx, ws, bdir, head, rec.SHA)
	if err != nil || !covered {
		return decide.GoalFacts{}, err
	}
	return decide.GoalFacts{Present: true, Unmet: unmetList(rec.Unmet())}, nil
}
