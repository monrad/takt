package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// claudeReviewer runs Claude Code headless as the fallback reviewer.
type claudeReviewer struct{}

func (c *claudeReviewer) Name() string { return nameClaude }

func (c *claudeReviewer) Healthy(ctx context.Context) error { return healthyBinary(ctx, nameClaude) }

// claudeArgs builds the claude invocation: dontAsk permission mode with a
// fixed read-only tool set, a JSON envelope constrained by ResultSchema, and
// no session persistence (spec §8).
func claudeArgs(req ReviewRequest) []string {
	return []string{
		nameClaude, "-p", req.Prompt, "--model", req.Model, "--effort", req.Effort,
		"--permission-mode", "dontAsk", "--allowedTools", "Read,Grep,Glob",
		"--output-format", "json", "--json-schema", ResultSchema, "--no-session-persistence",
	}
}

// claudeEnvelope is the `claude --output-format json` response shape. Only
// the fields this package acts on are decoded.
type claudeEnvelope struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

// parseClaudeEnvelope handles the `--output-format json` envelope: an error
// envelope (is_error: true) becomes VerdictError with the envelope's result
// text as the reason; a non-null structured_output is used when present;
// otherwise the result JSON is extracted from the result text.
func parseClaudeEnvelope(b []byte) (ReviewResult, error) {
	var env claudeEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		return ReviewResult{}, fmt.Errorf("backend: claude envelope: %w", err)
	}
	if env.IsError {
		return errorResult(nameClaude, "", env.Result, string(b), 0), nil
	}

	payload := []byte(env.StructuredOutput)
	if len(payload) == 0 || string(payload) == "null" {
		var err error
		if payload, err = ExtractJSON(env.Result); err != nil {
			return errorResult(nameClaude, "", err.Error(), env.Result, 0), nil
		}
	}

	r, err := ParseResult(payload)
	if err != nil {
		return errorResult(nameClaude, "", err.Error(), string(payload), 0), nil
	}
	return r, nil
}

func (c *claudeReviewer) Review(ctx context.Context, req ReviewRequest) (ReviewResult, error) {
	logPrompt(req.LogDir, req.LogID, req.Prompt)
	run := runCLI(ctx, req.RepoRoot, req.Timeout, req.LogDir, req.LogID, claudeArgs(req))
	if run.TimedOut {
		return errorResult(nameClaude, req.Model, "timeout after "+req.Timeout.String(), run.Stdout, run.Elapsed), nil
	}
	if run.Err != nil && run.Stdout == "" {
		return errorResult(nameClaude, req.Model, run.Err.Error()+": "+tail(run.Stderr), "", run.Elapsed), nil
	}
	r, err := parseClaudeEnvelope([]byte(run.Stdout))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return ReviewResult{}, err
		}
		return errorResult(nameClaude, req.Model, err.Error(), run.Stdout, run.Elapsed), nil
	}
	r.Provider, r.Model, r.Raw, r.Elapsed = nameClaude, req.Model, run.Stdout, run.Elapsed
	return r, nil
}
