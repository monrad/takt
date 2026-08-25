package decide

import (
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/op"
)

// VerifyFacts is what finish/verify.json says about the current HEAD.
type VerifyFacts struct {
	Present    bool             // a record exists that still covers HEAD
	Passed     bool             // every command passed (or the record is overridden/skipped)
	NoCommands bool             // the union of verify commands was empty
	Failed     []map[string]any // {command, exit, tail} for each failed command
}

// GoalFacts is what finish/goals.json says about the current HEAD.
type GoalFacts struct {
	Present bool             // a record exists that still covers HEAD
	Unmet   []map[string]any // {id, verdict, evidence} for goals neither achieved nor waived
}

// FinishFacts feeds rows 20–26 (spec §5.3). The CLI decides what "covers
// HEAD" means (bundle-only commits do not move the goalposts).
type FinishFacts struct {
	Verified       bool // verified_sha covers HEAD
	Verify         VerifyFacts
	GoalsChecked   bool // goals_checked_sha covers HEAD
	Goals          GoalFacts
	HasRetro       bool
	Disposition    string // "" until branch_finish is answered
	PRPushed       bool
	MergeAllowed   bool
	MergeBlocked   string // reason when !MergeAllowed
	DiscardAllowed bool
	DiscardBlocked string
}

const verifyTimeoutS = 900

// decideFinish walks rows 20–26 in order; each step is a pure function of
// the records on disk, so a crash anywhere re-derives the same op.
func decideFinish(st *bundle.State, f Facts) Decision {
	fin := f.Finish
	if !fin.Verified {
		return decideVerify(st, fin.Verify)
	}
	if st.Config.Goals && !fin.GoalsChecked {
		if !fin.Goals.Present {
			return Decision{
				Action: ActDispatch,
				Agent:  &op.Agent{Agent: "goal-assessor", Label: "assess the goals at HEAD"},
			}
		}
		return ask("goals_unmet", map[string]any{ctxSlug: st.Slug, "unmet": fin.Goals.Unmet})
	}
	if !fin.HasRetro {
		return run("retro", "write the retrospective", map[string]any{ctxSlug: st.Slug})
	}
	if fin.Disposition == "" {
		return ask("branch_finish", branchFinishContext(st, fin))
	}
	if fin.Disposition == "pr" && !fin.PRPushed {
		return run("push_pr", "push the branch and open the pull request",
			map[string]any{ctxSlug: st.Slug, "branch": st.Branch, "base": st.Base})
	}
	return Decision{Action: ActArchive}
}

// decideVerify is row 20: no record → run it; a record that failed or found
// nothing to run → ask.
func decideVerify(st *bundle.State, v VerifyFacts) Decision {
	switch {
	case !v.Present:
		return exec("verifying at HEAD", "takt verify --slug "+st.Slug, verifyTimeoutS)
	case v.NoCommands:
		return ask("no_verification", map[string]any{ctxSlug: st.Slug})
	default:
		return ask("verification_failed", map[string]any{ctxSlug: st.Slug, ctxFailed: v.Failed})
	}
}

func branchFinishContext(st *bundle.State, fin FinishFacts) map[string]any {
	return map[string]any{
		ctxSlug: st.Slug, "branch": st.Branch, "base": st.Base, "adopted": st.BranchAdopted,
		"merge_allowed": fin.MergeAllowed, "merge_blocked": fin.MergeBlocked,
		"discard_allowed": fin.DiscardAllowed, "discard_blocked": fin.DiscardBlocked,
	}
}
