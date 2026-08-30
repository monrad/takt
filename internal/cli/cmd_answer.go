package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/op"
)

// Choice values shared by more than one gate handler in this file — named
// so goconst does not flag the repeated literals.
const (
	choiceRetry = "retry"
	choiceStop  = "stop"
)

// cmdAnswer resolves a pending gate: it records the choice, applies it, and
// clears the gate. Answering a gate that is no longer pending is a no-op
// (spec §5.4).
func cmdAnswer(env Env) int {
	fs := flag.NewFlagSet("answer", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	g := fs.String("gate", "", "gate id")
	choice := fs.String("choice", "", "chosen option")
	reason := fs.String("reason", "", "reason for an override/waiver")
	file := fs.String("file", "", "file with a corrected clause list (alignment_confirm edit)")
	confirm := fs.String("confirm", "", "type the slug to confirm a discard (branch_finish)")
	if _, err := parseInterspersed(fs, env.Args); err != nil {
		return usageError(env, fs, err)
	}
	if *g == "" || *choice == "" {
		return fail(env.Stderr, exitUsage, "answer needs --gate and --choice", "")
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, *dirFlag, *slugFlag)
	if code != 0 {
		return code
	}
	if *g == "owner" {
		return printJSON(env, map[string]any{
			keyGate: "owner", keyChoice: *choice,
			keyHint: "takeover = `takt next --force`; abort/readonly = nothing to do",
		})
	}
	if tgt.st.PendingGate == nil || tgt.st.PendingGate.ID != *g {
		// Ignored at exit 0 is the contract (spec §5.4, same as a stale
		// digest) — but a session that read reviews/<gate>.md itself and
		// answered before `takt next` armed the gate would otherwise take
		// that exit code as "answered" and find the same gate back on the
		// next call. The hint names the missing step (#27).
		return printJSON(env, map[string]any{
			keyIgnored: true, keyReason: "no pending gate " + *g,
			keyHint: "run `takt next` first: a gate is answerable only once its ask op has been emitted",
		})
	}
	keep, err := applyAnswer(ctx, tgt, *g, *choice, *reason, *file, *confirm)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if keep {
		return printJSON(env, map[string]any{keyGate: *g, keyChoice: *choice, "kept": true})
	}
	if err = clearGate(tgt.bdir, tgt.st, *choice, *reason); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if _, _, err = commitBundle(ctx, tgt.ws, tgt.bdir, tgt.slug, "gate "+*g+": "+*choice); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{keyGate: *g, keyChoice: *choice, "cleared": true})
}

// applyAnswer applies the choice to the gate's own state. keep is true when
// the gate must stay open (the user chose to stop).
func applyAnswer(ctx context.Context, tgt *runTarget, g, choice, reason, file, confirm string) (bool, error) {
	switch g {
	case "gate_review":
		return answerGateReview(tgt.bdir, tgt.st, choice, reason)
	case "gate_review_capped":
		return answerGateReviewCapped(tgt.bdir, tgt.st, choice, reason)
	case "alignment_confirm":
		return answerAlignment(tgt.bdir, tgt.st, choice, file)
	case "plan_invalid":
		if choice == choiceRetry {
			return false, bundle.AppendEvent(tgt.bdir, "plan_attempts_reset", nil)
		}
		return true, nil
	case "agent_invalid":
		return answerAgentInvalid(tgt.bdir, tgt.st, choice)
	case "wave_failures", "review_error":
		return answerWaveGate(tgt.bdir, tgt.st, g, choice, reason)
	case "verification_failed":
		return answerVerification(ctx, tgt, choice, reason)
	case "no_verification":
		return answerNoVerification(ctx, tgt, choice, reason)
	case "goals_unmet":
		return answerGoalsUnmet(tgt, choice, reason)
	case "branch_finish":
		return answerBranchFinish(ctx, tgt, choice, reason, confirm)
	}
	return false, errorf("unknown gate %s", g)
}

