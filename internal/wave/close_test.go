package wave_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

func TestCloseRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if c, err := wave.ReadClose(dir, 0); err != nil || c != nil {
		t.Fatalf("%v %v", c, err)
	}
	in := wave.CloseResult{
		Wave:      0,
		Attempt:   2,
		Committed: true,
		CommitSHA: "abc",
		ClosedAt:  time.Now(),
		Rework:    []int{2},
		Tasks: []wave.TaskResult{
			{Task: 1, Status: "done", FilesChanged: []string{"a.go"}},
			{Task: 2, Status: "rework", Reason: "review"},
		},
	}
	if err := wave.WriteClose(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := wave.ReadClose(dir, 0)
	if err != nil || out.Attempt != 2 || len(out.Tasks) != 2 || out.Tasks[1].Status != "rework" || !out.Committed {
		t.Fatalf("%+v %v", out, err)
	}
	if !strings.HasSuffix(wave.ClosePath(dir, 3), "/waves/3/close.json") {
		t.Fatal(wave.ClosePath(dir, 3))
	}
}

func TestCommitWaveStagesOnlyTaskFilesAndBundle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, r := repo(t)
	testutil.WriteFile(t, root, "user.txt", "mine\n")
	// Review finding 2: a file the user staged themselves must not be swept
	// into the wave commit.
	testutil.WriteFile(t, root, "user_staged.txt", "wip\n")
	testutil.Git(t, root, "add", "user_staged.txt")
	testutil.WriteFile(t, root, "a.go", "a\n")
	testutil.WriteFile(t, root, "docs/takt/demo/state.json", "{}\n")
	sha, err := wave.CommitWave(ctx, r, []string{"a.go"}, "docs/takt/demo", "takt(demo): wave 0 — tasks 1")
	if err != nil || len(sha) != 40 {
		t.Fatalf("%q %v", sha, err)
	}
	if st := testutil.Git(t, root, "status", "--porcelain"); st != "A  user_staged.txt\n?? user.txt" {
		t.Fatalf("user files must stay untracked / staged and uncommitted: %q", st)
	}
	if files := testutil.Git(
		t,
		root,
		"show",
		"--name-only",
		"--format=",
		"HEAD",
	); !strings.Contains(files, "a.go") ||
		!strings.Contains(files, "docs/takt/demo/state.json") ||
		strings.Contains(files, "user_staged.txt") {
		t.Fatalf("commit content: %q", files)
	}
}
