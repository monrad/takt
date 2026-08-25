package wave_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

func TestVerifyScopeAndRevert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, r := repo(t)
	base, _ := wave.Baseline(ctx, r)
	testutil.WriteFile(t, root, "tracked.go", "agent\n")        // in scope of task 1
	testutil.WriteFile(t, root, "b.go", "b\n")                  // in scope of task 2
	testutil.WriteFile(t, root, "README.md", "agent strayed\n") // tracked, out of scope
	testutil.WriteFile(t, root, "stray.txt", "x\n")             // untracked, out of scope
	touched, _ := wave.TouchedSince(ctx, r, base)
	sc := wave.VerifyScope(touched, map[int][]string{1: {"tracked.go"}, 2: {"b.go"}})
	if len(sc.PerTask[1]) != 1 || len(sc.PerTask[2]) != 1 || len(sc.OutOfScope) != 2 {
		t.Fatalf("%+v", sc)
	}
	reverted, err := wave.Revert(ctx, r, sc.OutOfScope)
	if err != nil || len(reverted) != 2 {
		t.Fatalf("%v %v", reverted, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "README.md")); string(b) != "# fixture\n" {
		t.Fatalf("tracked stray must be restored: %q", b)
	}
	if _, serr := os.Stat(filepath.Join(root, "stray.txt")); !os.IsNotExist(serr) {
		t.Fatal("untracked stray must be deleted")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "b.go")); string(b) != "b\n" {
		t.Fatal("in-scope work must survive")
	}
}

// TestRevertSkipsUntrackedDeletion covers the fix for the gap where an
// out-of-scope path is an untracked baseline file the agent deleted:
// there is nothing to restore, so Revert must return without error and
// must not count it in the reverted slice (it stays visible only via the
// Scope.OutOfScope the caller already has, and from there in close.json).
func TestRevertSkipsUntrackedDeletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, r := repo(t)
	testutil.WriteFile(t, root, "stray.txt", "x\n") // user's untracked dirt at baseline time
	base, _ := wave.Baseline(ctx, r)
	os.Remove(filepath.Join(root, "stray.txt")) // agent deletes it
	touched, err := wave.TouchedSince(ctx, r, base)
	if err != nil {
		t.Fatal(err)
	}
	sc := wave.VerifyScope(touched, map[int][]string{})
	if len(sc.OutOfScope) != 1 || sc.OutOfScope[0].Path != "stray.txt" || !sc.OutOfScope[0].Deleted {
		t.Fatalf("%+v", sc)
	}
	reverted, err := wave.Revert(ctx, r, sc.OutOfScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(reverted) != 0 {
		t.Fatalf("an untracked deletion has nothing to restore and must not be counted as reverted: %v", reverted)
	}
}

func TestResetForRecoveryKeepsUntouchedUserDirt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, r := repo(t)
	testutil.WriteFile(t, root, "tracked.go", "user dirt\n")
	base, _ := wave.Baseline(ctx, r)
	testutil.WriteFile(t, root, "b.go", "agent new\n")
	reset, err := wave.ResetForRecovery(ctx, r, []string{"tracked.go", "b.go", "absent.go"}, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(reset) != 1 || reset[0] != "b.go" {
		t.Fatalf("only the agent's file is reset: %v", reset)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "tracked.go")); string(b) != "user dirt\n" {
		t.Fatal("user dirt untouched since baseline must survive")
	}
	testutil.WriteFile(t, root, "tracked.go", "agent overwrote\n")
	reset, _ = wave.ResetForRecovery(ctx, r, []string{"tracked.go"}, base)
	if len(reset) != 1 {
		t.Fatalf("a changed tracked file is reset to HEAD: %v", reset)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "tracked.go")); string(b) != "v1\n" {
		t.Fatalf("reset restores HEAD (user dirt is lost — documented): %q", b)
	}
}