// answerGateReview applies a spec/plan review gate's choice: revise leaves
// the session to edit — recording an accepted revision first when the spec
// review found nothing blocking, so the edit closes the gate rather than
// re-arming it — and accept records an evidenced override at the current
// hash (spec §9, fixed-point design §4).
//
// retry — offered only on an error verdict, where no review was taken —
// writes nothing at all: the caller clears the gate and commits, and the
// session runs the named `takt review` before its next `takt next`, exactly
// as the op table requires when an option's text names work. If it does not,
// the same gate comes back, because the error receipt still answers at the
// current hash; nothing has been lost either way.
func answerGateReview(bdir string, st *bundle.State, choice, reason string) (bool, error) {
	which := pendingGateName(st)
	switch choice {
	case "revise":
		return false, acceptRevision(bdir, which)
	case "accept":
		return false, overrideGate(bdir, which, reason)
	case choiceRetry:
		return false, nil
	case choiceStop:
		return true, nil
	}
	return false, errorf("unknown choice %q for gate_review", choice)
}

// answerGateReviewCapped applies the round-cap gate's choice: accept records
// an override at the current hash, retry restarts the round count for one
// more pass, stop leaves the gate open (fixed-point design §8).
func answerGateReviewCapped(bdir string, st *bundle.State, choice, reason string) (bool, error) {
	which := pendingGateName(st)
	switch choice {
	case "accept":
		return false, overrideGate(bdir, which, reason)
	case choiceRetry:
		return false, bundle.AppendEvent(bdir, gate.EvRoundsReset, map[string]any{keyGate: which})
	case choiceStop:
		return true, nil
	}
	return false, errorf("unknown choice %q for gate_review_capped", choice)
}

// pendingGateName reads the gate id ("spec" or "plan") out of the pending
// gate's stored context; every review gate carries it under "gate".
func pendingGateName(st *bundle.State) string {
	var payload struct {
		Context map[string]any `json:"context"`
	}
	_ = json.Unmarshal(st.PendingGate.Payload, &payload)
	which, _ := payload.Context["gate"].(string)
	return which
}

// acceptRevision records that the user was shown a spec review asking for
// rework over nothing blocking, and chose to revise. gate.Compute turns that
// into a satisfied gate as soon as spec.md moves (fixed-point design §4).
//
// It writes nothing for the plan gate, for a blocking rework, or for
// reject/error: those keep the re-arm-and-re-review loop. It also writes
// nothing when there is no receipt at all — the gate has never been
// reviewed, so there is no verdict to accept — or when the receipt does not
// answer at the current hash, since then the user is not looking at the
// verdict the receipt records.
func acceptRevision(bdir, which string) error {
	if which != gate.Spec {
		return nil
	}
	hash, _, err := gate.Hash(which, bdir)
	if err != nil {
		return err
	}
	r, err := gate.ReadReceipt(bdir, which)
	if err != nil || r == nil || r.Hash != hash ||
		r.Verdict != gate.VerdictRework || r.Severities["blocking"] > 0 {
		return err
	}
	return bundle.AppendEvent(bdir, gate.EvRevisionAccepted, map[string]any{
		keyGate: which, keyHash: hash,
	})
}

// overrideGate records an evidenced override at the gate's current hash
// (spec §9). The user declined to act on the verdict's findings, so — like
// an approving pass — they must not vanish with the override; carryFindings
// records them.
//
// The event is appended before the findings are carried, deliberately: if
// the write dies between the two, a retry re-appends gate_overridden, and a
// duplicate of that event is inert — gate.Compute stops at the first one
// that matches the current hash. The carry is now idempotent on the
// follow-up's own identity (gate.AppendFollowUps keys each item by
// gate.FollowUp.Key), so a repeated carry is inert too; the event-first
// order is kept for the inert-duplicate reason alone.
func overrideGate(bdir, which, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errorf("accepting a %s review verdict needs --reason", which)
	}
	hash, _, err := gate.Hash(which, bdir)
	if err != nil {
		return err
	}
	if err = bundle.AppendEvent(bdir, "gate_overridden", map[string]any{
		keyGate: which, keyHash: hash, keyReason: reason,
	}); err != nil {
		return err
	}
	res, err := readReviewResult(bdir, which)
	if err != nil {
		return err
	}
	return carryFindings(bdir, which, res.Findings, gate.SourceOverride)
}

