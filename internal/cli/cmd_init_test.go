package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/testutil"
)

// runIn runs takt with cwd=dir and a controlled environment.
func runIn(t *testing.T, dir string, env map[string]string, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		if k == "HOME" {
			return filepath.Join(dir, ".home")
		}
		return env[k]
	}
	code := cli.Main(args, &out, &errb, getenv, dir)
	var got map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON: %q", out.String())
		}
	}
	return code, got, errb.String()
}

func TestInitOnDefaultBranchCreatesRunBranch(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	code, got, errb := runIn(t, root, nil, "init", "Add", "a", "greeting")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["slug"] != "add-a-greeting" || got["branch"] != "takt/add-a-greeting" || got["branch_adopted"] != false ||
		got["base"] != "main" {
		t.Fatalf("out = %v", got)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "takt/add-a-greeting" {
		t.Fatalf("branch = %s", b)
	}
	st, err := bundle.LoadState(filepath.Join(root, "docs", "takt", "add-a-greeting"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseBrainstorm || st.Topic != "Add a greeting" || st.Config.MaxParallel != 8 ||
		st.Session == nil {
		t.Fatalf("state = %+v", st)
	}
	if got["committed"] != true {
		t.Fatal("in-repo bundle must be committed")
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(add-a-greeting): init" {
		t.Fatalf("commit message = %q", msg)
	}
	if clean := testutil.Git(t, root, "status", "--porcelain"); clean != "" {
		t.Fatalf("worktree not clean after init: %q", clean)
	}
}

func TestInitOnFeatureBranchAdopts(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.Git(t, root, "checkout", "-q", "-b", "monrad/2166")
	base := testutil.Git(t, root, "rev-parse", "HEAD")
	testutil.WriteFile(t, root, "f.txt", "x\n")
	testutil.Commit(t, root, "feature work")
	code, got, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["branch"] != "monrad/2166" || got["branch_adopted"] != true || got["base_sha"] != base {
		t.Fatalf("out = %v (base_sha must be the merge-base with main)", got)
	}
}

func TestInitRefusals(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, "staged.txt", "x\n")
	testutil.Git(t, root, "add", "staged.txt")
	if code, _, errb := runIn(
		t,
		root,
		nil,
		"init",
		"--slug",
		"demo",
		"t",
	); code != 1 ||
		!strings.Contains(errb, "staged") {
		t.Fatalf("staged changes must refuse: %d %s", code, errb)
	}
	testutil.Git(t, root, "reset", "-q")
	os.Remove(filepath.Join(root, "staged.txt"))
	if code, _, _ := runIn(t, root, nil, "init", "--slug", "demo", "t"); code != 0 {
		t.Fatal("first init should succeed")
	}
	if code, _, errb := runIn(
		t,
		root,
		nil,
		"init",
		"--slug",
		"demo",
		"t",
	); code != 1 ||
		!strings.Contains(errb, "exists") {
		t.Fatalf("existing slug must refuse: %d %s", code, errb)
	}
	if code, _, errb := runIn(
		t,
		t.TempDir(),
		nil,
		"init",
		"--slug",
		"x",
		"t",
	); code != 1 ||
		!strings.Contains(errb, "git repository") {
		t.Fatalf("outside a repo must refuse: %d %s", code, errb)
	}
}

func TestInitExternalDirIsNotCommitted(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	ext := t.TempDir()
	code, got, errb := runIn(t, root, map[string]string{"TAKT_DIR": ext}, "init", "--slug", "demo", "topic")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	want := filepath.Join(ext, filepath.Base(root), "demo")
	if got["bundle"] != want || got["committed"] != false {
		t.Fatalf("out = %v, want bundle %q uncommitted", got, want)
	}
	if n := testutil.Git(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("external bundle must not create a commit; count = %s", n)
	}
}

func TestInitFlagsFreezeConfig(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	code, _, errb := runIn(
		t,
		root,
		nil,
		"init",
		"--slug",
		"demo",
		"--autonomy",
		"step",
		"--no-review-tasks",
		"--no-goals",
		"topic",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	st, _ := bundle.LoadState(filepath.Join(root, "docs", "takt", "demo"))
	if st.Config.Autonomy != "step" || st.Config.Review.Tasks || st.Config.Goals || !st.Config.Review.Spec {
		t.Fatalf("frozen config = %+v", st.Config)
	}
}

// TestInitBadAutonomyFailsBeforeAnyGitMutation covers review finding 1: a
// bad --autonomy value must be caught before chooseBranch ever runs, so the
// repo stays on the default branch, no takt/<slug> branch is created, and a
// retry with a valid value is not mis-classified as an adopted branch.
func TestInitBadAutonomyFailsBeforeAnyGitMutation(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "--autonomy", "bogus", "topic")
	if code != 2 && code != 1 {
		t.Fatalf("exit = %d, want 1 or 2: %s", code, errb)
	}
	if !strings.Contains(errb, "autonomy") {
		t.Fatalf("stderr must mention autonomy: %s", errb)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Fatalf("must stay on the default branch: %s", b)
	}
	if list := testutil.Git(t, root, "branch", "--list", "takt/demo"); list != "" {
		t.Fatalf("takt/demo must not have been created: %q", list)
	}

	code, got, errb := runIn(t, root, nil, "init", "--slug", "demo", "--autonomy", "step", "topic")
	if code != 0 {
		t.Fatalf("valid retry must succeed: %d %s", code, errb)
	}
	if got["branch_adopted"] != false {
		t.Fatalf("retry out = %v, want a freshly created branch, not an adopted one", got)
	}
}

// TestInitAdoptedBranchMergeBaseFailureIsFatal covers review finding 2: when
// adopting a branch, a MergeBase failure (here, default_branch pointing at a
// ref that does not exist locally) must fail init outright rather than
// silently using HEAD as base_sha, and must leave no bundle directory
// behind.
func TestInitAdoptedBranchMergeBaseFailureIsFatal(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, ".takt.json", `{"default_branch":"nonexistent"}`)
	testutil.Commit(t, root, "add config")
	testutil.Git(t, root, "checkout", "-q", "-b", "monrad/2166")

	code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic")
	if code != 1 || !strings.Contains(errb, "merge-base") {
		t.Fatalf("must fail with a merge-base error: %d %s", code, errb)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "takt", "demo")); !os.IsNotExist(err) {
		t.Fatalf("bundle dir must not be created: err = %v", err)
	}
}

