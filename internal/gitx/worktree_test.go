package gitx_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/testutil"
)

func TestWorktreesAndMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t) // main with one commit
	testutil.Git(t, root, "branch", "takt/demo")
	linked := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, root, "worktree", "add", linked, "takt/demo")
	testutil.WriteFile(t, linked, "feature.txt", "x\n")
	testutil.Commit(t, linked, "feature")
	repo, err := gitx.Open(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	wts, err := repo.Worktrees(ctx)
	if err != nil || len(wts) != 2 || wts[0].Branch != "main" || wts[1].Branch != "takt/demo" {
		t.Fatalf("%v %+v", err, wts)
	}
	prim, err := repo.PrimaryWorktree(ctx)
	if err != nil || prim.Branch != "main" {
		t.Fatalf("%v %+v", err, prim)
	}
	if p, ok, coErr := repo.BranchCheckedOut(ctx, "takt/demo"); coErr != nil || !ok || p != wts[1].Path {
		t.Fatalf("%v %v %v", p, ok, coErr)
	}
	if _, ok, _ := repo.BranchCheckedOut(ctx, "nope"); ok {
		t.Fatal("unknown branch is not checked out")
	}
	clean, err := repo.IsCleanIn(ctx, prim.Path)
	if err != nil || !clean {
		t.Fatalf("primary must be clean: %v %v", clean, err)
	}
	testutil.WriteFile(t, prim.Path, "dirt.txt", "d\n")
	if clean, _ = repo.IsCleanIn(ctx, prim.Path); clean {
		t.Fatal("untracked file makes the primary dirty")
	}
	sha, err := repo.MergeNoFF(ctx, prim.Path, "takt/demo", "Merge takt/demo")
	if err != nil || sha == "" {
		t.Fatalf("%v %q", err, sha)
	}
	if got := testutil.Git(t, prim.Path, "log", "-1", "--format=%s"); got != "Merge takt/demo" {
		t.Fatalf("merge commit subject %q", got)
	}
	if err = repo.DeleteBranchForce(ctx, "takt/demo"); err == nil {
		t.Fatal("a branch checked out in a worktree cannot be deleted")
	}
}

func TestDiffQuietExcluding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	base := testutil.Git(t, root, "rev-parse", "HEAD")
	testutil.WriteFile(t, root, "docs/takt/demo/state.json", "{}\n")
	testutil.Commit(t, root, "bundle only")
	repo, _ := gitx.Open(ctx, root)
	if q, err := repo.DiffQuietExcluding(ctx, base, "HEAD", "docs/takt/demo"); err != nil || !q {
		t.Fatalf("bundle-only commit is quiet outside the bundle: %v %v", q, err)
	}
	if q, _ := repo.DiffQuietExcluding(ctx, base, "HEAD", ""); q {
		t.Fatal("without an exclude the diff is not quiet")
	}
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.Commit(t, root, "code")
	if q, _ := repo.DiffQuietExcluding(ctx, base, "HEAD", "docs/takt/demo"); q {
		t.Fatal("a code commit is not quiet")
	}
	if st, err := repo.DiffStat(ctx, base, "HEAD"); err != nil || st == "" {
		t.Fatalf("%v %q", err, st)
	}
}
