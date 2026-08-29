package backend_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/config"
)

// liveEnvVar gates every test in this file: a real reviewer CLI call spends
// money, hits a real model over the network, and can take minutes, so the
// hermetic suite (TAKT_LIVE unset) must stay entirely offline (Task 6
// brief).
const liveEnvVar = "TAKT_LIVE"

// healthCheckBudget bounds a Healthy() probe here; the reviewer's own
// --version call is normally instant.
const healthCheckBudget = 30 * time.Second

// smokeGrace is what a smoke test allows on top of the backend's own
// Timeout before giving up on the CLI call outright, mirroring
// deadline.Grace: the deadline the reviewer runs under must fire first, so
// a slow reviewer reports its own timeout instead of being cut off here.
const smokeGrace = 30 * time.Second

// rawTailBudget bounds how much of a failing raw output lands in a fatal
// test message.
const rawTailBudget = 400

// fixtureSpecTitle names the fixture in review findings and log ids.
const fixtureSpecTitle = "takt live smoke: greet design"

// fixtureSpec is a tiny design with one deliberate gap — no error-handling
// section — chosen so a rework verdict is plausible without being certain;
// the assertions below accept any of approve/rework/reject.
const fixtureSpec = `# Design: Greet

## Overview
Add an exported Greet(name string) string function to package greeting
that returns the string "Hello, " + name + "!".

## Requirements
- Greet("Ada") returns "Hello, Ada!".
- The function has a doc comment.
- A table-driven test covers at least one name.

## Assumptions & Open Decisions
- Assumes name is always a valid UTF-8 string; no further validation is
  specified.
`

// skipUnlessLive skips, naming liveEnvVar, unless it is set to "1".
func skipUnlessLive(t *testing.T) {
	t.Helper()
	if os.Getenv(liveEnvVar) != "1" {
		t.Skipf("skipping live reviewer smoke: set %s=1 to run it", liveEnvVar)
	}
}

// skipUnlessHealthy skips, naming the reviewer's binary, when it is not
// usable on this machine.
func skipUnlessHealthy(t *testing.T, r backend.Reviewer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckBudget)
	defer cancel()
	if err := r.Healthy(ctx); err != nil {
		t.Skipf("%s not usable on this machine: %v", r.Name(), err)
	}
}

// specPrompt renders the real review-spec template against the fixture,
// exactly as cmd_review.go's runReview does for the spec gate.
func specPrompt(t *testing.T) string {
	t.Helper()
	tok, err := brief.Token()
	if err != nil {
		t.Fatalf("brief.Token: %v", err)
	}
	prompt, err := brief.Render("review-spec", brief.ReviewData{
		Gate: "spec", Title: fixtureSpecTitle, Token: tok, Schema: backend.ResultSchema,
		Files: map[string]string{"spec.md": fixtureSpec},
	})
	if err != nil {
		t.Fatalf("brief.Render(review-spec): %v", err)
	}
	return prompt
}

