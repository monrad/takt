package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestUnstage covers review finding 3's new primitive: a failed init must be
// able to take its own paths back out of the index without touching the
// files or anything else that was staged.
func TestUnstage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, err := gitx.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/state.json", "{}\n")
	if aerr := r.Add(ctx, "docs/takt/demo"); aerr != nil {
		t.Fatal(aerr)
	}
	if staged, serr := r.HasStaged(ctx); serr != nil || !staged {
		t.Fatalf("precondition: staged = %v, err = %v", staged, serr)
	}
	if uerr := r.Unstage(ctx, "docs/takt/demo"); uerr != nil {
		t.Fatal(uerr)
	}
	if staged, serr := r.HasStaged(ctx); serr != nil || staged {
		t.Fatalf("Unstage left the index dirty: staged = %v, err = %v", staged, serr)
	}
	if _, serr := os.Stat(filepath.Join(root, "docs", "takt", "demo", "state.json")); serr != nil {
		t.Fatalf("Unstage must not delete the file: %v", serr)
	}
	if uerr := r.Unstage(ctx); uerr != nil {
		t.Fatalf("Unstage with no paths must be a no-op: %v", uerr)
	}
}

func TestRunGitDeadlineKillsHookHoldingStdout(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	// A pre-commit hook that outlives any reasonable deadline while holding git's stdout.
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	script := []byte("#!/bin/sh\nsleep 30\n")
	if err := os.WriteFile(hook, script, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := gitx.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "x.txt", "x\n")
	if err = r.Add(context.Background(), "x.txt"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = r.Commit(ctx, "hung")
	if err == nil {
		t.Fatal("commit must fail under the deadline")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond+gitx.WaitDelay+2*time.Second {
		t.Fatalf("commit returned after %v; the hook held git past the deadline", elapsed)
	}
}

func TestAddPathspecRestoreAndInHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, _ := gitx.Open(ctx, root)
	testutil.WriteFile(t, root, "keep.txt", "k\n")
	testutil.WriteFile(t, root, "a.go", "a\n")
	if err := r.AddPathspec(ctx, "a.go"); err != nil {
		t.Fatal(err)
	}
	if st := testutil.Git(
		t,
		root,
		"status",
		"--porcelain",
	); !strings.Contains(st, "A  a.go") ||
		!strings.Contains(st, "?? keep.txt") {
		t.Fatalf("only a.go staged: %q", st)
	}
	if _, err := r.Commit(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.InHead(ctx, "a.go"); !ok {
		t.Fatal("a.go must be in HEAD")
	}
	if ok, _ := r.InHead(ctx, "keep.txt"); ok {
		t.Fatal("keep.txt must not be in HEAD")
	}
	testutil.WriteFile(t, root, "a.go", "changed\n")
	if err := r.RestorePaths(ctx, "a.go"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.go")); string(b) != "a\n" {
		t.Fatalf("restore: %q", b)
	}
	os.Remove(filepath.Join(root, "a.go"))
	if err := r.AddPathspec(ctx, "a.go"); err != nil {
		t.Fatal(err)
	}
	if st := testutil.Git(t, root, "status", "--porcelain"); !strings.Contains(st, "D  a.go") {
		t.Fatalf("deletion must be staged: %q", st)
	}
}
