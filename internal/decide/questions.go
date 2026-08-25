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

// questionDefault fills an unrecognised gate id with a generic continue/stop choice.
func questionDefault(q *op.Op, gate string) {
	q.Narration = "gate " + gate
	q.Question = "Resolve gate " + gate
	q.Options = []op.Option{
		{Choice: "continue", Label: "Continue", Description: ""},
		{Choice: choiceStop, Label: labelStop, Description: ""},
	}
}
