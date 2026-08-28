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

// TestCommitPathsAndHasStagedInAreScoped covers review finding 2: takt must
// commit exactly the paths it owns. A user's unrelated staged file has to
// stay staged and out of the commit, and HasStagedIn must not see it.
func TestCommitPathsAndHasStagedInAreScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, _ := gitx.Open(ctx, root)

	testutil.WriteFile(t, root, "user_wip.txt", "mine\n")
	testutil.Git(t, root, "add", "user_wip.txt")
	if staged, err := r.HasStagedIn(ctx, "bundle"); err != nil || staged {
		t.Fatalf("HasStagedIn must ignore paths outside the pathspec: %v %v", staged, err)
	}

	testutil.WriteFile(t, root, "bundle/state.json", "{}\n")
	if err := r.AddPathspec(ctx, "bundle"); err != nil {
		t.Fatal(err)
	}
	staged, err := r.HasStagedIn(ctx, "bundle")
	if err != nil || !staged {
		t.Fatalf("HasStagedIn = %v, %v; want true", staged, err)
	}
	sha, err := r.CommitPaths(ctx, "takt(demo): scoped", "bundle")
	if err != nil || len(sha) != 40 {
		t.Fatalf("CommitPaths = %q, %v", sha, err)
	}
	files := testutil.Git(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "bundle/state.json") || strings.Contains(files, "user_wip.txt") {
		t.Fatalf("commit content = %q", files)
	}
	if st := testutil.Git(t, root, "status", "--porcelain"); st != "A  user_wip.txt" {
		t.Fatalf("the user's staged file must survive untouched: %q", st)
	}
	if staged, err = r.HasStagedIn(ctx, "bundle"); err != nil || staged {
		t.Fatalf("nothing left staged under the pathspec: %v %v", staged, err)
	}
}

// excludeOf reads the repository's info/exclude and fails the test unless it
// is exactly want. Kept as a helper so the tests below read as a sequence of
// states rather than a pile of branches (gocognit).
func excludeOf(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("info/exclude = %q, want %q", b, want)
	}
}

// excludePath is the repository's info/exclude, with any file git's
// templates shipped removed — so the create path is the one under test.
func excludePath(t *testing.T, r *gitx.Repo, root string) string {
	t.Helper()
	common, err := r.CommonDir(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if common != filepath.Join(root, ".git") {
		t.Fatalf("CommonDir = %q, want %q", common, filepath.Join(root, ".git"))
	}
	if err = os.RemoveAll(filepath.Join(common, "info")); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(common, "info", "exclude")
}

// TestEnsureExcludeCreatesTheFileAndAppendsOnce pins the ignore list takt
// writes the bundle's untracked area into: it lives in the common git dir,
// so every worktree honours it whatever branch is checked out, and it is the
// user's file too — nothing already in it may be disturbed.
func TestEnsureExcludeCreatesTheFileAndAppendsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, err := gitx.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	excl := excludePath(t, r, root)
	if err = r.EnsureExclude(ctx, "/docs/takt/demo/logs/*"); err != nil {
		t.Fatal(err)
	}
	excludeOf(t, excl, "/docs/takt/demo/logs/*\n")

	// The user's own content survives, and a rule already present is never
	// appended a second time.
	const mine = "# mine\nscratch/\n/docs/takt/demo/logs/*\n"
	if err = os.WriteFile(excl, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = r.EnsureExclude(ctx, "/docs/takt/demo/logs/*"); err != nil {
			t.Fatal(err)
		}
	}
	excludeOf(t, excl, mine)
	if err = r.EnsureExclude(ctx, "/docs/takt/other/logs/*"); err != nil {
		t.Fatal(err)
	}
	excludeOf(t, excl, mine+"/docs/takt/other/logs/*\n")
}

