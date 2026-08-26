package cli

import (
	"context"
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
	prompt, err := brief.Render("review-"+g, brief.ReviewData{
		Gate: g, Title: tgt.slug + " " + g, Token: tok, Schema: backend.ResultSchema, Files: files,
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
	if err = writeFindings(filepath.Join(tgt.bdir, "reviews", g+".md"), g, res); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
	}
	rc := gate.Receipt{
		Gate: g, Hash: hash, Verdict: res.Verdict,
		Reviewer: gate.Reviewer{Provider: res.Provider, Model: res.Model},
		Findings: "reviews/" + g + ".md", TS: timeNow(),
	}
	if err = gate.WriteReceipt(tgt.bdir, rc); err != nil {
		return fail(env.Stderr, exitError, err.Error(), "")
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

// writeFindings renders a reviewer result as markdown for humans.
func writeFindings(path, title string, res backend.ReviewResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Review: %s — %s\n\n%s\n\n", title, res.Verdict, res.Summary)
	if res.Reason != "" {
		fmt.Fprintf(&b, "Reason: %s\n\n", res.Reason)
	}
	for _, f := range res.Findings {
		fmt.Fprintf(&b, "- **%s** %s:%d — %s: %s\n", f.Severity, f.File, f.Line, f.Title, f.Detail)
	}
	fmt.Fprintf(&b, "\n_%s / %s_\n", res.Provider, res.Model)
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
