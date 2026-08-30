//nolint:testpackage // drives the unexported gatherFacts over an unexported workspace
package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/testutil"
)

// This is the chain-to-question seam of spec A3, over one real bundle on
// disk. The unit tests either side of it prove their own half against numbers
// they supply themselves: internal/config that Backends.Timeout answers for
// exactly the two names that have a key, internal/decide that the review_error
// question renders whatever chain it is handed. What neither can see is
// whether the configured chain actually reaches the question — in the
// configured order, with the configured deadlines, and without the entries
// config has no key for. A fill that is empty, reordered, or invents a key
// fails here and nowhere else (goal G5).

// The fixture's own numbers, named because mnd would flag them and because
// each is a claim about the config below: two deadlines that differ from each
// other and from every shipped default, so a rendering that quoted a constant
// rather than the gathered fact shows up as a mismatch.
const (
	reviewerClaudeTimeout  = 9 * time.Minute
	reviewerCopilotTimeout = 7 * time.Minute
)

// reviewerCfg is a config whose reviewer chain interleaves the two names
// config has a Timeout field for with two it has none for: "fake", which the
// backend registry holds, and "nonesuch", which nothing does — both legal,
// since backends.reviewer is not validated against a closed set. Claude is
// listed first and given the larger deadline, so an implementation that
// walked config's own fields instead of the chain would come out in the wrong
// order with the wrong numbers.
func reviewerCfg() config.Config {
	return config.Config{
		Backends: config.Backends{
			Reviewer: []string{"claude", "fake", "nonesuch", "copilot"},
			Claude:   config.Backend{Timeout: config.Duration(reviewerClaudeTimeout)},
			Copilot:  config.Backend{Timeout: config.Duration(reviewerCopilotTimeout)},
		},
	}
}

// reviewerState is the run the fixture holds: an execute-phase wave whose two
// tasks are dispatched, which with both digests on disk is the state row 16
// judges once a close record says a review errored.
func reviewerState() *bundle.State {
	return &bundle.State{
		Schema: 1, Slug: "demo", Topic: "demo", Phase: bundle.PhaseExecute,
		Branch: "takt/demo", Base: "main",
		Config: bundle.RunConfig{
			Autonomy: "auto", MaxParallel: 2, MaxRework: 1,
			Review: bundle.ReviewConfig{Tasks: true},
		},
		Tasks: []bundle.Task{
			{ID: 1, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Attempt: 1},
			{ID: 2, Wave: 0, Status: bundle.StatusPending, Files: []string{"b.go"}, Attempt: 1},
		},
		ActiveWave: &bundle.ActiveWave{
			N: 0, Slice: 1, Attempt: 1, StartedAt: time.Now().UTC(), SessionID: "S", Tasks: []int{1, 2},
		},
	}
}

// reviewerBundle writes the fixture to a fresh repository: the state, and a
// digest for every dispatched task, so the wave the facts describe is fully
// recorded and Decide reaches the close rather than a recovery.
func reviewerBundle(t *testing.T) (*workspace, string, *bundle.State) {
	t.Helper()
	root := testutil.NewRepo(t)
	repo, err := gitx.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := bundle.ResolveDir(repo.Root, filepath.Join(root, ".home"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace{Repo: repo, Cfg: reviewerCfg(), Dir: dir, Home: filepath.Join(root, ".home")}
	bdir := ws.Dir.Bundle("demo")
	st := reviewerState()
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	for _, id := range st.ActiveWave.Tasks {
		p := digestPath(bdir, st.ActiveWave.N, id, st.ActiveWave.Attempt)
		if err = bundle.WriteFileAtomic(p, []byte(`{"status":"done"}`)); err != nil {
			t.Fatal(err)
		}
	}
	return ws, bdir, st
}

// retryDescription is the review_error gate's retry option text — the one
// thing spec A3 grows.
func retryDescription(t *testing.T, d decide.Decision) string {
	t.Helper()
	if d.Action != decide.ActAsk || d.Op == nil || d.Op.Gate != "review_error" {
		t.Fatalf("a close that recorded a review error asks review_error: %+v", d)
	}
	for _, o := range d.Op.Options {
		if o.Choice == "retry" {
			return o.Description
		}
	}
	t.Fatalf("the gate must offer retry: %+v", d.Op.Options)
	return ""
}

// TestGatherFactsFillsReviewerBackendsInPreferenceOrder drives the real
// gatherFacts over the real config and then the real Decide over what it
// gathered: the configured chain reaches the question in the order
// backends.reviewer lists it, carrying the deadlines .takt.json holds, with
// the entries config has no key for skipped rather than rendered as keys that
// do not exist.
func TestGatherFactsFillsReviewerBackendsInPreferenceOrder(t *testing.T) {
	t.Parallel()
	ws, bdir, st := reviewerBundle(t)
	f, err := gatherFacts(t.Context(), ws, bdir, st, false, false, time.Now().UTC(), "S")
	if err != nil {
		t.Fatal(err)
	}
	want := []decide.ReviewerBackend{
		{Name: "claude", Timeout: reviewerClaudeTimeout},
		{Name: "copilot", Timeout: reviewerCopilotTimeout},
	}
	if !slices.Equal(f.ReviewerBackends, want) {
		t.Fatalf("gathered reviewer backends = %+v, want %+v", f.ReviewerBackends, want)
	}
	// The wave has to be fully recorded, or Decide takes the recovery row and
	// never reaches the close the gate hangs off.
	if len(f.Wave.Recorded) != len(st.ActiveWave.Tasks) {
		t.Fatalf("every dispatched task must be recorded: %v", f.Wave.Recorded)
	}
	f.Wave.Close = &decide.CloseFacts{ReviewErrors: []int{2}}
	d, err := decide.Decide(st, f)
	if err != nil {
		t.Fatal(err)
	}
	desc := retryDescription(t, d)
	claudeAt := strings.Index(desc, "backends.claude.timeout")
	copilotAt := strings.Index(desc, "backends.copilot.timeout")
	if claudeAt < 0 || copilotAt < 0 || claudeAt > copilotAt {
		t.Fatalf("both keys must be named, claude first: %q", desc)
	}
	if !strings.Contains(desc[claudeAt:copilotAt], reviewerClaudeTimeout.String()) {
		t.Errorf("claude's key must carry claude's configured deadline: %q", desc)
	}
	if !strings.Contains(desc[copilotAt:], reviewerCopilotTimeout.String()) {
		t.Errorf("copilot's key must carry copilot's configured deadline: %q", desc)
	}
	// Nothing config cannot speak for is named: a key that does not exist is
	// worse advice than several that do.
	for _, keyless := range []string{"fake", "nonesuch", "<name>"} {
		if strings.Contains(desc, keyless) {
			t.Errorf("an entry with no config key must be skipped, not rendered: %q holds %q", desc, keyless)
		}
	}
}
