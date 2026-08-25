package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/testutil"
)

func TestOpenFindsToplevelFromSubdir(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := gitx.Open(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root != root {
		t.Fatalf("Root = %q, want %q", r.Root, root)
	}
}

func TestOpenOutsideRepo(t *testing.T) {
	t.Parallel()
	_, err := gitx.Open(context.Background(), t.TempDir())
	if !errors.Is(err, gitx.ErrNotRepo) {
		t.Fatalf("err = %v, want ErrNotRepo", err)
	}
}

func TestBranchesAndCommits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, _ := gitx.Open(ctx, root)

	if b, _ := r.CurrentBranch(ctx); b != "main" {
		t.Fatalf("CurrentBranch = %q", b)
	}
	if d, _ := r.DefaultBranch(ctx, ""); d != "main" {
		t.Fatalf("DefaultBranch = %q", d)
	}
	if d, _ := r.DefaultBranch(ctx, "trunk"); d != "trunk" {
		t.Fatalf("DefaultBranch override = %q", d)
	}
	base, _ := r.HeadSHA(ctx)

	if err := r.CreateAndCheckout(ctx, "takt/demo"); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.CurrentBranch(ctx); b != "takt/demo" {
		t.Fatalf("after checkout CurrentBranch = %q", b)
	}
	if ok, _ := r.BranchExists(ctx, "takt/demo"); !ok {
		t.Fatal("BranchExists false after create")
	}
	if ok, _ := r.BranchExists(ctx, "nope"); ok {
		t.Fatal("BranchExists true for missing branch")
	}

	testutil.WriteFile(t, root, "x.txt", "x\n")
	if staged, _ := r.HasStaged(ctx); staged {
		t.Fatal("HasStaged true with only an untracked file")
	}
	if err := r.Add(ctx, "x.txt"); err != nil {
		t.Fatal(err)
	}
	if staged, _ := r.HasStaged(ctx); !staged {
		t.Fatal("HasStaged false after add")
	}
	sha, err := r.Commit(ctx, "takt(demo): wave 0")
	if err != nil || len(sha) != 40 {
		t.Fatalf("Commit = %q, %v", sha, err)
	}
	if mb, _ := r.MergeBase(ctx, "main", "takt/demo"); mb != base {
		t.Fatalf("MergeBase = %q, want %q", mb, base)
	}
}

func TestPorcelainRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, _ := gitx.Open(ctx, root)
	testutil.WriteFile(t, root, "README.md", "changed\n")
	testutil.WriteFile(t, root, "dir/new.go", "package dir\n")
	entries, err := r.Porcelain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]gitx.Entry{}
	for _, e := range entries {
		got[e.Path] = e
	}
	if e := got["README.md"]; e.Y != 'M' {
		t.Fatalf("README.md = %+v", e)
	}
	if e := got["dir/new.go"]; e.X != '?' {
		t.Fatalf("dir/new.go = %+v", e)
	}
}

func TestRunErrorIncludesStderr(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := gitx.Open(ctx, testutil.NewRepo(t))
	_, err := r.Run(ctx, "rev-parse", "--verify", "definitely-not-a-ref")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); len(got) < 10 {
		t.Fatalf("error too terse: %q", got)
	}
}

func TestCheckoutAndDeleteBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, _ := gitx.Open(ctx, root)

	if err := r.CreateAndCheckout(ctx, "takt/tmp"); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.CurrentBranch(ctx); b != "main" {
		t.Fatalf("CurrentBranch after Checkout = %q", b)
	}
	if err := r.DeleteBranch(ctx, "takt/tmp"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.BranchExists(ctx, "takt/tmp"); ok {
		t.Fatal("BranchExists true after DeleteBranch")
	}
}
