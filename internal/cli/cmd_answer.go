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
			"hint": "takeover = `takt next --force`; abort/readonly = nothing to do",
		})
	}
	if tgt.st.PendingGate == nil || tgt.st.PendingGate.ID != *g {
		return printJSON(env, map[string]any{keyIgnored: true, keyReason: "no pending gate " + *g})
	}
	keep, err := applyAnswer(ctx, tgt, *g, *choice, *reason, *file, *confirm)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if keep {
		return printJSON(env, map[string]any{keyGate: *g, keyChoice: *choice, "kept": true})
	}
	if err = clearGate(tgt.bdir, tgt.st, *choice); err != nil {
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
	case "alignment_confirm":
		return answerAlignment(tgt.bdir, tgt.st, choice, file)
	case "plan_invalid":
		if choice == "retry" {
			return false, bundle.AppendEvent(tgt.bdir, "plan_attempts_reset", nil)
		}
		return true, nil
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
// the gate to re-arm on the edited artifact's new hash, accept records an
// evidenced override at the current hash (spec §9).
func answerGateReview(bdir string, st *bundle.State, choice, reason string) (bool, error) {
	var payload struct {
		Context map[string]any `json:"context"`
	}
	_ = json.Unmarshal(st.PendingGate.Payload, &payload)
	which, _ := payload.Context["gate"].(string)
	switch choice {
	case "revise":
		return false, nil // the session edits; the hash re-arms the gate
	case "accept":
		if strings.TrimSpace(reason) == "" {
			return false, errorf("accepting a %s review verdict needs --reason", which)
		}
		hash, _, err := gate.Hash(which, bdir)
		if err != nil {
			return false, err
		}
		return false, bundle.AppendEvent(bdir, "gate_overridden", map[string]any{
			keyGate: which, keyHash: hash, keyReason: reason,
		})
	case "stop":
		return true, nil
	}
	return false, errorf("unknown choice %q for gate_review", choice)
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
		a.Skipped = true
	default:
		return false, errorf("unknown choice %q for alignment_confirm", choice)
	}
	return false, writeAlignment(bdir, *a)
}
