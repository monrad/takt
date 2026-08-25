package cli

import (
	"context"
	"path/filepath"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/finish"
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
func headCovered(ctx context.Context, ws *workspace, bdir, sha string) (bool, error) {
	head, err := ws.Repo.HeadSHA(ctx)
	if err != nil {
		return false, err
	}
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

// gatherFinishFacts fills rows 20–26's inputs. Disposition availability
// (merge/discard) is computed in archive.go (Task 6) and merged here.
func gatherFinishFacts(ctx context.Context, ws *workspace, bdir string, st *bundle.State) (decide.FinishFacts, error) {
	var fin decide.FinishFacts
	var err error
	if st.VerifiedSHA != nil {
		if fin.Verified, err = headCovered(ctx, ws, bdir, *st.VerifiedSHA); err != nil {
			return fin, err
		}
	}
	if fin.Verify, err = verifyFacts(ctx, ws, bdir); err != nil {
		return fin, err
	}
	if st.GoalsCheckedSHA != nil {
		if fin.GoalsChecked, err = headCovered(ctx, ws, bdir, *st.GoalsCheckedSHA); err != nil {
			return fin, err
		}
	}
	if fin.Goals, err = goalFacts(ctx, ws, bdir); err != nil {
		return fin, err
	}
	fin.HasRetro = fileNonEmpty(filepath.Join(bdir, "retro.md"))
	if st.Disposition != nil {
		fin.Disposition = st.Disposition.Choice
		fin.PRPushed = st.Disposition.PRURL != ""
	}
	return fin, nil
}

// verifyFacts reads finish/verify.json and reports it only while it still
// covers HEAD; a record left behind by an earlier commit is no record at all.
func verifyFacts(ctx context.Context, ws *workspace, bdir string) (decide.VerifyFacts, error) {
	rec, err := finish.ReadVerify(bdir)
	if err != nil || rec == nil {
		return decide.VerifyFacts{}, err
	}
	covered, err := headCovered(ctx, ws, bdir, rec.SHA)
	if err != nil || !covered {
		return decide.VerifyFacts{}, err
	}
	return decide.VerifyFacts{
		Present: true, Passed: rec.Passed, NoCommands: rec.NoCommands, Failed: failedList(rec.Results),
	}, nil
}

// goalFacts reads finish/goals.json and reports it only while it still
// covers HEAD, exactly as verifyFacts does for the verification record.
func goalFacts(ctx context.Context, ws *workspace, bdir string) (decide.GoalFacts, error) {
	rec, err := finish.ReadGoals(bdir)
	if err != nil || rec == nil {
		return decide.GoalFacts{}, err
	}
	covered, err := headCovered(ctx, ws, bdir, rec.SHA)
	if err != nil || !covered {
		return decide.GoalFacts{}, err
	}
	return decide.GoalFacts{Present: true, Unmet: unmetList(rec.Unmet())}, nil
}
