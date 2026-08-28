package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
)

// reviewOpts is `takt review`'s parsed command line.
type reviewOpts struct {
	dir      string
	slug     string
	gate     string
	skip     bool
	reason   string
	evidence string
	force    bool
}

// cmdReview runs a gate review headless and writes the hash-bound receipt,
// or records an evidenced skip instead (spec §9).
func cmdReview(env Env) int {
	o, code := reviewFlags(env)
	if code != 0 {
		return code
	}
	ctx, cancel := commandContext(env)
	defer cancel()
	tgt, code := openTarget(ctx, env, o.dir, o.slug)
	if code != 0 {
		return code
	}
	hash, present, err := gate.Hash(o.gate, tgt.bdir)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if o.skip {
		return recordSkip(env, tgt, o, hash)
	}
	if !o.force {
		if rc, ok := cachedReceipt(tgt.bdir, o.gate, hash); ok {
			return printJSON(env, map[string]any{
				keyGate: o.gate, keyVerdict: rc.Verdict, keyProvider: rc.Reviewer.Provider,
				"cached": true, "receipt": "gates/" + o.gate + ".json",
			})
		}
	}
	return runReview(env, tgt, o.gate, hash, present)
}

// cachedReceipt returns the receipt that already answers a review of gate
// at hash: one whose hash is current and whose verdict is a reviewer's
// word — approve, rework or reject. An `error` verdict and an evidenced
// skip are not answers, so they never short-circuit a re-run (spec §9).
// This is what makes `exec review` safe to execute twice (spec §5.4): a
// replayed op returns the receipt instead of a second backend call and a
// second `reviewed` commit at the same hash.
func cachedReceipt(bdir, g, hash string) (*gate.Receipt, bool) {
	r, err := gate.ReadReceipt(bdir, g)
	if err != nil || r == nil || r.Hash != hash || r.Skipped != nil || r.Verdict == gate.VerdictError {
		return nil, false
	}
	return r, true
}

// reviewGrace is what a gate review is allowed on top of the backend's own
// timeout, mirroring how closeWaveTimeout bounds the per-task reviews: takt's
// deadline must not fire before the backend's, so a slow reviewer reports its
// own timeout instead of being cut off by takt's.
const reviewGrace = 30 * time.Second

// reviewFlags parses `takt review spec|plan`'s positional gate and flags.
func reviewFlags(env Env) (reviewOpts, int) {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slugFlag := fs.String("slug", "", "run")
	skip := fs.Bool("skip", false, "record an evidenced skip instead of reviewing")
	reason := fs.String("reason", "", "why the review was skipped")
	evidence := fs.String("evidence", "", "file holding the backend's error output")
	force := fs.Bool("force", false, "re-run the reviewer even when a receipt already answers at the current hash")
	positional, err := parseInterspersed(fs, env.Args)
	if err != nil {
		return reviewOpts{}, usageError(env, fs, err)
	}
	if len(positional) != 1 || (positional[0] != gate.Spec && positional[0] != gate.Plan) {
		return reviewOpts{}, fail(env.Stderr, exitUsage, "usage: takt review spec|plan", "")
	}
	return reviewOpts{
		dir: *dirFlag, slug: *slugFlag, gate: positional[0],
		skip: *skip, reason: *reason, evidence: *evidence, force: *force,
	}, 0
}