// answerAlignment confirms, corrects or skips the auditor's clause list.
func answerAlignment(bdir string, st *bundle.State, choice, file string) (bool, error) {
	a, _ := readAlignment(bdir)
	if a == nil {
		a = &alignmentFile{AnchorHash: anchorHash(st.Topic)}
	}
	switch choice {
	case "confirm":
		a.Confirmed = true
	case "edit":
		b, err := os.ReadFile(file)
		if err != nil {
			return false, errorf("--file is required for edit: %w", err)
		}
		var clauses []brief.Clause
		if err = json.Unmarshal(b, &clauses); err != nil || len(clauses) == 0 {
			return false, errors.New("--file must hold a JSON array of clauses")
		}
		a.Clauses, a.Confirmed, a.Verdicts = clauses, true, nil
	case "skip":
		return false, skipAlignment(bdir, st)
	default:
		return false, errorf("unknown choice %q for alignment_confirm", choice)
	}
	return false, writeAlignment(bdir, *a)
}

// answerAgentInvalid clears a capped agent: retry resets its attempt count
// through a *_attempts_reset event — the durable record, as the planner's
// is (spec §4.4) — carrying the rejection reasons forward so the retried
// brief can still quote them, and skip records the audit as skipped
// (alignment only).
func answerAgentInvalid(bdir string, st *bundle.State, choice string) (bool, error) {
	var payload struct {
		Context map[string]any `json:"context"`
	}
	_ = json.Unmarshal(st.PendingGate.Payload, &payload)
	agent, _ := payload.Context["agent"].(string)
	switch choice {
	case choiceRetry:
		reset := map[string]string{
			op.AgentAlignmentAuditor: evAlignmentReset,
			op.AgentGoalAssessor:     evGoalsReset,
			op.AgentReviewer:         evReviewerReset,
		}[agent]
		if reset == "" {
			return false, errorf("agent_invalid gate names no agent")
		}
		return false, bundle.AppendEvent(bdir, reset, map[string]any{keyProblems: payload.Context[keyProblems]})
	case "skip":
		switch agent {
		case op.AgentAlignmentAuditor:
			return false, skipAlignment(bdir, st)
		case op.AgentReviewer:
			return false, skipInternalReview(bdir, st)
		}
		return false, errorf("skip answers only the alignment-auditor or the reviewer, not the %s", agent)
	case choiceStop:
		return true, nil
	}
	return false, errorf("unknown choice %q for agent_invalid", choice)
}

// skipInternalReview records the internal review skipped for the active
// dispatch: the layer is advisory, so a skipped review reads as complete
// and close-wave proceeds without candidates (two-layers design §4.3).
func skipInternalReview(bdir string, st *bundle.State) error {
	aw := st.ActiveWave
	if aw == nil {
		return errorf("no active wave to skip the internal review for")
	}
	return bundle.AppendEvent(bdir, "internal_review_skipped", map[string]any{
		keyWave: aw.N, keySlice: sliceOf(aw), keyAttempt: aw.Attempt, keyReason: "agent_invalid",
	})
}

// skipAlignment records the audit as skipped for this run's anchor: the
// alignment digest is advisory, and a skipped audit reads as complete to
// every row that checks it (spec §7.3).
func skipAlignment(bdir string, st *bundle.State) error {
	a, _ := readAlignment(bdir)
	if a == nil || a.AnchorHash != anchorHash(st.Topic) {
		a = &alignmentFile{AnchorHash: anchorHash(st.Topic)}
	}
	a.Skipped = true
	return writeAlignment(bdir, *a)
}
