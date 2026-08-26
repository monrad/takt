package decide

import (
	"fmt"

	"github.com/monrad/takt/internal/op"
)

// Option choice/label text shared across gates — pulled into constants
// because goconst flags the repeated literals; the values are unchanged.
const (
	choiceStop  = "stop"
	choiceRetry = "retry"
	choiceFix   = "fix"
	choiceAbort = "abort"
	labelStop   = "Stop"
)

// Question builds the ask op for a gate id (spec §5.2). ctx carries the
// values the text needs (slug, gate, task ids…) and is echoed as Context so
// a re-rendered gate is identical to the first rendering.
func Question(gate string, ctx map[string]any) op.Op {
	slug, _ := ctx[ctxSlug].(string)
	q := op.Op{Op: op.Ask, Gate: gate, Context: ctx,
		Answer: fmt.Sprintf("takt answer --gate %s --choice <choice> --slug %s", gate, slug)}
	switch gate {
	case "owner":
		questionOwner(&q, ctx)
	case "gate_review":
		questionGateReview(&q, ctx)
	case "alignment_confirm":
		questionAlignmentConfirm(&q, ctx)
	case "plan_invalid":
		questionPlanInvalid(&q, ctx)
	case "wave_failures":
		questionWaveFailures(&q, ctx)
	case "review_error":
		questionReviewError(&q, ctx)
	case "verification_failed":
		questionVerificationFailed(&q, ctx)
	case "no_verification":
		questionNoVerification(&q, ctx)
	case "goals_unmet":
		questionGoalsUnmet(&q, ctx)
	case "branch_finish":
		questionBranchFinish(&q, ctx)
	default:
		questionDefault(&q, gate)
	}
	return q
}

// questionOwner fills the "owner" gate (another session holds the lock).
func questionOwner(q *op.Op, ctx map[string]any) {
	q.Narration = "another session holds this run"
	q.Question = fmt.Sprintf(
		"Session %v on %v is driving this run (heartbeat %v). How do you want to proceed?",
		ctx["holder"], ctx["host"], ctx["heartbeat"],
	)
	q.Options = []op.Option{
		{
			Choice:      "abort",
			Label:       "Abort (Recommended)",
			Description: "Leave the run to the other session; nothing is written.",
		},
		{
			Choice:      "takeover",
			Label:       "Take over (force)",
			Description: "Re-run `takt next --force`; the other session's next call will be blocked.",
		},
		{Choice: "readonly", Label: "Read-only", Description: "Inspect with `takt status`; no mutations."},
	}
}

// questionGateReview fills the "gate_review" gate (spec/plan review asked for rework).
func questionGateReview(q *op.Op, ctx map[string]any) {
	g, _ := ctx["gate"].(string)
	q.Narration = g + " review asked for rework"
	q.Question = fmt.Sprintf(
		"The %s review verdict is %v: %v. How do you want to proceed?",
		g, ctx["verdict"], ctx["summary"],
	)
	q.Options = []op.Option{
		{
			Choice: "revise",
			Label:  "Revise and re-review (Recommended)",
			Description: fmt.Sprintf(
				"Edit the %s with the findings in reviews/%s.md; the gate re-arms on the new hash.", g, g,
			),
		},
		{
			Choice:      "accept",
			Label:       "Accept as is",
			Description: "Record an override with a reason (`--reason`) and proceed.",
		},
		{Choice: choiceStop, Label: labelStop, Description: "Keep the gate open and end the turn."},
	}
}

// questionAlignmentConfirm fills the "alignment_confirm" gate.
func questionAlignmentConfirm(q *op.Op, ctx map[string]any) {
	q.Narration = "confirm the request's clauses"
	q.Question = fmt.Sprintf(
		"The auditor decomposed your original request into clauses A1..A%v (see alignment.json). Confirm or correct them.",
		ctx[ctxCount],
	)
	q.Options = []op.Option{
		{Choice: "confirm", Label: "Confirm (Recommended)", Description: "Use the clauses as written."},
		{
			Choice:      "edit",
			Label:       "Edit",
			Description: "Provide a corrected clause list with `--file <clauses.json>`.",
		},
		{
			Choice:      "skip",
			Label:       "Skip the audit",
			Description: "Proceed without the alignment digest (advisory only).",
		},
	}
}

// questionPlanInvalid fills the "plan_invalid" gate (planner failed 3 times).
func questionPlanInvalid(q *op.Op, ctx map[string]any) {
	q.Narration = "the planner produced an invalid index three times"
	q.Question = fmt.Sprintf(
		"plan.index.json is still invalid after %v attempts: %v",
		ctx["attempts"], ctx["problems"],
	)
	q.Options = []op.Option{
		{
			Choice:      choiceRetry,
			Label:       "Try once more (Recommended)",
			Description: "Re-dispatch the planner with the problems appended.",
		},
		{
			Choice:      choiceStop,
			Label:       labelStop,
			Description: "End the turn; fix the plan by hand and run `takt plan validate`.",
		},
	}
}

// questionWaveFailures fills the "wave_failures" gate.
func questionWaveFailures(q *op.Op, ctx map[string]any) {
	q.Narration = fmt.Sprintf("wave %v has failed or blocked tasks", ctx[ctxWave])
	q.Question = fmt.Sprintf(
		"Wave %v: failed %v, blocked %v, rework exhausted %v, rework pending %v. How do you want to proceed?",
		ctx[ctxWave], ctx[ctxFailed], ctx[ctxBlocked], ctx[ctxExhausted], ctx[ctxRework],
	)
	q.Options = []op.Option{
		{
			Choice:      choiceRetry,
			Label:       "Retry the failed tasks (Recommended)",
			Description: "Re-dispatch them with the failure context appended (model escalates one tier).",
		},
		{
			Choice:      "waive",
			Label:       "Waive selected tasks",
			Description: "`takt waive --task N --reason …` per task, then `takt next`.",
		},
		{Choice: choiceStop, Label: labelStop, Description: "End the turn with the wave open."},
	}
}

