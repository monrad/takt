package gitx

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short name; "" when detached or bare
	Bare     bool
	Detached bool
}

// Worktrees lists every worktree of the repository; the first entry is the
// primary one (spec §18: "the entry marked as the main working tree").
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	var cur *Worktree
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wts = append(wts, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &wts[len(wts)-1]
		case cur == nil:
			continue
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Detached = true
		}
	}
	if len(wts) == 0 {
		return nil, errors.New("git worktree list returned nothing")
	}
	return wts, nil
}

// PrimaryWorktree is the main working tree.
func (r *Repo) PrimaryWorktree(ctx context.Context) (Worktree, error) {
	wts, err := r.Worktrees(ctx)
	if err != nil {
		return Worktree{}, err
	}
	return wts[0], nil
}

// BranchCheckedOut reports the worktree path holding branch, if any.
func (r *Repo) BranchCheckedOut(ctx context.Context, branch string) (string, bool, error) {
	wts, err := r.Worktrees(ctx)
	if err != nil {
		return "", false, err
	}
	for _, w := range wts {
		if w.Branch == branch {
			return w.Path, true, nil
		}
	}
	return "", false, nil
}

// IsCleanIn is true when dir has no modified, staged or untracked files.
func (r *Repo) IsCleanIn(ctx context.Context, dir string) (bool, error) {
	out, err := r.Run(ctx, "-C", dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// MergeNoFF merges branch into dir's checked-out branch with a merge commit
// and returns its sha (spec §7.5 merge disposition).
func (r *Repo) MergeNoFF(ctx context.Context, dir, branch, msg string) (string, error) {
	if _, err := r.Run(ctx, "-C", dir, "merge", "--no-ff", "--no-edit", "-m", msg, branch); err != nil {
		return "", err
	}
	out, err := r.Run(ctx, "-C", dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// MergeAbort undoes a merge that stopped on a conflict, restoring dir to
// the commit it was on. takt is not there to resolve a conflict — and the
// worktree the merge ran in is usually not the one the user is sitting in —
// so a merge it could not finish is taken back out rather than left staged.
func (r *Repo) MergeAbort(ctx context.Context, dir string) error {
	_, err := r.Run(ctx, "-C", dir, "merge", "--abort")
	return err
}

// DeleteBranchSafe is `git branch -d`: it refuses a branch that is not
// merged into HEAD, which is the check that makes deleting a run branch
// after a merge safe rather than merely quiet. git refuses either way when a
// worktree has the branch checked out.
func (r *Repo) DeleteBranchSafe(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "branch", "-d", name)
	return err
}

// DeleteBranchForce is `git branch -D`; git refuses when a worktree has it.
func (r *Repo) DeleteBranchForce(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "branch", "-D", name)
	return err
}

// DiffQuietExcluding is true when nothing outside excludeRel changed between
// from and to. An empty excludeRel compares the whole tree.
func (r *Repo) DiffQuietExcluding(ctx context.Context, from, to, excludeRel string) (bool, error) {
	args := []string{"diff", "--quiet", from, to, "--", "."}
	if excludeRel != "" {
		args = append(args, ":(exclude)"+excludeRel)
	}
	_, err := r.Run(ctx, args...)
	if err == nil {
		return true, nil
	}
	if e, ok := errors.AsType[*exec.ExitError](err); ok && e.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// DiffStat is `git diff --stat from to`, for the goal assessor's brief.
func (r *Repo) DiffStat(ctx context.Context, from, to string) (string, error) {
	return r.Run(ctx, "diff", "--stat", from, to)
}
