package cli

import (
	"context"
	"errors"
	"maps"
	"os"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
)

// markVerified writes the record, sets verified_sha and records the event.
// Every path that declares HEAD verified goes through here.
func markVerified(_ context.Context, tgt *runTarget, rec finish.VerifyRecord, data map[string]any) error {
	rec.Passed = true
	if err := finish.WriteVerify(tgt.bdir, rec); err != nil {
		return err
	}
	sha := rec.SHA
	tgt.st.VerifiedSHA = &sha
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return err
	}
	ev := map[string]any{keySHA: sha, keyPassed: true}
	maps.Copy(ev, data)
	return bundle.AppendEvent(tgt.bdir, "verify", ev)
}

// answerVerification applies verification_failed: fix drops the record so
// the next call re-verifies; override verifies HEAD with a recorded reason;
// abort only ends the turn (the gate returns next call, like wave_failures/stop).
func answerVerification(ctx context.Context, tgt *runTarget, choice, reason string) (bool, error) {
	switch choice {
	case "fix":
		return false, dropVerify(tgt.bdir)
	case "override":
		if strings.TrimSpace(reason) == "" {
			return false, errors.New("override needs --reason")
		}
		rec, err := finish.ReadVerify(tgt.bdir)
		if err != nil {
			return false, err
		}
		if rec == nil {
			return false, errors.New("no verification record to override")
		}
		rec.Overridden = reason
		return false, markVerified(ctx, tgt, *rec, map[string]any{"overridden": reason})
	case "abort":
		return true, nil
	}
	return false, errorf("unknown choice %q for verification_failed", choice)
}

// answerNoVerification applies no_verification: specify stores a command
// and re-arms verify; proceed verifies HEAD with nothing run.
func answerNoVerification(ctx context.Context, tgt *runTarget, choice, reason string) (bool, error) {
	switch choice {
	case "specify":
		if err := finish.AppendExtra(tgt.bdir, reason); err != nil {
			return false, err
		}
		return false, dropVerify(tgt.bdir)
	case "proceed":
		head, err := tgt.ws.Repo.HeadSHA(ctx)
		if err != nil {
			return false, err
		}
		rec := finish.VerifyRecord{SHA: head, NoCommands: true, Skipped: true, At: timeNow()}
		return false, markVerified(ctx, tgt, rec, map[string]any{"skipped": true, keyNoCommands: true})
	}
	return false, errorf("unknown choice %q for no_verification", choice)
}

// markGoalsChecked writes the record, sets goals_checked_sha and records
// goal_check with the verdict counts, plus whatever else the caller wants on
// that event (healFinish marks its repair). Every path that declares HEAD's
// goals checked goes through here.
func markGoalsChecked(tgt *runTarget, rec finish.GoalsRecord, data map[string]any) error {
	if err := finish.WriteGoals(tgt.bdir, rec); err != nil {
		return err
	}
	sha := rec.SHA
	tgt.st.GoalsCheckedSHA = &sha
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, v := range rec.Verdicts {
		counts[v.Verdict]++
	}
	ev := map[string]any{
		keySHA: sha, "achieved": counts["achieved"], "partial": counts["partial"],
		"missed": counts["missed"], "waived": len(rec.Waived),
	}
	maps.Copy(ev, data)
	return bundle.AppendEvent(tgt.bdir, "goal_check", ev)
}

// answerGoalsUnmet applies goals_unmet: fix drops the record (re-assess
// after the user's commits); waive records every unmet goal with the
// reason and checks the goals at HEAD; abort only ends the turn.
func answerGoalsUnmet(tgt *runTarget, choice, reason string) (bool, error) {
	switch choice {
	case "fix":
		err := os.Remove(finish.GoalsPath(tgt.bdir))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	case "waive":
		return false, waiveGoals(tgt, reason)
	case "abort":
		return true, nil
	}
	return false, errorf("unknown choice %q for goals_unmet", choice)
}

// waiveGoals records the reason against every currently unmet goal — one
// goal_waived event each — and then checks the goals at the record's HEAD.
func waiveGoals(tgt *runTarget, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("waive needs --reason")
	}
	rec, err := finish.ReadGoals(tgt.bdir)
	if err != nil {
		return err
	}
	if rec == nil {
		return errors.New("no goal record to waive against")
	}
	if rec.Waived == nil {
		rec.Waived = map[string]string{}
	}
	for _, v := range rec.Unmet() {
		rec.Waived[v.ID] = reason
		_ = bundle.AppendEvent(tgt.bdir, "goal_waived", map[string]any{"goal": v.ID, keyReason: reason})
	}
	return markGoalsChecked(tgt, *rec, nil)
}

// dropVerify removes the record; absence is not an error.
func dropVerify(bdir string) error {
	err := os.Remove(finish.VerifyPath(bdir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// answerBranchFinish records the disposition after re-checking that it is
// available right now. The gate's payload is rendered once and re-rendered
// verbatim from it (spec §4.3), so what it says about merge and discard is
// as old as the question: the primary worktree may have moved or gone dirty
// since, and a disabled option the prompt still shows must not be able to
// start a merge takt cannot make. Only those two arms ask — the facts cost
// two git calls, and keep and pr have nothing to consult them about.
func answerBranchFinish(ctx context.Context, tgt *runTarget, choice, reason, confirm string) (bool, error) {
	switch choice {
	case dispositionMerge:
		df, err := gatherDispositionFacts(ctx, tgt.ws, tgt.st)
		if err != nil {
			return false, err
		}
		if !df.MergeAllowed {
			return false, errorf("merge is not available: %s", df.MergeBlocked)
		}
	case dispositionDiscard:
		df, err := gatherDispositionFacts(ctx, tgt.ws, tgt.st)
		if err != nil {
			return false, err
		}
		if !df.DiscardAllowed {
			// Only an adopted branch blocks discard, and that reason is
			// written once, on MergeBlocked (see gatherDispositionFacts).
			return false, errorf("discard is not available: %s", df.MergeBlocked)
		}
		if confirm != tgt.slug {
			return false, errorf("discard requires --confirm %s", tgt.slug)
		}
	case dispositionPR, dispositionKeep:
		// Nothing to check: keeping a branch and pushing one are available
		// on any run, in any worktree.
	default:
		return false, errorf("unknown choice %q for branch_finish", choice)
	}
	tgt.st.Disposition = &bundle.Disposition{Choice: choice, At: timeNow(), Reason: reason}
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return false, err
	}
	return false, bundle.AppendEvent(tgt.bdir, "disposition", map[string]any{keyChoice: choice, keyReason: reason})
}
