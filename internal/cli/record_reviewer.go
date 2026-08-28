package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/wave"
)

// reviewerModeVerify is the reviewer's non-lens mode (two-layers design §3.3).
const reviewerModeVerify = "verify"

// evLensIgnored is the event a stale-attempt or already-verified lens reply
// appends — named so the two sites in this file that append it (a stale
// dispatch attempt, and a lens record arriving after the verifier has
// already judged its candidates) spell the same literal once.
const evLensIgnored = "lens_ignored"

// Event/output keys this file's records use that no other command file
// shares — named so goconst sees one definition of each rather than a
// literal repeated across recordLens and recordVerify.
const (
	keyDropped    = "dropped"
	keyCandidates = "candidates"
	keyConfirmed  = "confirmed"
)

// lensReply is the JSON shape a lens agent returns.
type lensReply struct {
	Lens     string `json:"lens"`
	Findings []struct {
		Severity string `json:"severity"`
		File     string `json:"file"`
		Line     int    `json:"line"`
		Title    string `json:"title"`
		Detail   string `json:"detail"`
	} `json:"findings"`
}

// verifyReply is the JSON shape the verifier returns.
type verifyReply struct {
	Mode     string                  `json:"mode"`
	Verdicts []wave.CandidateVerdict `json:"verdicts"`
}

// recordReviewer records one reviewer reply: a lens's findings, or the
// verifier's verdicts (two-layers design §5.1, §5.3). What the agent got
// wrong is a problem list at exit 0 with a reviewer_invalid event — the
// planner's contract; takt's own invariants (no active wave, unreadable
// file, a mode outside the frozen set) exit 1.
func recordReviewer(env Env, tgt *runTarget, mode string, attempt int, from string) int {
	aw := tgt.st.ActiveWave
	if aw == nil {
		return fail(env.Stderr, exitError, "no active wave", "run `takt next`")
	}
	lenses := tgt.st.Config.Review.Lenses
	if mode != reviewerModeVerify && !slices.Contains(lenses, mode) {
		// The behaviour contract (design §5.1) groups this with "no active
		// wave" and "unreadable --from" as takt's own invariants, all three
		// exiting 1 — not exitUsage (2), which the brief's own draft used
		// here.
		return fail(env.Stderr, exitError,
			fmt.Sprintf("--mode %q is neither a configured lens nor verify", mode), "")
	}
	if attempt != aw.Attempt {
		_ = bundle.AppendEvent(tgt.bdir, evLensIgnored, map[string]any{
			keyWave: aw.N, keySlice: sliceOf(aw), keyAttempt: attempt, keyMode: mode,
			keyReason: reasonStaleAttempt,
		})
		return printJSON(env, map[string]any{keyIgnored: true, keyReason: reasonStaleAttempt})
	}
	raw, err := os.ReadFile(from)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if mode == reviewerModeVerify {
		return recordVerify(env, tgt, string(raw))
	}
	return recordLens(env, tgt, mode, string(raw))
}

// recordLens validates and writes one lens record. A reply the record
// cannot use leaves the dispatch pending — the next `takt next`
// re-dispatches exactly this lens with nothing else disturbed.
func recordLens(env Env, tgt *runTarget, lens, msg string) int {
	aw := tgt.st.ActiveWave
	existing, err := wave.ReadInternalRecord(tgt.bdir, aw.N, sliceOf(aw), aw.Attempt)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if existing != nil {
		// The verifier has judged this dispatch's candidate list; a late
		// lens record must not change it under the verdicts (design §5.1).
		_ = bundle.AppendEvent(tgt.bdir, evLensIgnored, map[string]any{
			keyWave: aw.N, keySlice: sliceOf(aw), keyAttempt: aw.Attempt, keyMode: lens,
			keyReason: "internal review already verified",
		})
		return printJSON(env, map[string]any{keyIgnored: true, keyReason: "internal review already verified"})
	}
	reply, problems := parseLensReply(msg, lens)
	if len(problems) > 0 {
		_ = bundle.AppendEvent(tgt.bdir, evReviewerInvalid, map[string]any{keyMode: lens, keyProblems: problems})
		return printJSON(env, map[string]any{keyValid: false, keyProblems: problems})
	}
	rec := wave.LensRecord{
		Lens: lens, Wave: aw.N, Slice: sliceOf(aw), Attempt: aw.Attempt,
		Model: tgt.ws.Cfg.Agents.Reviewer.Model, RecordedAt: timeNow(),
		Findings: []wave.LensFinding{},
	}
	for _, f := range reply.Findings {
		if f.File == "" {
			rec.Dropped = append(rec.Dropped, wave.DroppedFinding{Title: f.Title, Reason: "no file cited"})
			continue
		}
		rec.Findings = append(rec.Findings, wave.LensFinding{
			Finding: backend.Finding{
				Severity: f.Severity, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
			},
			Task: taskForFile(tgt.st, aw, f.File),
		})
	}
	if err = wave.WriteLensRecord(tgt.bdir, rec); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(tgt.bdir, "lens_recorded", map[string]any{
		keyWave: aw.N, keySlice: rec.Slice, keyAttempt: rec.Attempt, keyMode: lens,
		keyFindings: len(rec.Findings), keyDropped: len(rec.Dropped),
	})
	lost := endAttemptStreak(tgt.bdir, evReviewerInvalid, evReviewerReset,
		map[string]any{keyReason: reasonRecorded, keyMode: lens})
	return printJSON(env, warnStreakLoss(map[string]any{
		keyValid: true, keyMode: lens, keyFindings: len(rec.Findings),
	}, lost))
}

