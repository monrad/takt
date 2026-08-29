package cli

import (
	"context"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/testutil"
)

// These are internal tests (package cli, like slug_test.go): both drive
// applyDisposition directly, because a git read that fails is reachable from
// the command line only by racing the process that would have to fail.
//
// prPushX is the hand-off both tests below expect: the plain push, which is
// what a git read that cannot answer degrades to.
const prPushX = "git push origin takt/x"

// prWorkspace opens a workspace over root holding nothing but the repository
// — the pr disposition reads git and reads nothing else.
func prWorkspace(t *testing.T, root string) *workspace {
	t.Helper()
	repo, err := gitx.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	return &workspace{Repo: repo}
}

// prRun is an archived pr run on takt/x, the state applyDisposition reads.
func prRun() *bundle.State {
	return &bundle.State{
		Branch:      "takt/x",
		Disposition: &bundle.Disposition{Choice: dispositionPR, Applied: true},
	}
}

// TestArchivedPRPushIsOfferedWhenGitCannotAnswer covers the first of the two
// reads failing. The context is cancelled before the call, so `git cat-file`
// never runs and the error is not an ExitError — the only kind CommitExists
// turns into an answer — which leaves the existence of the remote-tracking
// ref genuinely unknown. The archive has already landed and the session
// confirms every cleanup before running it, so the push is offered anyway
// and the stop still succeeds.
func TestArchivedPRPushIsOfferedWhenGitCannotAnswer(t *testing.T) {
	t.Parallel()
	ws := prWorkspace(t, testutil.NewRepo(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cleanup, _, err := applyDisposition(ctx, ws, prRun(), "")
	if err != nil {
		t.Fatalf("a git read that fails must not fail the archived stop: %v", err)
	}
	if len(cleanup) != 1 || cleanup[0] != prPushX {
		t.Fatalf("an unanswerable read still offers the push: %v", cleanup)
	}
}

// TestArchivedPRPushIsOfferedWhenTheAncestryReadFails covers the second read
// failing on its own. The remote-tracking ref exists while the branch it
// tracks does not, so CommitExists answers true and `git merge-base
// --is-ancestor takt/x refs/remotes/origin/takt/x` then exits 128 over the
// name it cannot resolve — not the exit 1 that would be an answer. The
// fallback is the same as the first read's.
func TestArchivedPRPushIsOfferedWhenTheAncestryReadFails(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	testutil.Git(t, root, "update-ref", "refs/remotes/origin/takt/x", "HEAD")
	cleanup, _, err := applyDisposition(t.Context(), prWorkspace(t, root), prRun(), "")
	if err != nil {
		t.Fatalf("the second read failing must not fail the archived stop either: %v", err)
	}
	if len(cleanup) != 1 || cleanup[0] != prPushX {
		t.Fatalf("an unanswerable ancestry read still offers the push: %v", cleanup)
	}
}
