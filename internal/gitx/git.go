// Package gitx is a thin wrapper over the git CLI. Every call is
// -C-qualified to the repository root, so callers never depend on the
// process cwd (spec §4.5). No network operations live here (spec §13).
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNotRepo is returned by Open when cwd is not inside a git work tree.
var ErrNotRepo = errors.New("gitx: not inside a git repository")

// WaitDelay bounds how long a git child (and anything holding its stdout,
// such as a hook) may outlive a cancelled context before it is killed
// (spec §13). Shared with the verify runner.
const WaitDelay = 5 * time.Second

// Repo is a handle on one work tree (linked or primary).
type Repo struct {
	Root string
}

// Open resolves the work-tree root for cwd.
func Open(ctx context.Context, cwd string) (*Repo, error) {
	out, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRepo, cwd)
	}
	return &Repo{Root: out}, nil
}

// Run executes git with args in the repo root and returns trimmed stdout.
func (r *Repo) Run(ctx context.Context, args ...string) (string, error) {
	return runGit(ctx, r.Root, args...)
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	//nolint:gosec // G204: gitx's whole purpose is to run "git" with caller-supplied args; the binary name is fixed
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.WaitDelay = WaitDelay
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// CurrentBranch returns the checked-out branch name ("HEAD" when detached).
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBranch returns override if non-empty, else origin/HEAD's branch,
// else "main" if it exists, else "master".
func (r *Repo) DefaultBranch(ctx context.Context, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if ref, err := r.Run(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(ref, "origin/"), nil
	}
	for _, cand := range []string{"main", "master"} {
		if ok, _ := r.BranchExists(ctx, cand); ok {
			return cand, nil
		}
	}
	return "", errors.New("gitx: cannot determine the default branch; set default_branch in .takt.json")
}

// HeadSHA returns the full sha of HEAD.
func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "HEAD")
}

// MergeBase returns the merge base of two refs.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	return r.Run(ctx, "merge-base", a, b)
}

// BranchExists reports whether a local branch exists.
func (r *Repo) BranchExists(ctx context.Context, name string) (bool, error) {
	_, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// CreateAndCheckout creates a branch at HEAD and checks it out.
func (r *Repo) CreateAndCheckout(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "checkout", "-q", "-b", name)
	return err
}

// HasStaged reports whether the index differs from HEAD.
func (r *Repo) HasStaged(ctx context.Context) (bool, error) {
	_, err := r.Run(ctx, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

// HasStagedIn reports whether the index differs from HEAD *under the given
// paths only*. [Repo.HasStaged] answers for the whole index, which is the
// wrong question for takt: whatever the user staged themselves must neither
// make takt think it has work to commit nor be swept into takt's commit
// (spec §4.7). Called without paths it reports false: an empty pathspec
// would silently widen to the whole tree.
func (r *Repo) HasStagedIn(ctx context.Context, paths ...string) (bool, error) {
	if len(paths) == 0 {
		return false, nil
	}
	_, err := r.Run(ctx, append([]string{"diff", "--cached", "--quiet", "--"}, paths...)...)
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

// CommitPaths commits exactly the given paths and returns the new HEAD sha.
// Unlike [Repo.Commit] it never records the rest of the index, so a file the
// user staged for their own reasons stays staged and out of takt's history
// (spec §4.7). The paths must already be known to git — stage new files with
// [Repo.AddPathspec] first — and the caller must know there is something to
// commit ([Repo.HasStagedIn]); git exits non-zero on an empty commit.
//
// One caveat: a pathspec commit is git's "--only" mode, which builds a
// temporary index and therefore holds .git/index.lock for the whole run,
// hooks included — a plain commit does not. If the deadline kills it
// mid-hook, the stale lock blocks the next git command in that repository.
// Callers that must be able to clean up after their own killed commit use
// [Repo.Commit] instead (see commitInitBundle).
func (r *Repo) CommitPaths(ctx context.Context, msg string, paths ...string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("gitx: CommitPaths needs at least one path")
	}
	args := append([]string{"commit", "-q", "-m", msg, "--"}, paths...)
	if _, err := r.Run(ctx, args...); err != nil {
		return "", err
	}
	return r.HeadSHA(ctx)
}

// Porcelain returns the parsed `git status --porcelain=v1 -z` entries.
func (r *Repo) Porcelain(ctx context.Context) ([]Entry, error) {
	//nolint:gosec // G204: fixed "git status" invocation, no caller-supplied arguments
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	cmd.WaitDelay = WaitDelay
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	entries, err := ParsePorcelainZ(out)
	if err != nil {
		return nil, fmt.Errorf("gitx: %w", err)
	}
	return entries, nil
}

// Add stages exactly the given paths (never -A).
func (r *Repo) Add(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := r.Run(ctx, append([]string{"add", "--"}, paths...)...)
	return err
}

// Commit commits the index with msg and returns the new HEAD sha.
func (r *Repo) Commit(ctx context.Context, msg string) (string, error) {
	if _, err := r.Run(ctx, "commit", "-q", "-m", msg); err != nil {
		return "", err
	}
	return r.HeadSHA(ctx)
}

// Checkout switches to an already-existing local branch.
func (r *Repo) Checkout(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "checkout", "-q", name)
	return err
}

// DeleteBranch force-deletes a local branch. Used to roll back a run
// branch takt init created but then failed to finish initialising
// (spec D9).
func (r *Repo) DeleteBranch(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "branch", "-D", name)
	return err
}

// Unstage removes exactly the given paths from the index, leaving the files
// on disk untouched. It is the inverse of [Repo.Add] and is used to take an
// aborted takt init's own paths back out of the index (spec D9); paths not
// in the index are silently fine.
func (r *Repo) Unstage(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := r.Run(ctx, append([]string{"reset", "-q", "--"}, paths...)...)
	return err
}

// AddPathspec stages every change (modify, add, delete) under exactly the
// given paths: `git add -A -- <paths>`. Never called without a pathspec.
func (r *Repo) AddPathspec(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := r.Run(ctx, append([]string{"add", "-A", "--"}, paths...)...)
	return err
}

// RestorePaths discards working-tree changes to tracked paths.
func (r *Repo) RestorePaths(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := r.Run(ctx, append([]string{"checkout", "--"}, paths...)...)
	return err
}

// InHead reports whether path exists in the HEAD commit.
func (r *Repo) InHead(ctx context.Context, path string) (bool, error) {
	_, err := r.Run(ctx, "cat-file", "-e", "HEAD:"+path)
	if err == nil {
		return true, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return false, nil
	}
	return false, err
}