// runReview asks the configured reviewer for a verdict and records it.
//
// The reviewer runs under a deadline of its own — the backend's timeout plus
// reviewGrace — not under the one commandContext hands out. That budget
// bounds git (spec §13, two minutes by default, and a test or a caller may
// shorten it to seconds), while a spec or plan review is routinely minutes
// of backend work: running the reviewer under it killed healthy reviews with
// "context deadline exceeded" (review I5). The git work after the verdict
// takes a fresh commandContext for the same reason — the budget bounds each
// git call, and the review may well have outlasted the one the command
// started with.
func runReview(env Env, tgt *runTarget, g, hash string, present []string) int {
	reviewer, be, err := reviewerFor(tgt.ws, env)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(),
			"install copilot or claude, or record an evidenced skip with --skip --reason … --evidence …")
	}
	tok, _ := brief.Token()
	files := map[string]string{}
	for _, name := range present {
		files[name] = readArtifact(tgt.bdir, name)
	}
	tmpl, prior := "review-"+g, []brief.PriorFinding(nil)
	if g == gate.Spec {
		if prior = priorFindingsForScopedPass(tgt.bdir); len(prior) > 0 {
			tmpl = "review-spec-followup"
		}
	}
	prompt, err := brief.Render(tmpl, brief.ReviewData{
		Gate: g, Title: tgt.slug + " " + g, Token: tok, Schema: backend.ResultSchema,
		Files: files, PriorFindings: prior,
	})
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	rctx, rcancel := context.WithTimeout(context.Background(), time.Duration(be.Timeout)+reviewGrace)
	defer rcancel()
	res, err := reviewer.Review(rctx, backend.ReviewRequest{
		Rubric: g, Title: tgt.slug, Prompt: prompt, RepoRoot: tgt.ws.Repo.Root,
		Model: be.Model, Effort: be.Effort, Timeout: time.Duration(be.Timeout),
		LogDir: filepath.Join(tgt.bdir, "logs"), LogID: fmt.Sprintf("review-%s-%d", g, time.Now().Unix()),
	})
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	if err = storeFindings(tgt.bdir, g, res); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	rc := gate.Receipt{
		Gate: g, Hash: hash, Verdict: res.Verdict,
		Reviewer: gate.Reviewer{Provider: res.Provider, Model: res.Model},
		Findings: "reviews/" + g + ".md", Severities: res.SeverityCounts(), TS: timeNow(),
	}
	if err = gate.WriteReceipt(tgt.bdir, rc); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	// An approving pass closes the gate without asking anyone for anything,
	// so its findings would otherwise die in reviews/<gate>.md (#29).
	if res.Verdict == gate.VerdictApprove {
		if err = carryFindings(tgt.bdir, g, res.Findings, gate.SourceApprove); err != nil {
			return fail(env.Stderr, exitError, err.Error(), "")
		}
	}
	_ = bundle.AppendEvent(tgt.bdir, "gate_reviewed", map[string]any{
		keyGate: g, keyHash: hash, keyVerdict: res.Verdict, keyProvider: res.Provider, keyFindings: len(res.Findings),
	})
	gctx, gcancel := commandContext(env)
	defer gcancel()
	if _, _, err = commitBundle(gctx, tgt.ws, tgt.bdir, tgt.slug, g+" reviewed: "+res.Verdict); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	return printJSON(env, map[string]any{
		keyGate: g, keyVerdict: res.Verdict, keyFindings: len(res.Findings),
		keyProvider: res.Provider, keyReason: res.Reason,
	})
}

// recordSkip writes an evidenced skip receipt: a skip is a backend outage
// the user can point at, never a convenience (spec §9).
func recordSkip(env Env, tgt *runTarget, o reviewOpts, hash string) int {
	if strings.TrimSpace(o.reason) == "" || o.evidence == "" {
		return fail(env.Stderr, exitUsage, "--skip needs both --reason and --evidence",
			"a skip is an evidenced backend outage, never a convenience")
	}
	if !fileNonEmpty(o.evidence) {
		return fail(env.Stderr, exitError, "evidence file is missing or empty: "+o.evidence, "")
	}
	rel, err := preserveEvidence(tgt.bdir, o.gate, o.evidence)
	if err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	rc := gate.Receipt{
		Gate: o.gate, Hash: hash, Verdict: gate.VerdictError, TS: timeNow(),
		Skipped: &gate.Skipped{Reason: o.reason, EvidencePath: rel},
	}
	if err = gate.WriteReceipt(tgt.bdir, rc); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	_ = bundle.AppendEvent(tgt.bdir, "gate_skipped", map[string]any{
		keyGate: o.gate, keyHash: hash, keyReason: o.reason,
	})
	return printJSON(env, map[string]any{keyGate: o.gate, keyVerdict: "skipped", keyReason: o.reason})
}

// preserveEvidence copies the backend's error output into the bundle as
// gates/<gate>.evidence.txt and returns that bundle-relative path. The file
// the user pointed at is usually a scratch file outside the repo, so
// recording its absolute path would leave the receipt citing evidence that
// is unreadable on any other machine and gone by the next reboot; every
// path in a receipt is bundle-relative (spec §4.5), the same convention
// `findings: reviews/<gate>.md` already follows.
func preserveEvidence(bdir, g, src string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	rel := "gates/" + g + ".evidence.txt"
	dest := filepath.Join(bdir, filepath.FromSlash(rel))
	if err = os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", err
	}
	//nolint:gosec // G703: dest is inside the run's bundle dir under a fixed
	// gates/<gate>.evidence.txt name; the slug it comes from is validated by
	// bundle.ValidSlug (see selectSlug) and gate is spec|plan, so no caller
	// can steer this write.
	if err = os.WriteFile(dest, b, 0o600); err != nil {
		return "", err
	}
	return rel, nil
}

// storeFindings records a pass's findings in both shapes: reviews/<gate>.md
// for a human and reviews/<gate>.json for the code that has to read a
// finding as data.
//
// An `error` verdict records neither. It is the backend failing, not a
// reviewer's answer — the same reason cachedReceipt refuses to let one
// short-circuit a re-run — and the stored findings are a live referent
// rather than a log: priorFindingsForScopedPass reads the .json to scope the
// confirming pass, and the carry-forward reads it on accept. Overwriting
// them with an errored result's empty findings would let one transient
// backend failure delete the blocking findings a previous pass earned and
// drop the run back into the unscoped re-review loop the spec gate's fixed
// point exists to end. The receipt is still written by the caller, so the
// run sees the failure; only the findings survive it.
func storeFindings(bdir, g string, res backend.ReviewResult) error {
	if res.Verdict == gate.VerdictError {
		return nil
	}
	if err := writeFindings(filepath.Join(bdir, "reviews", g+".md"), g, res); err != nil {
		return err
	}
	return writeResultJSON(filepath.Join(bdir, "reviews", g+".json"), res)
}