// TestInitRejectsInvalidSlug covers review finding 1: --slug becomes a
// directory name under the bundle root and a git branch name, so an
// unvalidated value escapes the bundle root (`../../escaped` writes
// state.json to <repo>/escaped and commits it) or commits a path takt
// cannot address (`My Feature`). Rejection is a usage error and must happen
// before any filesystem or git mutation.
func TestInitRejectsInvalidSlug(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	for _, bad := range []string{"../../escaped", "My Feature", "UPPER", "-lead", "a--b"} {
		code, _, errb := runIn(t, root, nil, "init", "--slug", bad, "topic")
		if code != 2 {
			t.Fatalf("--slug %q: exit %d, want 2: %s", bad, code, errb)
		}
		if !strings.Contains(errb, "slug") {
			t.Fatalf("--slug %q: stderr must mention slug: %s", bad, errb)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "takt")); !os.IsNotExist(err) {
		t.Fatalf("bundle root must not be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("--slug ../../escaped must not write outside the bundle root: %v", err)
	}
	if clean := testutil.Git(t, root, "status", "--porcelain"); clean != "" {
		t.Fatalf("tree not clean: %q", clean)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Fatalf("must stay on the default branch: %s", b)
	}
}

// TestInitAcceptsFlagsAfterTheTopic covers review finding 2: spec §5.1
// documents `takt init <topic…> [--slug s] [--autonomy …]`, so a flag after
// the topic must be parsed as a flag, not swallowed into the topic.
func TestInitAcceptsFlagsAfterTheTopic(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	code, got, errb := runIn(t, root, nil, "init", "Add greeting", "--autonomy", "step", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["slug"] != "demo" || got["branch"] != "takt/demo" {
		t.Fatalf("out = %v", got)
	}
	st, err := bundle.LoadState(filepath.Join(root, "docs", "takt", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Topic != "Add greeting" || st.Config.Autonomy != "step" {
		t.Fatalf("state = %+v", st)
	}
}

// TestInitTopicSurroundsFlags checks the third ordering: positional words on
// both sides of a flag join into one topic (review finding 2).
func TestInitTopicSurroundsFlags(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	code, _, errb := runIn(t, root, nil, "init", "Add", "--slug", "demo", "a", "greeting")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	st, err := bundle.LoadState(filepath.Join(root, "docs", "takt", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Topic != "Add a greeting" {
		t.Fatalf("topic = %q", st.Topic)
	}
}

// TestInitDoubleDashEndsFlagParsing checks that a literal -- makes the rest
// the topic, however it is spelled (review finding 2).
func TestInitDoubleDashEndsFlagParsing(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	code, got, errb := runIn(t, root, nil, "init", "--", "-weird topic")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	slug, ok := got["slug"].(string)
	if !ok || slug != "weird-topic" {
		t.Fatalf("slug = %v", got["slug"])
	}
	st, err := bundle.LoadState(filepath.Join(root, "docs", "takt", slug))
	if err != nil {
		t.Fatal(err)
	}
	if st.Topic != "-weird topic" {
		t.Fatalf("topic = %q", st.Topic)
	}
}

// TestInitRollsBackEverythingItWrote covers review finding 3: when the
// commit fails — here because signing is forced on with a gpg program that
// cannot be run — init must undo everything it did: the branch it created,
// the index entries it staged, and the bundle files it wrote. The tree must
// end exactly as it started, and a retry after fixing the cause must
// succeed rather than trip over "already exists" or "staged changes".
func TestInitRollsBackEverythingItWrote(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.Git(t, root, "config", "commit.gpgsign", "true")
	testutil.Git(t, root, "config", "gpg.program", filepath.Join(t.TempDir(), "no-such-gpg"))

	code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic")
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errb)
	}
	if clean := testutil.Git(t, root, "status", "--porcelain"); clean != "" {
		t.Fatalf("init left the tree dirty: %q", clean)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Fatalf("HEAD = %s, want main", b)
	}
	if list := testutil.Git(t, root, "branch", "--list", "takt/demo"); list != "" {
		t.Fatalf("takt/demo must be gone: %q", list)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "takt", "demo", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("bundle files must be removed: %v", err)
	}
	if strings.Contains(errb, "you are on branch") {
		t.Fatalf("the hint must not claim a failed checkout when the rollback worked: %s", errb)
	}

	testutil.Git(t, root, "config", "commit.gpgsign", "false")
	retry, got, rerr := runIn(t, root, nil, "init", "--slug", "demo", "topic")
	if retry != 0 {
		t.Fatalf("retry must succeed: %d %s", retry, rerr)
	}
	if got["branch"] != "takt/demo" || got["committed"] != true {
		t.Fatalf("retry out = %v", got)
	}
}

// TestInitKeepsPreExistingBundleFilesOnRollback pins the other half of
// review finding 3: rollback removes what init wrote, never a spec.md or
// anything else that was already sitting in the bundle directory.
func TestInitKeepsPreExistingBundleFilesOnRollback(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# drafted by hand\n")
	testutil.Git(t, root, "config", "commit.gpgsign", "true")
	testutil.Git(t, root, "config", "gpg.program", filepath.Join(t.TempDir(), "no-such-gpg"))

	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errb)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "takt", "demo", "spec.md")); err != nil {
		t.Fatalf("a pre-existing bundle file must survive rollback: %v", err)
	}
	for _, gone := range []string{"state.json", "events.jsonl"} {
		if _, err := os.Stat(filepath.Join(root, "docs", "takt", "demo", gone)); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed: %v", gone, err)
		}
	}
	if staged := testutil.Git(t, root, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("nothing may be left staged: %q", staged)
	}
}

