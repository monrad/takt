package wave

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/monrad/takt/internal/gitx"
)

// TailLines is how much verify output a digest keeps.
const TailLines = 200

// VerifyResult is one verify command's outcome.
type VerifyResult struct {
	Command   string `json:"command"`
	Exit      int    `json:"exit"`
	Passed    bool   `json:"passed"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Tail      string `json:"tail"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// RunVerify runs each command with `bash -lc` from root under timeout.
func RunVerify(ctx context.Context, root string, cmds []string, timeout time.Duration) []VerifyResult {
	out := make([]VerifyResult, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, runOne(ctx, root, c, timeout))
	}
	return out
}

func runOne(ctx context.Context, root, command string, timeout time.Duration) VerifyResult {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bash", "-lc", command)
	cmd.Dir = root
	cmd.WaitDelay = gitx.WaitDelay
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	start := time.Now()
	err := cmd.Run()
	res := VerifyResult{Command: command, Tail: tail(buf.String()), ElapsedMS: time.Since(start).Milliseconds()}
	res.TimedOut = errors.Is(cctx.Err(), context.DeadlineExceeded)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.Passed = true
	case errors.As(err, &exitErr):
		res.Exit = exitErr.ExitCode()
	default:
		res.Exit = -1
		res.Tail += "\n" + err.Error()
	}
	if res.TimedOut {
		res.Passed = false
	}
	return res
}

func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > TailLines {
		lines = lines[len(lines)-TailLines:]
	}
	return strings.Join(lines, "\n")
}
