// Package testutil holds helpers shared by tests: temporary git repos with a
// known shape. Never imported by non-test code.
package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git runs git in dir and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec // G204: test helper always invokes "git" with test-controlled args
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null", // never read the developer's global config
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+dir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewRepo creates a temp repository on branch main with one commit.
func NewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "config", "user.name", "takt test")
	Git(t, dir, "config", "user.email", "takt@example.invalid")
	Git(t, dir, "config", "commit.gpgsign", "false")
	WriteFile(t, dir, "README.md", "# fixture\n")
	Git(t, dir, "add", "README.md")
	Git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

// WriteFile writes content to root/rel, creating parent directories.
func WriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Commit stages everything and commits; returns the new HEAD sha.
func Commit(t *testing.T, root, msg string) string {
	t.Helper()
	Git(t, root, "add", "-A")
	Git(t, root, "commit", "-q", "-m", msg)
	return Git(t, root, "rev-parse", "HEAD")
}