// refuseDeleteHook is a reference-transaction hook that lets every ref
// update through except deleting refs/heads/takt/demo. It is how the test
// below reaches the one rollback branch a real failure would otherwise
// almost never produce.
const refuseDeleteHook = `#!/bin/sh
[ "$1" = prepared ] || exit 0
while read -r old new ref; do
  case "$new" in 0000000000000000000000000000000000000000)
    case "$ref" in refs/heads/takt/demo) echo "refusing to delete $ref" >&2; exit 1;; esac;;
  esac
done
exit 0
`

// TestInitHintNamesOnlyWhatIsLeftBehind covers the second half of review
// finding 3: when the checkout back to the default branch succeeded and only
// the branch deletion failed, the hint must say the branch was left behind —
// not tell the user they are still standing on it and to check out again.
func TestInitHintNamesOnlyWhatIsLeftBehind(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	hook := filepath.Join(root, ".git", "hooks", "reference-transaction")
	testutil.WriteFile(t, root, ".git/hooks/reference-transaction", refuseDeleteHook)
	if err := os.Chmod(hook, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "config", "commit.gpgsign", "true")
	testutil.Git(t, root, "config", "gpg.program", filepath.Join(t.TempDir(), "no-such-gpg"))

	code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic")
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errb)
	}
	if !strings.Contains(errb, "branch takt/demo was left behind") ||
		!strings.Contains(errb, "git branch -D takt/demo") {
		t.Fatalf("hint must name the surviving branch: %s", errb)
	}
	if strings.Contains(errb, "you are on branch") {
		t.Fatalf("the checkout succeeded, so the hint must not claim otherwise: %s", errb)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Fatalf("HEAD = %s, want main", b)
	}
	if clean := testutil.Git(t, root, "status", "--porcelain"); clean != "" {
		t.Fatalf("init left the tree dirty: %q", clean)
	}
}