// severities is the closed severity set a lens finding must use.
var severities = map[string]bool{"blocking": true, "major": true, "minor": true, "nit": true}

// parseLensReply pulls the JSON block out of a lens's message; the returned
// problems make it unusable, and only one of the two returns is ever set.
func parseLensReply(msg, lens string) (*lensReply, []string) {
	js, err := backend.ExtractJSON(msg)
	if err != nil {
		return nil, []string{"no JSON block in the reviewer's message: " + err.Error()}
	}
	var r lensReply
	if uerr := json.Unmarshal(js, &r); uerr != nil {
		return nil, []string{"the reviewer's JSON block does not parse: " + uerr.Error()}
	}
	var problems []string
	if r.Lens != "" && r.Lens != lens {
		problems = append(problems, fmt.Sprintf("the reply names lens %q but was dispatched as %q", r.Lens, lens))
	}
	for i, f := range r.Findings {
		if !severities[f.Severity] {
			problems = append(problems, fmt.Sprintf("finding %d: unknown severity %q", i+1, f.Severity))
		}
		if f.Title == "" {
			problems = append(problems, fmt.Sprintf("finding %d has no title", i+1))
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return &r, nil
}

// taskForFile attributes a finding to the slice task that declares its file;
// within a wave those are disjoint by plan validation, so the answer is
// unique — 0 when no task declares it (two-layers design §5.1).
func taskForFile(st *bundle.State, aw *bundle.ActiveWave, file string) int {
	for _, id := range aw.Tasks {
		if t := st.Task(id); t != nil && slices.Contains(t.Files, file) {
			return id
		}
	}
	return 0
}

// recordVerify validates the verifier's verdicts against the recomputed
// candidate list, writes the internal record, and carries the confirmed
// findings no task owns to follow-ups — here, once per attempt by
// construction, where a re-run close could carry them twice (design §3.5).
func recordVerify(env Env, tgt *runTarget, msg string) int {
	aw := tgt.st.ActiveWave
	existing, rerr := wave.ReadInternalRecord(tgt.bdir, aw.N, sliceOf(aw), aw.Attempt)
	if rerr != nil {
		return fail(env.Stderr, exitError, rerr.Error(), "")
	}
	if existing != nil {
		// A replay of a dispatch already verified — every record is safe to
		// run twice (design §5.4 "every op is safe to execute twice"), so
		// this must not re-append internal_review_recorded or carry the
		// unattributed findings to follow-ups.json a second time.
		return printJSON(env, map[string]any{
			keyValid: true, keyIgnored: true,
			keyCandidates: len(existing.Candidates), keyConfirmed: len(existing.Confirmed),
		})
	}
	lenses := tgt.st.Config.Review.Lenses
	records := map[string]*wave.LensRecord{}
	for _, l := range lenses {
		r, err := wave.ReadLensRecord(tgt.bdir, aw.N, sliceOf(aw), aw.Attempt, l)
		if err != nil {
			return fail(env.Stderr, exitError, err.Error(), "")
		}
		if r == nil {
			return fail(env.Stderr, exitError, "lens "+l+" has no record for this dispatch",
				"run `takt next`; the verify dispatch comes after every lens is recorded")
		}
		records[l] = r
	}
	candidates := wave.MergeCandidates(lenses, records)
	if len(candidates) == 0 {
		return fail(env.Stderr, exitError, "no candidates to verify",
			"run `takt next`; with zero candidates the internal review completes without a verifier")
	}
	verdicts, problems := parseVerifyReply(msg, candidates)
	if len(problems) > 0 {
		_ = bundle.AppendEvent(tgt.bdir, evReviewerInvalid, map[string]any{
			keyMode: reviewerModeVerify, keyProblems: problems,
		})
		return printJSON(env, map[string]any{keyValid: false, keyProblems: problems})
	}
	rec := wave.InternalRecord{
		Wave: aw.N, Slice: sliceOf(aw), Attempt: aw.Attempt,
		Model: tgt.ws.Cfg.Agents.Reviewer.Model, RecordedAt: timeNow(),
		Lenses: slices.Clone(lenses), Candidates: candidates, Verdicts: verdicts,
		Confirmed: []string{},
	}
	for _, v := range verdicts {
		if v.Verdict == wave.VerdictConfirmed {
			rec.Confirmed = append(rec.Confirmed, v.ID)
		}
	}
	if err := wave.WriteInternalRecord(tgt.bdir, rec); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(tgt.bdir, "internal_review_recorded", map[string]any{
		keyWave: aw.N, keySlice: rec.Slice, keyAttempt: rec.Attempt,
		keyCandidates: len(candidates), keyConfirmed: len(rec.Confirmed),
	})
	// Deliberate ordering: the record is written before the unattributed
	// findings are carried, so a crash in between drops that carry on
	// replay — the `existing != nil` short-circuit above sees the record and
	// returns before carryUnattributed runs again. The reverse order would
	// instead double-carry on replay, which is worse: a dropped carry is a
	// silent loss recoverable only by noticing it's missing, but a duplicate
	// follow-up is loud without the run rerunning being reason enough to
	// review it twice. The clean resolution is follow-up de-duplication
	// (#44), not an ordering that avoids the crash window entirely.
	if code := carryUnattributed(env, tgt, &rec); code != 0 {
		return code
	}
	lost := endAttemptStreak(tgt.bdir, evReviewerInvalid, evReviewerReset,
		map[string]any{keyReason: reasonRecorded, keyMode: reviewerModeVerify})
	return printJSON(env, warnStreakLoss(map[string]any{
		keyValid: true, keyCandidates: len(candidates), keyConfirmed: len(rec.Confirmed),
	}, lost))
}

// parseVerifyReply validates exactly one verdict per candidate id and the
// evidence bar: a confirmed verdict without evidence and a citation is a
// rejection, enforced by Go rather than asked for (design D7).
func parseVerifyReply(msg string, candidates []wave.Candidate) ([]wave.CandidateVerdict, []string) {
	js, err := backend.ExtractJSON(msg)
	if err != nil {
		return nil, []string{"no JSON block in the verifier's message: " + err.Error()}
	}
	var r verifyReply
	if uerr := json.Unmarshal(js, &r); uerr != nil {
		return nil, []string{"the verifier's JSON block does not parse: " + uerr.Error()}
	}
	known := map[string]bool{}
	for _, c := range candidates {
		known[c.ID] = true
	}
	var problems []string
	seen := map[string]bool{}
	for _, v := range r.Verdicts {
		switch {
		case !known[v.ID]:
			problems = append(problems, fmt.Sprintf("verdict for unknown candidate %q", v.ID))
		case seen[v.ID]:
			problems = append(problems, fmt.Sprintf("two verdicts for candidate %q", v.ID))
		}
		seen[v.ID] = true
		if v.Verdict != wave.VerdictConfirmed && v.Verdict != wave.VerdictFalsePositive {
			problems = append(problems, fmt.Sprintf("%s: unknown verdict %q", v.ID, v.Verdict))
		}
		if v.Verdict == wave.VerdictConfirmed && (v.Evidence == "" || len(v.Citations) == 0) {
			problems = append(problems, v.ID+": confirmed without evidence and a citation")
		}
	}
	for _, c := range candidates {
		if !seen[c.ID] {
			problems = append(problems, "no verdict for candidate "+c.ID)
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return r.Verdicts, nil
}

// carryUnattributed appends the confirmed findings no task owns to
// follow-ups.json — they never reach the backend or a retry brief, so this
// is their only route to a human (design D11).
func carryUnattributed(env Env, tgt *runTarget, rec *wave.InternalRecord) int {
	unowned := rec.ConfirmedByTask()[0]
	items := make([]gate.FollowUp, 0, len(unowned))
	for _, f := range unowned {
		items = append(items, gate.FollowUp{
			Severity: f.Severity, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
			Source: gate.SourceInternal, Wave: rec.Wave, TS: timeNow(),
		})
	}
	if err := gate.AppendFollowUps(tgt.bdir, items...); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return 0
}
