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