// runSpecSmoke drives one backend's reviewer over the fixture spec end to
// end — the real CLI, a real model call — and asserts the contract every
// backend.Reviewer owes its callers (spec §8.1). Callers must skip via
// skipUnlessLive before doing anything else that could run outside
// TAKT_LIVE=1, and must call t.Parallel() themselves (it is not safe to
// hoist into this helper and still satisfy the paralleltest linter, which
// inspects the test function body directly).
func runSpecSmoke(t *testing.T, name string, be config.Backend) {
	skipUnlessLive(t)
	reg := backend.Registry(os.Getenv)
	reviewer, ok := reg[name]
	if !ok {
		t.Fatalf("no %s reviewer in the registry", name)
	}
	skipUnlessHealthy(t, reviewer)

	logDir := t.TempDir()
	repoDir := t.TempDir()
	logID := "live-" + name

	timeout := time.Duration(be.Timeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout+smokeGrace)
	defer cancel()

	start := time.Now()
	res, err := reviewer.Review(ctx, backend.ReviewRequest{
		Rubric: "spec", Title: fixtureSpecTitle, Prompt: specPrompt(t), RepoRoot: repoDir,
		Model: be.Model, Effort: be.Effort, Timeout: timeout, LogDir: logDir, LogID: logID,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("%s: Review returned an error (failures are reported as VerdictError instead): %v", name, err)
	}
	t.Logf("%s: verdict=%s provider=%s model=%s elapsed=%s", name, res.Verdict, res.Provider, res.Model, elapsed)

	for _, ext := range []string{"prompt", "stdout"} {
		p := filepath.Join(logDir, logID+"."+ext)
		if _, statErr := os.Stat(p); statErr != nil {
			t.Errorf("expected log file %s: %v", p, statErr)
		}
	}

	if res.Verdict == backend.VerdictError {
		t.Fatalf("%s: reported VerdictError: reason=%q stdout tail=%q", name, res.Reason, tailOf(res.Raw))
	}
	switch res.Verdict {
	case backend.VerdictApprove, backend.VerdictRework, backend.VerdictReject:
	default:
		t.Fatalf("%s: unexpected verdict %q", name, res.Verdict)
	}
	if res.Summary == "" {
		t.Errorf("%s: Summary is empty", name)
	}
	if res.Provider != name {
		t.Errorf("%s: Provider = %q, want %q", name, res.Provider, name)
	}
	assertJSONExtractable(t, name, res.Raw)
}

// TestLiveCopilotReviewsASpec smokes the copilot reviewer against the real
// copilot CLI (Task 6 brief).
func TestLiveCopilotReviewsASpec(t *testing.T) {
	t.Parallel()
	runSpecSmoke(t, "copilot", config.Defaults().Backends.Copilot)
}

// TestLiveClaudeReviewsASpec smokes the claude reviewer against the real
// claude CLI (Task 6 brief).
func TestLiveClaudeReviewsASpec(t *testing.T) {
	t.Parallel()
	runSpecSmoke(t, "claude", config.Defaults().Backends.Claude)
}

// tailOf bounds s to its last rawTailBudget bytes, prefixed with an
// ellipsis when truncated.
func tailOf(s string) string {
	if len(s) <= rawTailBudget {
		return s
	}
	return "…" + s[len(s)-rawTailBudget:]
}

// assertJSONExtractable confirms raw carries exactly one JSON verdict, via
// the same path the reviewer itself used to parse it, and that the result
// parses.
//
// copilot runs with --output-format text, so its raw output is the model's
// plain-text response; the review-spec prompt asks it to "Return ONLY a
// fenced ```json block", and ExtractJSON's fenced-block preference is what
// copilotReviewer.Review relies on to pull the verdict out — so this checks
// for exactly one such fence.
//
// claude runs with --output-format json --json-schema <schema>, so its raw
// output is the whole envelope: {"is_error":...,"result":...,
// "structured_output":{...}}. Constraining the model with --json-schema
// makes it hand back structured_output directly with no markdown fence at
// all — confirmed against a live run, where "result" was a bare JSON object,
// never a ```json block — so this mirrors claudeReviewer's own
// parseClaudeEnvelope unwrap instead of asserting a fence that will not be
// there.
func assertJSONExtractable(t *testing.T, name, raw string) {
	t.Helper()
	switch name {
	case "copilot":
		if n := strings.Count(raw, "```json"); n != 1 {
			t.Errorf("copilot raw output: want exactly one ```json fence, got %d", n)
		}
		b, err := backend.ExtractJSON(raw)
		if err != nil {
			t.Errorf("ExtractJSON(copilot raw): %v", err)
			return
		}
		if _, err = backend.ParseResult(b); err != nil {
			t.Errorf("ParseResult(copilot extracted JSON): %v", err)
		}
	case "claude":
		var env struct {
			IsError          bool            `json:"is_error"`
			Result           string          `json:"result"`
			StructuredOutput json.RawMessage `json:"structured_output"`
		}
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Errorf("claude raw output is not the expected envelope: %v", err)
			return
		}
		payload := []byte(env.StructuredOutput)
		if len(payload) == 0 || string(payload) == "null" {
			var err error
			if payload, err = backend.ExtractJSON(env.Result); err != nil {
				t.Errorf("no JSON verdict found in claude envelope result: %v", err)
				return
			}
		}
		if _, err := backend.ParseResult(payload); err != nil {
			t.Errorf("ParseResult(claude structured_output): %v", err)
		}
	default:
		t.Errorf("assertJSONExtractable: unknown backend %q", name)
	}
}

// TestLiveFallbackOrder proves backend.Select's ordering end to end: with
// copilot unreachable (a PATH holding nothing but a symlink to the real
// claude binary), Select must fall through to claude. It makes no model
// call, but Healthy's own probe shells out to run "claude --version" (see
// healthyBinary in run.go), so — per the Task 6 brief's own criterion, gate
// it on TAKT_LIVE=1 whenever it shells out — it is gated behind TAKT_LIVE
// like the smoke tests above rather than treated as hermetic. It mutates
// PATH via t.Setenv, which forbids t.Parallel().
func TestLiveFallbackOrder(t *testing.T) {
	skipUnlessLive(t)
	realClaude, err := exec.LookPath("claude")
	if err != nil {
		t.Skipf("claude not found on PATH: %v", err)
	}

	tmp := t.TempDir()
	if err = os.Symlink(realClaude, filepath.Join(tmp, "claude")); err != nil {
		t.Fatalf("symlink claude into a restricted PATH: %v", err)
	}
	t.Setenv("PATH", tmp)

	ctx, cancel := context.WithTimeout(context.Background(), healthCheckBudget)
	defer cancel()
	reg := backend.Registry(os.Getenv)
	r, err := backend.Select(ctx, []string{"copilot", "claude"}, reg)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if r.Name() != "claude" {
		t.Fatalf("Select fell through to %q, want claude", r.Name())
	}
}