// questionReviewError fills the "review_error" gate.
func questionReviewError(q *op.Op, ctx map[string]any) {
	q.Narration = "a task review errored"
	q.Question = fmt.Sprintf("The reviewer failed for task(s) %v: %v", ctx["tasks"], ctx["error"])
	q.Options = []op.Option{
		{Choice: choiceRetry, Label: "Retry the review (Recommended)", Description: "Re-run `takt close-wave`."},
		{
			Choice:      "skip",
			Label:       "Skip review for these tasks",
			Description: "Record an evidenced skip (`--reason`) and accept the tasks.",
		},
		{Choice: choiceStop, Label: labelStop, Description: "End the turn with the wave open."},
	}
}

// questionVerificationFailed fills the "verification_failed" gate. It takes
// no values from ctx, but keeps the (q, ctx) signature shared by every
// filler dispatched from Question's switch.
func questionVerificationFailed(q *op.Op, _ map[string]any) {
	q.Narration = "verification failed at HEAD"
	q.Question = "Verification failed. How do you want to proceed?"
	q.Options = []op.Option{
		{Choice: choiceFix, Label: "Fix first and re-run (Recommended)",
			Description: "Fix the failure, commit, then `takt next` re-verifies at the new HEAD."},
		{Choice: "override", Label: "Proceed anyway (reviewed)",
			Description: "Record verified_sha = HEAD with your reason (`--reason`); the override is in the event log."},
		{
			Choice:      choiceAbort,
			Label:       "Abort finish",
			Description: "End the turn; the question returns on the next `takt next`.",
		},
	}
}

// questionNoVerification fills the "no_verification" gate. It takes no
// values from ctx, but keeps the (q, ctx) signature shared by every filler
// dispatched from Question's switch.
func questionNoVerification(q *op.Op, _ map[string]any) {
	q.Narration = "no verify commands to run"
	q.Question = "The plan declares no verify commands. How do you want to proceed?"
	q.Options = []op.Option{
		{
			Choice:      "specify",
			Label:       "Specify one (Recommended)",
			Description: "`takt answer --gate no_verification --choice specify --reason \"<command>\"`; it runs at HEAD next.",
		},
		{Choice: "proceed", Label: "Proceed without verification",
			Description: "Record verified_sha = HEAD with no commands run; the skip is in the event log."},
	}
}

// questionGoalsUnmet fills the "goals_unmet" gate.
func questionGoalsUnmet(q *op.Op, ctx map[string]any) {
	q.Narration = "goal check found unmet goals"
	q.Question = fmt.Sprintf("Unmet goals: %v. How do you want to proceed?", ctx["unmet"])
	q.Options = []op.Option{
		{Choice: choiceFix, Label: "Fix and continue (Recommended)",
			Description: "Address the goals, commit, then `takt next` re-verifies and re-assesses at the new HEAD."},
		{Choice: "waive", Label: "Waive the unmet goals",
			Description: "`--reason` required; one goal_waived event per goal, then goals_checked_sha = HEAD."},
		{
			Choice:      choiceAbort,
			Label:       "Abort finish",
			Description: "End the turn; the question returns on the next `takt next`.",
		},
	}
}

// questionBranchFinish fills the "branch_finish" gate.
func questionBranchFinish(q *op.Op, ctx map[string]any) {
	q.Narration = "choose what happens to the branch"
	q.Question = fmt.Sprintf("Run %v is verified on %v (base %v). What should happen to the branch?",
		ctx[ctxSlug], ctx["branch"], ctx["base"])
	pr := op.Option{
		Choice:      "pr",
		Label:       "Push and open a pull request",
		Description: "The session pushes the branch and runs `gh pr create --base <base> --fill`, then `takt done --step push_pr`.",
	}
	keep := op.Option{
		Choice:      "keep",
		Label:       "Keep the branch as-is",
		Description: "Archive the run; you integrate later.",
	}
	if adopted, _ := ctx["adopted"].(bool); adopted {
		q.Options = []op.Option{pr, keep}
		return
	}
	merge := op.Option{
		Choice:      "merge",
		Label:       "Merge into the base branch locally (Recommended)",
		Description: "`git merge --no-ff` in the primary worktree after the archive commit; the branch is deleted when nothing has it checked out.",
	}
	if allowed, _ := ctx["merge_allowed"].(bool); !allowed {
		merge.Disabled, _ = ctx["merge_blocked"].(string)
	}
	discard := op.Option{
		Choice:      "discard",
		Label:       "Discard the work",
		Description: "Requires `--confirm <slug>`. The bundle is copied to <dir>/.discarded/<slug>/ and the branch force-deleted.",
	}
	if allowed, _ := ctx["discard_allowed"].(bool); !allowed {
		discard.Disabled, _ = ctx["discard_blocked"].(string)
	}
	q.Options = []op.Option{merge, pr, keep, discard}
}

// questionDefault fills an unrecognised gate id with a generic continue/stop choice.
func questionDefault(q *op.Op, gate string) {
	q.Narration = "gate " + gate
	q.Question = "Resolve gate " + gate
	q.Options = []op.Option{
		{Choice: "continue", Label: "Continue", Description: ""},
		{Choice: choiceStop, Label: labelStop, Description: ""},
	}
}