func TestInitRollsBackEvenWhenTheDeadlineCausedTheFailure(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	// pre-commit sleeps past the (test-shortened) command deadline.
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	script := []byte("#!/bin/sh\nsleep 5\n")
	if err := os.WriteFile(hook, script, 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"TAKT_GIT_TIMEOUT": "1s"}
	code, _, errb := runIn(t, root, env, "init", "--slug", "demo", "topic")
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if b := testutil.Git(t, root, "branch", "--show-current"); b != "main" {
		t.Fatalf("rollback must return to main; on %q", b)
	}
	if st := testutil.Git(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("rollback must leave a clean tree; got %q", st)
	}
	if out := testutil.Git(t, root, "branch", "--list", "takt/demo"); out != "" {
		t.Fatalf("run branch must be deleted; got %q", out)
	}
}

// TestInitRefusesTheRepoRootAsBundleDir covers review finding I3: `--dir .`
// is refused, and refused before any branch or file exists, because takt
// cannot tell its own writes from the work tree when the two are the same
// directory.
func TestInitRefusesTheRepoRootAsBundleDir(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	code, _, errb := runIn(t, root, nil, "init", "--dir", ".", "Add a greeting")
	if code != 1 || !strings.Contains(errb, "repository root") {
		t.Fatalf("%d %s", code, errb)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Fatalf("refused before any branch is created, got %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, "state.json")); !os.IsNotExist(err) {
		t.Fatal("no bundle file may be written")
	}
	if st := testutil.Git(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("tree must be untouched: %q", st)
	}
}
