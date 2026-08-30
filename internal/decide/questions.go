package decide

import (
	"fmt"
	"strings"

	"github.com/monrad/takt/internal/op"
)

// Option choice/label text shared across gates — pulled into constants
// because goconst flags the repeated literals; the values are unchanged.
const (
	choiceStop  = "stop"
	choiceRetry = "retry"
	choiceSkip  = "skip"
	choiceFix   = "fix"
	choiceAbort = "abort"
	labelStop   = "Stop"
	// suffixRecommended marks the one option a question recommends. It is
	// appended rather than spelled into a label where which option carries
	// it depends on the facts — branch_finish recommends the merge it can
	// perform and the pull request when it cannot (#26).
	suffixRecommended = " (Recommended)"
)

// Gate ids Question switches on (spec §5.2). Vocab returns these same
// constants, so the twelve ids in the switch below and the twelve ids the
// prompt parity test reads can never drift apart without a compile error.
const (
	gateOwner              = "owner"
	gateReview             = "gate_review"
	gateReviewCapped       = "gate_review_capped"
	gateAlignmentConfirm   = "alignment_confirm"
	gatePlanInvalid        = "plan_invalid"
	gateAgentInvalid       = "agent_invalid"
	gateWaveFailures       = "wave_failures"
	gateReviewError        = "review_error"
	gateVerificationFailed = "verification_failed"
	gateNoVerification     = "no_verification"
	gateGoalsUnmet         = "goals_unmet"
	gateBranchFinish       = "branch_finish"
)