// renderFindings renders a reviewer result as markdown for humans. It
// returns the text rather than writing it so that a caller with more to say
// about the same pass — writeTaskFindings, which adds the scoped pass and
// the confirmed internal findings — can build one document and write it
// once.
func renderFindings(title string, res backend.ReviewResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review: %s — %s\n\n%s\n\n", title, res.Verdict, res.Summary)
	if res.Reason != "" {
		fmt.Fprintf(&b, "Reason: %s\n\n", res.Reason)
	}
	for _, f := range res.Findings {
		fmt.Fprintf(&b, "- **%s** %s:%d — %s: %s\n", f.Severity, f.File, f.Line, f.Title, f.Detail)
	}
	fmt.Fprintf(&b, "\n_%s / %s_\n", res.Provider, res.Model)
	return b.String()
}

// writeFindings writes renderFindings' markdown to path, atomically; the
// write creates the reviews directory when it is not there yet.
func writeFindings(path, title string, res backend.ReviewResult) error {
	return bundle.WriteFileAtomic(path, []byte(renderFindings(title, res)))
}

// writeResultJSON stores the reviewer's structured result beside the human
// rendering. writeFindings renders severities into a markdown bullet and the
// structure is lost, so nothing downstream can read a finding as data: the
// scoped follow-up pass needs the prior findings to quote, and the
// carry-forward needs them to record. Written for both gates because
// runReview is shared and the cost is one file; only the spec gate reads it.
func writeResultJSON(path string, res backend.ReviewResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return bundle.WriteJSONAtomic(path, res)
}

// priorFindingsForScopedPass returns the previous spec pass's findings when
// that pass asked for rework over something blocking — the one case a second
// review call is spent on (fixed-point design §5). The pass that follows is
// scoped to these, which is what gives it a finite referent and lets it
// terminate; "is this spec unambiguous?" never could. It returns the whole
// finding list, not just the blocking ones: the blocking finding is what
// buys the pass, and the pass then confirms everything the previous one said.
//
// Both halves of the judgement — the verdict, and whether anything was
// blocking — come from reviews/<gate>.json rather than from the receipt,
// because the two artifacts have different jobs. The receipt records the
// gate's state *including* its failures, which is why an errored pass still
// writes one; the findings file records the content of the last pass a
// reviewer actually answered, which is why storeFindings leaves it alone on
// an error verdict. Scoping is a content question, so an errored pass is
// transparent to it: a transient backend failure between a blocking rework
// and its confirming pass must not silently widen that pass back to the
// whole document, which is exactly what reading the receipt did. Taking both
// halves from one artifact also makes the decision self-consistent — the
// findings file carries no hash and no pass identity, so pairing it with the
// receipt was convention, not a checkable invariant.
func priorFindingsForScopedPass(bdir string) []brief.PriorFinding {
	res, err := readReviewResult(bdir, gate.Spec)
	if err != nil || res.Verdict != backend.VerdictRework || res.SeverityCounts()["blocking"] == 0 {
		return nil
	}
	out := make([]brief.PriorFinding, 0, len(res.Findings))
	for _, f := range res.Findings {
		out = append(out, brief.PriorFinding{
			Severity: f.Severity, File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
		})
	}
	return out
}

// readReviewResult reads reviews/<gate>.json, the structured result
// runReview stored beside the human rendering. An absent file means no
// findings: a run whose reviews predate the file carries nothing forward
// rather than failing.
func readReviewResult(bdir, g string) (backend.ReviewResult, error) {
	b, err := os.ReadFile(filepath.Join(bdir, "reviews", g+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return backend.ReviewResult{}, nil
	}
	if err != nil {
		return backend.ReviewResult{}, err
	}
	var r backend.ReviewResult
	if uerr := json.Unmarshal(b, &r); uerr != nil {
		return backend.ReviewResult{}, fmt.Errorf("reviews/%s.json: %w", g, uerr)
	}
	return r, nil
}

// carryFindings records findings nobody was asked to act on as follow-ups
// (fixed-point design §6). An approving pass's minors and the findings a
// user overrode both reach the retro this way instead of being frozen in
// reviews/<gate>.md. Findings that were the instruction for a revise are not
// carried — the session was asked to act on those.
func carryFindings(bdir, g string, fs []backend.Finding, source string) error {
	items := make([]gate.FollowUp, 0, len(fs))
	for _, f := range fs {
		items = append(items, gate.FollowUp{
			Gate: g, Severity: f.Severity, File: f.File, Line: f.Line,
			Title: f.Title, Detail: f.Detail, Source: source, TS: timeNow(),
		})
	}
	return gate.AppendFollowUps(bdir, items...)
}
