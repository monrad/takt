package cli_test

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

// TestUnlockHintsAtTheBranchHoldingTheBundle pins that `takt unlock`
// inherits openTarget's bundle hint (#8): unlock is the command a stuck
// session reaches for, so failing it with a bare "no such file" is the worst
// moment to say nothing about where the bundle actually is.
func TestUnlockHintsAtTheBranchHoldingTheBundle(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 0 {
		t.Fatal(errb)
	}
	testutil.Git(t, root, "checkout", "main")
	code, _, errb := runIn(t, root, nil, "unlock", "--slug", "demo")
	if code != 1 || !strings.Contains(errb, "takt/demo") ||
		!strings.Contains(errb, "check it out, or pass --dir") {
		t.Fatalf("branch hint: %d %s", code, errb)
	}
}
