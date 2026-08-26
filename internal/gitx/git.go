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
	"path/filepath"
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

// CommitExists reports whether rev names a commit object in this repository.
// A sha a record claims but that was never written — a crash inside `git
// commit` — resolves to nothing, which is what lets a caller tell a claimed
// commit from a real one (spec §5.4).
func (r *Repo) CommitExists(ctx context.Context, rev string) (bool, error) {
	_, err := r.Run(ctx, "cat-file", "-e", rev+"^{commit}")
	if err == nil {
		return true, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return false, nil
	}
	return false, err
}

// IsAncestor reports whether commit a is an ancestor of b (a commit is its
// own ancestor). `git merge-base --is-ancestor` says so with exit 0, denies
// it with exit 1 and fails otherwise, so an exit 1 is an answer rather than
// an error.
func (r *Repo) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	_, err := r.Run(ctx, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if e, ok := errors.AsType[*exec.ExitError](err); ok && e.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// CommonDir returns the repository's *common* git directory — the one every
// worktree of the repository shares, and where `info/exclude` lives. A
// linked worktree has a git dir of its own, so `--git-dir` would answer with
// a per-worktree path whose `info/` git never reads.
//
// git answers relative to the directory it ran in, and gitx runs every
// command with -C <root>, so a relative answer is joined onto the root
// rather than onto the caller's cwd (spec §4.5).
func (r *Repo) CommonDir(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(out) {
		return out, nil
	}
	return filepath.Join(r.Root, out), nil
}

// EnsureExclude records ignore rules in the repository's `info/exclude`:
// the ignore list that lives in the common git dir, is honoured by every
// worktree whatever branch it has checked out, and is never cloned. That is
// the only place an ignore rule for a *branch-specific* directory can
// survive a branch switch — a tracked .gitignore inside that directory goes
// away with the branch that carries it.
//
// It is the user's file too, so nothing already in it is disturbed: a rule
// is appended only when it is not already there (whitespace-insensitively),
// existing bytes are preserved, and an append always starts on a line of its
// own. A missing info/ directory or exclude file is created. Rules are
// appended in the order given, which is how a caller passing a pattern and
// its negation gets gitignore's last-match-wins the right way round; a file
// that already holds only the negation is the one state that cannot be
// repaired by appending, and no caller writes one.
//
// A rule is written exactly as given: this is a gitignore pattern, not a
// literal path, so escaping it is the caller's business. A path holding a
// glob metacharacter (`*`, `?`, `[`) would match more than itself, and one
// opening with `#` or `!` would be read as a comment or a negation. takt's
// only caller — excludeLogsDir in internal/cli — builds the pattern from a
// bundle slug, which bundle.ValidSlug holds to a-z, 0-9 and `-`, so the only
// way to reach any of them is a `--dir` naming such a directory.
func (r *Repo) EnsureExclude(ctx context.Context, lines ...string) error {
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			return errors.New("gitx: EnsureExclude needs a non-empty rule")
		}
	}
	common, err := r.CommonDir(ctx)
	if err != nil {
		return err
	}
	path := filepath.Join(common, "info", "exclude")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var add strings.Builder
	if len(old) > 0 && !strings.HasSuffix(string(old), "\n") {
		add.WriteString("\n")
	}
	for _, line := range lines {
		if !excludeHas(string(old)+add.String(), line) {
			add.WriteString(line + "\n")
		}
	}
	if add.Len() == 0 || strings.TrimSpace(add.String()) == "" {
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(add.String()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// excludeHas reports whether content already carries rule as a line of its
// own, ignoring surrounding whitespace.
func excludeHas(content, rule string) bool {
	for existing := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(existing) == rule {
			return true
		}
	}
	return false
}