// TestEnsureExcludeKeepsRuleOrderAndLineBoundaries covers the two things a
// pattern-plus-negation caller depends on: the rules land in the order given
// (gitignore is last-match-wins), and an append never glues itself onto a
// last line the user left without a newline.
func TestEnsureExcludeKeepsRuleOrderAndLineBoundaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, err := gitx.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	excl := excludePath(t, r, root)
	pair := []string{"/docs/takt/p/logs/*", "!/docs/takt/p/logs/.gitignore"}
	want := strings.Join(pair, "\n") + "\n"
	for range 2 {
		if err = r.EnsureExclude(ctx, pair...); err != nil {
			t.Fatal(err)
		}
		excludeOf(t, excl, want)
	}
	if err = os.WriteFile(excl, []byte("noeol"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = r.EnsureExclude(ctx, "/x/"); err != nil {
		t.Fatal(err)
	}
	excludeOf(t, excl, "noeol\n/x/\n")
	// An empty rule is a bug in the caller, not a no-op.
	if err = r.EnsureExclude(ctx, "  "); err == nil {
		t.Fatal("an empty rule must be refused")
	}
}

// TestEscapeIgnorePatternEscapesTheBackslashFirst pins the escaper
// EnsureExclude's callers compose their rules with. The backslash case is
// the one that decides the order: `\` is gitignore's own escape character
// and a legal character in a Unix directory name, so a name carrying one
// must come out as a doubled backslash and never as an escape for whatever
// follows it.
func TestEscapeIgnorePatternEscapesTheBackslashFirst(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"an ordinary path is untouched", "docs/takt/demo", "docs/takt/demo"},
		{"a bracket would open a character class", "docs/[takt]/demo", `docs/\[takt]/demo`},
		{"a star and a question mark are wildcards", "docs/a*b?c/demo", `docs/a\*b\?c/demo`},
		{"a literal backslash is doubled", `docs/ta\kt/demo`, `docs/ta\\kt/demo`},
		{"a backslash before a metacharacter is not an escape", `docs/a\*b`, `docs/a\\\*b`},
		{"a leading hash would be a comment", "#notes/demo", `\#notes/demo`},
		{"a leading bang would be a negation", "!notes/demo", `\!notes/demo`},
		{"a hash or bang further in is literal already", "docs/#a!b/demo", "docs/#a!b/demo"},
		{"git strips an unescaped trailing space", "docs/demo ", `docs/demo\ `},
		{"an inner space needs nothing", "docs/de mo", "docs/de mo"},
		{"nothing to escape in nothing", "", ""},
	} {
		if got := gitx.EscapeIgnorePattern(tc.in); got != tc.want {
			t.Errorf("%s: EscapeIgnorePattern(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestEnsureExcludeHonoursAnEscapedPatternInGit is the escaper's other half:
// git itself must read the escaped rule as the directory it names. A
// backslash-bearing directory is the case a test can only settle by asking
// git, since the escape and the character are the same byte.
func TestEnsureExcludeHonoursAnEscapedPatternInGit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, err := gitx.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	const dir = `docs/ta\kt/demo`
	if err = os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir), "logs"), 0o750); err != nil {
		t.Fatal(err)
	}
	esc := gitx.EscapeIgnorePattern(dir)
	if err = r.EnsureExclude(ctx, "/"+esc+"/logs/*", "!/"+esc+"/logs/.gitignore"); err != nil {
		t.Fatal(err)
	}
	// check-ignore exits 0 for an ignored path and 1 for one that is not,
	// so the two calls are the assertion: the payload is hidden and the
	// ignore file is re-included.
	if _, err = r.Run(ctx, "check-ignore", "-q", dir+"/logs/session.json"); err != nil {
		t.Fatalf("the escaped rule must ignore the log payload: %v", err)
	}
	if _, err = r.Run(ctx, "check-ignore", "-q", dir+"/logs/.gitignore"); err == nil {
		t.Fatal("the negation must keep the ignore file itself visible")
	}
}
