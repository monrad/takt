// Package testutil holds helpers shared by tests: temporary git repos with a
// known shape. Never imported by non-test code.
package testutil

import (
	"context"
	"fmt"
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
	return NewRepoAt(t, t.TempDir())
}

// NewRepoAt is [NewRepo] in a directory the caller names — for a test that
// needs the repository at a particular path, a path with a space in it say.
// The directory is created when it does not exist.
func NewRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
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

// RunHermetic runs m with a git environment scrubbed of the developer's own
// configuration and returns the exit code TestMain must pass to [os.Exit].
// gitx inherits [os.Environ], so without this a global core.excludesFile
// that ignores docs/ would make takt init fail inside the test suite, and a
// global core.hooksPath would run the developer's hooks. Setting the
// environment once, before any test starts, is compatible with t.Parallel.
func RunHermetic(m *testing.M) int {
	home, err := os.MkdirTemp("", "takt-test-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil: "+err.Error())
		return 1
	}
	defer func() { _ = os.RemoveAll(home) }()
	env := map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"HOME":                home,
		// git runs `git maintenance run --auto --detach` after a commit.
		// The detached child outlives the git call takt waited for, so it is
		// still walking the object store when t.TempDir cleans the repo up —
		// which surfaces as a rare "unlinkat …/.git: directory not empty"
		// failure under -race -count=N, in whichever test happened to be
		// unlucky. Both auto-maintenance paths are turned off through
		// GIT_CONFIG_COUNT rather than a config file, so a repo made with
		// plain `git init` in any test inherits them.
		"GIT_CONFIG_COUNT":   "2",
		"GIT_CONFIG_KEY_0":   "maintenance.autoDetach",
		"GIT_CONFIG_VALUE_0": "false",
		"GIT_CONFIG_KEY_1":   "gc.auto",
		"GIT_CONFIG_VALUE_1": "0",
	}
	for k, v := range env {
		if serr := os.Setenv(k, v); serr != nil {
			fmt.Fprintln(os.Stderr, "testutil: "+serr.Error())
			return 1
		}
	}
	return m.Run()
}
