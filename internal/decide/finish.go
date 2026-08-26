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
		// A record with nothing unmet is the goals-side twin of a passed
		// verify record with no verified_sha: the assessment landed and the
		// state write did not (review I2). healFinish repairs that before
		// the loop runs, so reaching here means something else produced it
		// — and re-assessing HEAD is the answer that can only be right,
		// while "Unmet goals: []" is a question with no answer the user
		// could give, persisted as a gate.
		if !fin.Goals.Present || len(fin.Goals.Unmet) == 0 {
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
// nothing to run → ask. A record that *passed* is the one shape this row
// should never be looking at — decideFinish only comes here when
// verified_sha does not cover HEAD, and a pass is what sets it — so it means
// the record landed and the state write did not (review I2). healFinish
// repairs that before the loop runs; as defence in depth, verifying HEAD
// again is the answer that can only be right, while `verification_failed`
// with an empty failed list is the one that can only be wrong.
func decideVerify(st *bundle.State, v VerifyFacts) Decision {
	switch {
	case !v.Present, v.Passed:
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