// Question builds the ask op for a gate id (spec §5.2). ctx carries the
// values the text needs (slug, gate, task ids…) and is echoed as Context so
// a re-rendered gate is identical to the first rendering.
func Question(gate string, ctx map[string]any) op.Op {
	slug, _ := ctx[ctxSlug].(string)
	q := op.Op{Op: op.Ask, Gate: gate, Context: ctx,
		Answer: fmt.Sprintf("takt answer --gate %s --choice <choice> --slug %s", gate, slug)}
	switch gate {
	case gateOwner:
		questionOwner(&q, ctx)
	case gateReview:
		questionGateReview(&q, ctx)
	case gateReviewCapped:
		questionGateReviewCapped(&q, ctx)
	case gateAlignmentConfirm:
		questionAlignmentConfirm(&q, ctx)
	case gatePlanInvalid:
		questionPlanInvalid(&q, ctx)
	case gateAgentInvalid:
		questionAgentInvalid(&q, ctx)
	case gateWaveFailures:
		questionWaveFailures(&q, ctx)
	case gateReviewError:
		questionReviewError(&q, ctx)
	case gateVerificationFailed:
		questionVerificationFailed(&q, ctx)
	case gateNoVerification:
		questionNoVerification(&q, ctx)
	case gateGoalsUnmet:
		questionGoalsUnmet(&q, ctx)
	case gateBranchFinish:
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

// questionGateReview fills the "gate_review" gate: a review that did not
// close its gate. An error verdict takes its own row — see
// questionGateReviewErrored — because nothing was reviewed there.
//
// On the rows a reviewer really answered, the revise option's text depends on
// what revising will actually do: on the spec gate, a rework verdict that
// found nothing blocking is "usable after the listed edits" and the edit
// itself closes the gate (fixed-point design §4), so promising a re-review
// there would tell the user the opposite of what happens.
//
// The verdict is half of that condition, not just the severity: acceptRevision
// writes the closing event only for a non-blocking rework, so the remaining
// rows of the design's §3 table — blocking rework and reject — keep the
// re-arm-and-re-review loop and have to keep being told so.
func questionGateReview(q *op.Op, ctx map[string]any) {
	g, _ := ctx["gate"].(string)
	verdict, _ := ctx["verdict"].(string)
	blocking, _ := ctx["blocking"].(bool)
	if verdict == verdictError {
		slug, _ := ctx[ctxSlug].(string)
		reason, _ := ctx["reason"].(string)
		questionGateReviewErrored(q, g, slug, reason)
		return
	}
	q.Narration = g + " review asked for rework"
	q.Question = fmt.Sprintf(
		"The %s review verdict is %v: %v. How do you want to proceed?",
		g, ctx["verdict"], ctx["summary"],
	)
	revise := op.Option{
		Choice: "revise",
		Label:  "Revise and re-review (Recommended)",
		Description: fmt.Sprintf(
			"Edit the %s with the findings in reviews/%s.md; the gate re-arms on the new hash.", g, g,
		),
	}
	if g == specGate && verdict == verdictRework && !blocking {
		revise.Label = "Revise (Recommended)"
		revise.Description = "Edit spec.md with the findings in reviews/spec.md; " +
			"the gate closes on the edit — no second review."
	}
	q.Options = append([]op.Option{revise}, gateReviewAcceptAndStop()...)
}

// questionGateReviewErrored fills the "gate_review" gate for an error
// verdict: the backend failed, so no review was taken. reviews/<gate>.md was
// left alone and still describes the previous pass, which is why revise is
// not offered — there are no findings to act on and an edit would only
// re-arm the gate for the pass that never ran. retry names the one action
// that can produce a verdict; it writes nothing, so the same question comes
// back if the review is not re-run.
func questionGateReviewErrored(q *op.Op, g, slug, reason string) {
	if reason == "" {
		reason = "(no reason recorded)"
	}
	q.Narration = g + " review errored"
	q.Question = fmt.Sprintf(
		"The %s review errored: %s. reviews/%s.md still describes the previous pass. "+
			"How do you want to proceed?",
		g, reason, g,
	)
	q.Options = append([]op.Option{{
		Choice: choiceRetry,
		Label:  "Re-run the review (Recommended)",
		Description: fmt.Sprintf(
			"Re-run the reviewer: `takt review %s --slug %s`, then `takt next`.", g, slug,
		),
	}}, gateReviewAcceptAndStop()...)
}

// gateReviewAcceptAndStop returns the two options every gate_review row ends
// with — record an evidenced override, or leave the gate open — spelled once
// because the error row and the reviewer-answered rows share them verbatim.
func gateReviewAcceptAndStop() []op.Option {
	return []op.Option{
		{
			Choice:      "accept",
			Label:       "Accept as is",
			Description: "Record an override with a reason (`--reason`); the findings are carried to the retro.",
		},
		{Choice: choiceStop, Label: labelStop, Description: "Keep the gate open and end the turn."},
	}
}

// questionGateReviewCapped fills the "gate_review_capped" gate: a spec or plan
// review has taken maxAgentAttempts passes without the gate closing
// (fixed-point design §8). Gate review is the one loop that cannot
// self-limit, so this is where it stops and asks.
func questionGateReviewCapped(q *op.Op, ctx map[string]any) {
	g, _ := ctx["gate"].(string)
	q.Narration = fmt.Sprintf("the %s review has taken %v passes", g, ctx[ctxAttempts])
	q.Question = fmt.Sprintf(
		"The %s review has run %v times without closing the gate (findings in reviews/%s.md). "+
			"How do you want to proceed?",
		g, ctx[ctxAttempts], g,
	)
	q.Options = []op.Option{
		{
			Choice:      "accept",
			Label:       "Accept as is (Recommended)",
			Description: "Record an override with a reason (`--reason`); the findings are carried to the retro.",
		},
		{
			Choice:      choiceRetry,
			Label:       "One more pass",
			Description: "Reset the round count and review once more.",
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
			Choice:      choiceSkip,
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
		ctx[ctxAttempts], ctx[ctxProblems],
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

// questionAgentInvalid fills the "agent_invalid" gate: an agent whose reply
// takt could not parse three times running (spec §5.3 rows 10, 11, 21).
// Skipping is an answer only for the alignment auditor and the reviewer,
// whose digest and internal review are both advisory (two-layers design
// §4.2); the goal check has no skip — a run that must not check its goals
// is initialised with --no-goals.
func questionAgentInvalid(q *op.Op, ctx map[string]any) {
	agent, _ := ctx[ctxAgent].(string)
	q.Narration = fmt.Sprintf("the %s replied unusably %v times", agent, ctx[ctxAttempts])
	q.Question = fmt.Sprintf(
		"takt could not parse the %s's reply after %v attempts: %v",
		agent, ctx[ctxAttempts], ctx[ctxProblems],
	)
	q.Options = []op.Option{
		{
			Choice:      choiceRetry,
			Label:       "Try once more (Recommended)",
			Description: "Re-dispatch the " + agent + " with the rejection reasons appended to its brief.",
		},
	}
	switch agent {
	case op.AgentAlignmentAuditor:
		q.Options = append(q.Options, op.Option{
			Choice:      choiceSkip,
			Label:       "Skip the audit",
			Description: "Proceed without the alignment digest (advisory only).",
		})
	case op.AgentReviewer:
		q.Options = append(q.Options, op.Option{
			Choice:      choiceSkip,
			Label:       "Skip the review",
			Description: "Proceed without the internal review for this wave (advisory only).",
		})
	}
	q.Options = append(q.Options, op.Option{
		Choice:      choiceStop,
		Label:       labelStop,
		Description: "End the turn; the brief the agent was given is under briefs/, its rejections in the event log.",
	})
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

// questionReviewError fills the "review_error" gate. Only the retry option's
// description depends on the facts: it names the deadline to raise when what
// failed was a timeout, which is the one thing a user hitting this gate twice
// otherwise has to read the source to learn (spec A3).
func questionReviewError(q *op.Op, ctx map[string]any) {
	q.Narration = "a task review errored"
	q.Question = fmt.Sprintf("The reviewer failed for task(s) %v: %v", ctx["tasks"], ctx["error"])
	q.Options = []op.Option{
		{Choice: choiceRetry, Label: "Retry the review (Recommended)", Description: reviewErrorRetryText(ctx)},
		{
			Choice:      "skip",
			Label:       "Skip review for these tasks",
			Description: "Record an evidenced skip (`--reason`) and accept the tasks.",
		},
		{Choice: choiceStop, Label: labelStop, Description: "End the turn with the wave open."},
	}
}

// reRunClose is the retry option's first sentence, unchanged from before the
// gate named any key: what the choice actually does.
const reRunClose = "Re-run `takt close-wave`."

// backendKeyPlaceholder is the key shape the retry option falls back to when
// the run's reviewer chain names no backend config can speak for — an empty
// chain, or one holding only "fake" and names config does not know. It
// carries no deadline, because there is no configured one to quote.
const backendKeyPlaceholder = "backends.<name>.timeout"

// reviewErrorRetryText is the retry option's description: re-running the
// close, plus which key raises the deadline when that is what the review hit.
func reviewErrorRetryText(ctx map[string]any) string {
	named := backendDeadlines(ctx)
	if len(named) == 0 {
		return reRunClose + " If the review timed out, raising `" + backendKeyPlaceholder +
			"` in `.takt.json` is the fix."
	}
	return reRunClose + " If the review timed out, raising the deadline in `.takt.json` is the fix: " +
		strings.Join(named, ", ") + "."
}

// backendDeadlines reads the `backends` context entry decideActiveWave built
// — one pre-rendered key and deadline per reviewer backend that has a config
// key, in preference order — and renders each as `key` (now duration).
//
// The read is defensive in the style internal/cli's toInt uses: a value that
// is not the shape decide wrote is skipped rather than asserted on, since
// this same map arrives both freshly built and decoded from the gate payload
// persisted on disk. It is one code path over two encodings of one shape,
// not two code paths.
func backendDeadlines(ctx map[string]any) []string {
	raw, isList := ctx[ctxBackends].([]any)
	if !isList {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		m, isMap := e.(map[string]any)
		if !isMap {
			continue
		}
		key, hasKey := m[ctxBackendKey].(string)
		if !hasKey || key == "" {
			continue
		}
		if d, hasTimeout := m[ctxBackendTimeout].(string); hasTimeout && d != "" {
			out = append(out, "`"+key+"` (now "+d+")")
			continue
		}
		out = append(out, "`"+key+"`")
	}
	return out
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

// questionBranchFinish fills the "branch_finish" gate. Exactly one option is
// ever recommended, it is listed first, and it is enabled: recommending the
// disabled merge — which `takt init` blocks in its own flow, since the run
// branch is checked out in the primary worktree — told the user to choose
// something the binary refuses (#26). When merge is available it is still
// the recommendation; when it is not, opening a pull request is.
func questionBranchFinish(q *op.Op, ctx map[string]any) {
	q.Narration = "choose what happens to the branch"
	q.Question = fmt.Sprintf("Run %v is verified on %v (base %v). What should happen to the branch?",
		ctx[ctxSlug], ctx["branch"], ctx["base"])
	pr := op.Option{
		Choice: "pr",
		Label:  "Push and open a pull request",
		Description: "The session pushes the branch and runs `gh pr create --base <base> " +
			"--title '<title>' --body-file <path>` with the op's `pr_title` and `pr_body_path` " +
			"inputs, then `takt done --step push_pr`.",
	}
	keep := op.Option{
		Choice:      "keep",
		Label:       "Keep the branch as-is",
		Description: "Archive the run; you integrate later.",
	}
	if adopted, _ := ctx["adopted"].(bool); adopted {
		pr.Label += suffixRecommended
		q.Options = []op.Option{pr, keep}
		return
	}
	merge := op.Option{
		Choice:      "merge",
		Label:       "Merge into the base branch locally",
		Description: "`git merge --no-ff` in the primary worktree after the archive commit; the branch is deleted when nothing has it checked out.",
	}
	discard := op.Option{
		Choice:      "discard",
		Label:       "Discard the work",
		Description: "Requires `--confirm <slug>`. The bundle is copied to <dir>/.discarded/<slug>/ and the branch force-deleted.",
	}
	if allowed, _ := ctx["discard_allowed"].(bool); !allowed {
		discard.Disabled, _ = ctx["discard_blocked"].(string)
	}
	if allowed, _ := ctx["merge_allowed"].(bool); !allowed {
		merge.Disabled, _ = ctx["merge_blocked"].(string)
		pr.Label += suffixRecommended
		q.Options = []op.Option{pr, keep, merge, discard}
		return
	}
	merge.Label += suffixRecommended
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
