package backend

import "context"

// tailLen bounds how much of a failing CLI's stderr is quoted in an error.
const tailLen = 400

// copilotReviewer runs GitHub Copilot CLI headless as the cross-vendor
// reviewer.
type copilotReviewer struct{}

func (c *copilotReviewer) Name() string { return nameCopilot }

func (c *copilotReviewer) Healthy(ctx context.Context) error { return healthyBinary(ctx, nameCopilot) }

// copilotArgs builds the copilot invocation. Deliberately no --allow-* flags:
// in non-interactive mode any tool needing permission is denied, which makes
// the reviewer read-only by construction.
func copilotArgs(req ReviewRequest) []string {
	return []string{
		nameCopilot, "-p", req.Prompt, "--silent", "--output-format", "text",
		"--model", req.Model, "--effort", req.Effort, "-C", req.RepoRoot, "--add-dir", req.RepoRoot,
	}
}

func (c *copilotReviewer) Review(ctx context.Context, req ReviewRequest) (ReviewResult, error) {
	logPrompt(req.LogDir, req.LogID, req.Prompt)
	run := runCLI(ctx, req.RepoRoot, req.Timeout, req.LogDir, req.LogID, copilotArgs(req))
	if run.TimedOut {
		return errorResult(nameCopilot, req.Model, "timeout after "+req.Timeout.String(), run.Stdout, run.Elapsed), nil
	}
	if run.Err != nil {
		// A failed CLI run is reported as a VerdictError ReviewResult, not as
		// this method's own error return (see claude.go's identical contract).
		reason := run.Err.Error() + ": " + tail(run.Stderr)
		res := errorResult(nameCopilot, req.Model, reason, run.Stdout, run.Elapsed)
		return res, nil //nolint:nilerr // by design, see comment above
	}
	b, err := ExtractJSON(run.Stdout)
	if err != nil {
		return errorResult(nameCopilot, req.Model, err.Error(), run.Stdout, run.Elapsed), nil
	}
	r, err := ParseResult(b)
	if err != nil {
		return errorResult(nameCopilot, req.Model, err.Error(), run.Stdout, run.Elapsed), nil
	}
	r.Provider, r.Model, r.Raw, r.Elapsed = nameCopilot, req.Model, run.Stdout, run.Elapsed
	return r, nil
}

// tail returns the last tailLen bytes of s, prefixed with an ellipsis when
// truncated, for quoting in an error without dumping the whole stream.
func tail(s string) string {
	if len(s) <= tailLen {
		return s
	}
	return "…" + s[len(s)-tailLen:]
}
