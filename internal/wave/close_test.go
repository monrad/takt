package wave_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

func TestCloseRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if c, err := wave.ReadClose(dir, 0, 1); err != nil || c != nil {
		t.Fatalf("%v %v", c, err)
	}
	in := wave.CloseResult{
		Wave:      0,
		Slice:     1,
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
	out, err := wave.ReadClose(dir, 0, 1)
	if err != nil || out.Attempt != 2 || len(out.Tasks) != 2 || out.Tasks[1].Status != "rework" || !out.Committed {
		t.Fatalf("%+v %v", out, err)
	}
	if !strings.HasSuffix(wave.ClosePath(dir, 3, 2), "/waves/3/close.s2.json") {
		t.Fatal(wave.ClosePath(dir, 3, 2))
	}
}

// TestClosePathsPerSlice pins the per-slice record layout Task 8 introduced:
// one close record per slice of a wave, listed and picked by slice number
// rather than by the clock.
func TestClosePathsPerSlice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if p := wave.ClosePath(dir, 0, 1); !strings.HasSuffix(p, filepath.Join("waves", "0", "close.s1.json")) {
		t.Fatalf("%s", p)
	}
	if c, err := wave.LatestClose(dir, 0); err != nil || c != nil {
		t.Fatalf("no records → nil: %v %+v", err, c)
	}
	for _, s := range []int{1, 2} {
		if err := wave.WriteClose(dir, wave.CloseResult{Wave: 0, Slice: s, Attempt: 1, Committed: s == 1}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := wave.AllCloses(dir, 0)
	if err != nil || len(all) != 2 || all[0].Slice != 1 || all[1].Slice != 2 {
		t.Fatalf("%v %+v", err, all)
	}
	latest, _ := wave.LatestClose(dir, 0)
	if latest == nil || latest.Slice != 2 {
		t.Fatalf("%+v", latest)
	}
	if c, _ := wave.ReadClose(dir, 0, 3); c != nil {
		t.Fatal("missing slice → nil")
	}
	if werr := wave.WriteClose(dir, wave.CloseResult{Wave: 0, Attempt: 1}); werr == nil {
		t.Fatal("a record with no slice number has no path to be written to; it must be refused")
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
