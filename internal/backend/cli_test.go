package backend_test

import (
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
)

func TestCopilotArgs(t *testing.T) {
	t.Parallel()
	args := backend.CopilotArgs(
		backend.ReviewRequest{Prompt: "P", RepoRoot: "/r", Model: "gpt-5.6-sol", Effort: "high"},
	)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p P", "--silent", "--output-format text", "--model gpt-5.6-sol", "--effort high", "-C /r", "--add-dir /r"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "--allow") {
		t.Errorf("no permission grants for a read-only reviewer: %q", joined)
	}
}

func TestClaudeArgsAndEnvelope(t *testing.T) {
	t.Parallel()
	args := backend.ClaudeArgs(backend.ReviewRequest{Prompt: "P", Model: "opus", Effort: "high"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p P", "--model opus", "--effort high", "--permission-mode dontAsk", "--allowedTools Read,Grep,Glob", "--output-format json", "--json-schema", "--no-session-persistence"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	// structured_output present
	r, err := backend.ParseClaudeEnvelope(
		[]byte(
			`{"type":"result","is_error":false,"result":"prose","structured_output":{"verdict":"approve","summary":"s"}}`,
		),
	)
	if err != nil || r.Verdict != "approve" {
		t.Fatalf("%+v %v", r, err)
	}
	// only result text with a fenced block
	r, err = backend.ParseClaudeEnvelope(
		[]byte(
			"{\"is_error\":false,\"result\":\"see\\n```json\\n{\\\"verdict\\\":\\\"rework\\\",\\\"summary\\\":\\\"x\\\"}\\n```\"}",
		),
	)
	if err != nil || r.Verdict != "rework" {
		t.Fatalf("%+v %v", r, err)
	}
	// error envelope
	r, err = backend.ParseClaudeEnvelope([]byte(`{"is_error":true,"result":"Not logged in"}`))
	if err != nil || r.Verdict != backend.VerdictError || !strings.Contains(r.Reason, "Not logged in") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRunCLITimeoutIsAResult(t *testing.T) {
	t.Parallel()
	run := backend.RunCLI(t.Context(), t.TempDir(), 300*time.Millisecond, t.TempDir(), "x", []string{"sleep", "5"})
	if !run.TimedOut || run.Elapsed > 6*time.Second {
		t.Fatalf("%+v", run)
	}
}
