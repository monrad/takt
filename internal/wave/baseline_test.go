package wave_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

func repo(t *testing.T) (string, *gitx.Repo) {
	t.Helper()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, "tracked.go", "v1\n")
	testutil.Commit(t, root, "tracked")
	r, err := gitx.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return root, r
}

func TestBaselineAndTouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, r := repo(t)
	testutil.WriteFile(t, root, "tracked.go", "user edit\n")  // user's dirt
	testutil.WriteFile(t, root, "scratch.txt", "user note\n") // user's untracked file
	base, err := wave.Baseline(ctx, r)
	if err != nil || len(base) != 2 {
		t.Fatalf("%v %+v", err, base)
	}
	for _, e := range base {
		if e.Hash == "" {
			t.Fatalf("hash missing for %s", e.Path)
		}
	}
	// Agent work: edits the dirty file further, adds a new file, deletes nothing.
	testutil.WriteFile(t, root, "tracked.go", "agent edit\n")
	testutil.WriteFile(t, root, "new.go", "package x\n")
	touched, err := wave.TouchedSince(ctx, r, base)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tp := range touched {
		got[tp.Path] = true
	}
	if !got["tracked.go"] || !got["new.go"] || got["scratch.txt"] {
		t.Fatalf("touched = %v (user's unchanged dirt must not count)", got)
	}
	os.Remove(filepath.Join(root, "scratch.txt"))
	touched, err = wave.TouchedSince(ctx, r, base)
	if err != nil {
		t.Fatal(err)
	}
	var foundDeletedScratch bool
	for _, tp := range touched {
		if tp.Path == "scratch.txt" {
			foundDeletedScratch = true
			if !tp.Deleted {
				t.Fatal("a removed baseline file is touched+deleted")
			}
		}
	}
	if !foundDeletedScratch {
		t.Fatal(
			"deleting an untracked baseline file must be reported: git status stops mentioning it entirely, so TouchedSince must cross-check baseline paths directly",
		)
	}
}

// TestTouchedSinceReportsRevertedBaselineFile covers the other half of the
// same gap: a baseline-dirty *tracked* file whose content the agent changes
// back to exactly what HEAD has. `git status` then shows it as clean (it
// drops out of dirtyPaths entirely), even though its content no longer
// matches what the baseline recorded — that is still a change to a
// baseline path and must be reported (not deleted, since the file exists).
func TestTouchedSinceReportsRevertedBaselineFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, r := repo(t)
	testutil.WriteFile(t, root, "tracked.go", "user edit\n") // user's dirt at baseline time
	base, err := wave.Baseline(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// Agent "fixes" the file by writing back exactly HEAD's content.
	testutil.WriteFile(t, root, "tracked.go", "v1\n")
	touched, err := wave.TouchedSince(ctx, r, base)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tp := range touched {
		if tp.Path == "tracked.go" {
			found = true
			if tp.Deleted {
				t.Fatal("the file still exists; it must not be reported as deleted")
			}
		}
	}
	if !found {
		t.Fatal("a baseline path whose content changed (even back to HEAD) must be reported as touched")
	}
}

// TestSaveBaselineIsAtomic pins what the shared atomic writer must leave
// behind: the record and nothing else. A temp file left in waves/<n>/ would
// be litter in a directory takt's own readers scan, and a half-written
// baseline would send the retry it exists for against the wrong tree.
func TestSaveBaselineIsAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	entries := []bundle.BaselineEntry{{Path: "a.go", Hash: "h"}}
	if err := wave.SaveBaseline(dir, 0, 1, entries); err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob(filepath.Join(dir, "waves", "0", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || !strings.HasSuffix(names[0], "baseline.json") {
		t.Fatalf("the write must leave the record and no temp file: %v", names)
	}
	got, slice, err := wave.ReadBaseline(dir, 0)
	if err != nil || slice != 1 || len(got) != 1 || got[0].Path != "a.go" {
		t.Fatalf("%v %d %+v", err, slice, got)
	}
}
