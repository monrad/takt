package backend

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// waitDelay mirrors gitx.WaitDelay without importing it: how long a CLI
// child (and anything holding its stdout) may outlive a cancelled context
// before it is killed.
const waitDelay = 5 * time.Second

// defaultTimeout applies when a ReviewRequest leaves Timeout unset. It
// mirrors config's defaultBackendTimeout (backends.<name>.timeout) without
// importing it, for the same reason waitDelay above mirrors gitx.WaitDelay;
// a test in internal/cli, which imports both packages, asserts the two are
// equal so they cannot drift.
const defaultTimeout = 15 * time.Minute

// healthCheckTimeout bounds a reviewer binary's `--version` probe.
const healthCheckTimeout = 10 * time.Second

// logFileMode is the permission for prompt/stdout/stderr log files: owner
// read-write only, since prompts and reviewer output may embed repo content.
const logFileMode = 0o600

// logDirMode is the permission for a created log directory.
const logDirMode = 0o750

// cliRun is the outcome of one headless CLI invocation.
type cliRun struct {
	Stdout   string
	Stderr   string
	Elapsed  time.Duration
	TimedOut bool
	Err      error
}

// runCLI runs argv in dir under timeout, logging stdout/stderr to
// <logDir>/<logID>.{stdout,stderr} when logDir and logID are both set.
func runCLI(ctx context.Context, dir string, timeout time.Duration, logDir, logID string, argv []string) cliRun {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout(timeout))
	defer cancel()

	//nolint:gosec // argv is takt's own reviewer invocation
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.WaitDelay = waitDelay
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb

	start := time.Now()
	err := cmd.Run()
	run := cliRun{Stdout: out.String(), Stderr: errb.String(), Elapsed: time.Since(start), Err: err}
	run.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)

	writeLog(logDir, logID, "stdout", out.Bytes())
	writeLog(logDir, logID, "stderr", errb.Bytes())
	return run
}

// resolveTimeout applies the package fallback: a deadline that is unset —
// or negative, which would be one already past — becomes defaultTimeout.
// Every backend call resolves its deadline here, so there is one place the
// fallback lives and one value a test can observe.
func resolveTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultTimeout
	}
	return d
}

// logPrompt stores the rendered prompt beside the outputs.
func logPrompt(logDir, logID, prompt string) {
	writeLog(logDir, logID, "prompt", []byte(prompt))
}

// writeLog writes b to <logDir>/<logID>.<ext> when both logDir and logID are
// set. Logging is best-effort diagnostics, not the review outcome, so
// failures here are intentionally swallowed rather than surfaced.
func writeLog(logDir, logID, ext string, b []byte) {
	if logDir == "" || logID == "" {
		return
	}
	_ = os.MkdirAll(logDir, logDirMode)
	_ = os.WriteFile(filepath.Join(logDir, logID+"."+ext), b, logFileMode)
}

// healthyBinary reports whether name is on PATH and runs successfully with
// --version, used by the copilot and claude reviewers' Healthy checks.
func healthyBinary(ctx context.Context, name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return err
	}
	run := runCLI(ctx, "", healthCheckTimeout, "", "", []string{name, "--version"})
	return run.Err
}
