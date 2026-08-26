package doctor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/doctor"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/plan"
)

func newDir(t *testing.T) bundle.Dir {
	t.Helper()
	d, err := bundle.ResolveDir(t.TempDir(), t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// healthy is a bundle with nothing wrong with it. Its review config matches
// the shipped defaults (spec §12: all three gates on), because that is what
// the checks judge against — a zero RunConfig would silently switch the gate
// half of index-staleness off.
func healthy(slug string) *bundle.State {
	return &bundle.State{
		Schema: 1, Slug: slug, Topic: "t", Phase: bundle.PhaseExecute,
		CreatedAt: time.Now(),
		Config:    bundle.RunConfig{Review: bundle.ReviewConfig{Spec: true, Plan: true, Tasks: true}},
		Branch:    "takt/" + slug, Base: "main", Gates: map[string]string{"spec": "ok", "plan": "ok"},
		Tasks: []bundle.Task{
			{ID: 1, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Class: "implement"},
		},
	}
}

func noOpts(string) plan.ValidateOpts { return plan.ValidateOpts{RepoRoot: "/", MaxFilesPerTask: 12} }

func levels(fs []doctor.Finding, check string) []string {
	var out []string
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f.Level)
		}
	}
	return out
}

func TestHealthyBundlePasses(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	if err := bundle.SaveState(d.Bundle("ok"), healthy("ok")); err != nil {
		t.Fatal(err)
	}
	fs := doctor.Run(context.Background(), d, false, doctor.Default, noOpts)
	for _, f := range fs {
		if f.Level != "PASS" {
			t.Errorf("unexpected %+v", f)
		}
	}
	if len(levels(fs, "state-schema")) != 1 || len(levels(fs, "plan-disjoint")) != 1 {
		t.Fatalf("each check reports once per bundle: %+v", fs)
	}
}

func TestCorruptStateIsError(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	if err := os.MkdirAll(d.Bundle("bad"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(d.Bundle("bad"), "state.json"), []byte(`{"schema":1,"slug":"bad","phase":"flying"}`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	fs := doctor.Run(context.Background(), d, false, doctor.Default, noOpts)
	if l := levels(fs, "state-schema"); len(l) != 1 || l[0] != "ERROR" {
		t.Fatalf("state-schema = %v", l)
	}
}

func TestActiveWaveReferencingMissingWaveIsError(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("aw")
	st.ActiveWave = &bundle.ActiveWave{N: 5, Attempt: 1, StartedAt: time.Now()}
	if err := bundle.SaveState(d.Bundle("aw"), st); err != nil {
		t.Fatal(err)
	}
	fs := doctor.Run(context.Background(), d, false, doctor.Default, noOpts)
	if l := levels(fs, "state-schema"); l[0] != "ERROR" {
		t.Fatalf("active_wave pointing at a wave with no tasks must be ERROR: %+v", fs)
	}
}

// TestActiveWaveWithoutASliceWarns covers the upgrade path: a wave
// dispatched before close records were kept per slice has slice 0. takt
// heals it (the next close records it as slice 1), so this is a WARN and not
// an ERROR — but the state is still from an older build, and saying so is
// what stops the user hunting for the cause of a renumbered record.
func TestActiveWaveWithoutASliceWarns(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("old")
	st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: time.Now(), Tasks: []int{1}}
	if err := bundle.SaveState(d.Bundle("old"), st); err != nil {
		t.Fatal(err)
	}
	fs := doctor.Run(context.Background(), d, false, doctor.Default, noOpts)
	if l := levels(fs, "state-schema"); len(l) != 1 || l[0] != "WARN" {
		t.Fatalf("state-schema = %v (%+v)", l, fs)
	}
	for _, f := range fs {
		if f.Check == "state-schema" && !strings.Contains(f.Message, "active_wave.slice is 0") {
			t.Fatalf("the warning must name the field: %+v", f)
		}
	}
	// A wave that does have a slice is not warned about.
	st.ActiveWave.Slice = 1
	if err := bundle.SaveState(d.Bundle("old"), st); err != nil {
		t.Fatal(err)
	}
	if l := levels(doctor.Run(context.Background(), d, false, doctor.Default, noOpts), "state-schema"); l[0] != "PASS" {
		t.Fatalf("state-schema = %v", l)
	}
}

func TestPlanDisjointFindsUnorderedOverlap(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	if err := bundle.SaveState(d.Bundle("ov"), healthy("ov")); err != nil {
		t.Fatal(err)
	}
	planIndex := `{"schema":1,"spec_hash":"x","tasks":[
	  {"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]},
	  {"id":2,"title":"b","description":"d","files":["a.go"],"verify":["true"]}]}`
	if err := os.WriteFile(filepath.Join(d.Bundle("ov"), "plan.index.json"), []byte(planIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := doctor.Run(context.Background(), d, false, doctor.Default, noOpts)
	if l := levels(fs, "plan-disjoint"); len(l) != 1 || l[0] != "ERROR" {
		t.Fatalf("plan-disjoint = %v", l)
	}
}

func TestArchivedSkippedUnlessAll(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("old")
	st.Phase = bundle.PhaseArchived
	if err := bundle.SaveState(d.Bundle("old"), st); err != nil {
		t.Fatal(err)
	}
	// index-lock is repo-wide (task 7): it runs once per invocation
	// regardless of any bundle being archived, so the only finding left when
	// the sole bundle is skipped is its PASS (doctor.Run never sets
	// RepoRoot, so index-lock always passes here).
	skipped := doctor.Run(context.Background(), d, false, doctor.Default, noOpts)
	if len(skipped) != 1 || skipped[0].Check != "index-lock" {
		t.Fatalf("archived must be skipped, leaving only the repo-wide check: %+v", skipped)
	}
	if all := doctor.Run(context.Background(), d, true, doctor.Default, noOpts); len(all) <= 1 {
		t.Fatal("--all must include archived")
	}
}

func TestStaleWaveWarnsOnlyWhenSessionIsDead(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("w")
	old := time.Now().Add(-2 * time.Hour)
	st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: old, SessionID: "S", Tasks: []int{1}}
	st.Session = &bundle.Session{ID: "S", Heartbeat: old}
	bundle.SaveState(d.Bundle("w"), st)
	o := doctor.Options{
		Now: time.Now(), WaveStaleAfter: 30 * time.Minute, LockTTL: 10 * time.Minute,
		ValidateOpts: noOpts, Resolve: func(string) bool { return true },
	}
	fs := doctor.RunWith(context.Background(), d, o, []doctor.Check{doctor.StaleWave})
	if l := levels(fs, "stale-wave"); len(l) != 1 || l[0] != "WARN" {
		t.Fatalf("%+v", fs)
	}
	st.Session.Heartbeat = time.Now()
	bundle.SaveState(d.Bundle("w"), st)
	fs = doctor.RunWith(context.Background(), d, o, []doctor.Check{doctor.StaleWave})
	if l := levels(fs, "stale-wave"); l[0] != "PASS" {
		t.Fatal("a live session's long wave is not stale")
	}
}

func TestIndexStalenessAndBranch(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("s")
	st.Phase = bundle.PhaseExecute
	st.Gates = map[string]string{"spec": "ok", "plan": "ok"}
	bundle.SaveState(d.Bundle("s"), st)
	os.WriteFile(filepath.Join(d.Bundle("s"), "spec.md"), []byte("# spec\n"), 0o600)
	os.WriteFile(filepath.Join(d.Bundle("s"), "plan.md"), []byte("# plan\n"), 0o600)
	staleIndex := `{"schema":1,"spec_hash":"sha256:stale","tasks":[` +
		`{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]}]}`
	os.WriteFile(filepath.Join(d.Bundle("s"), "plan.index.json"), []byte(staleIndex), 0o600)
	o := doctor.Options{
		Now:            time.Now(),
		WaveStaleAfter: time.Hour,
		LockTTL:        time.Hour,
		CurrentBranch:  "other",
		ValidateOpts:   noOpts,
		Resolve:        func(ref string) bool { return ref == "takt/s" },
	}
	fs := doctor.RunWith(context.Background(), d, o, []doctor.Check{doctor.IndexStaleness, doctor.Branch})
	if l := levels(fs, "index-staleness"); len(l) == 0 || l[0] != "ERROR" {
		t.Fatalf("no receipts in phase execute → ERROR: %+v", fs)
	}
	if l := levels(fs, "branch"); len(l) == 0 || l[0] != "ERROR" {
		t.Fatalf("base_sha unresolvable → ERROR: %+v", fs)
	}
	st.BaseSHA = "takt/s" // resolvable in this stub
	bundle.SaveState(d.Bundle("s"), st)
	fs = doctor.RunWith(context.Background(), d, o, []doctor.Check{doctor.Branch})
	if l := levels(fs, "branch"); l[0] != "WARN" {
		t.Fatalf("checkout on another branch → WARN: %+v", fs)
	}
}

// TestIndexStalenessSkipsADisabledReview covers review I6: a run started
// with --no-review-spec / --no-review-plan never takes those receipts, so
// the gate half of index-staleness has nothing to check — and used to
// report the missing receipt as an ERROR telling the user to run a review
// they had switched off. The fixture is the one
// TestIndexStalenessAndBranch proves ERRORs with the reviews on.
func TestIndexStalenessSkipsADisabledReview(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("off")
	st.Config.Review.Spec, st.Config.Review.Plan = false, false
	st.Gates = map[string]string{"spec": "disabled", "plan": "disabled"}
	if err := bundle.SaveState(d.Bundle("off"), st); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"spec.md": "# spec\n", "plan.md": "# plan\n"} {
		if err := os.WriteFile(filepath.Join(d.Bundle("off"), name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	idx := `{"schema":1,"spec_hash":"` + goals.Hash([]byte("# spec\n")) + `","tasks":[` +
		`{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]}]}`
	if err := os.WriteFile(filepath.Join(d.Bundle("off"), "plan.index.json"), []byte(idx), 0o600); err != nil {
		t.Fatal(err)
	}
	o := doctor.Options{
		Now: time.Now(), WaveStaleAfter: time.Hour, LockTTL: time.Hour,
		ValidateOpts: noOpts, Resolve: func(string) bool { return true },
	}
	fs := doctor.RunWith(context.Background(), d, o, []doctor.Check{doctor.IndexStaleness})
	if l := levels(fs, "index-staleness"); len(l) != 1 || l[0] != "PASS" {
		t.Fatalf("a disabled review has no receipt to go stale: %+v", fs)
	}
}

func TestIndexLockWarnsOnlyWhenStale(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o750)
	d := newDir(t)
	st := healthy("w")
	bundle.SaveState(d.Bundle("w"), st)
	o := doctor.Options{
		Now: time.Now(), RepoRoot: root, ValidateOpts: noOpts, Resolve: func(string) bool { return true },
	}
	checks := []doctor.Check{doctor.IndexLock}
	if l := levels(doctor.RunWith(context.Background(), d, o, checks), "index-lock"); len(l) != 1 || l[0] != "PASS" {
		t.Fatalf("no lock file → PASS: %v", l)
	}
	lock := filepath.Join(root, ".git", "index.lock")
	os.WriteFile(lock, nil, 0o600)
	if l := levels(doctor.RunWith(context.Background(), d, o, checks), "index-lock"); l[0] != "PASS" {
		t.Fatal("a fresh lock belongs to a running git command")
	}
	old := time.Now().Add(-10 * time.Minute)
	os.Chtimes(lock, old, old)
	fs := doctor.RunWith(context.Background(), d, o, checks)
	if l := levels(fs, "index-lock"); l[0] != "WARN" {
		t.Fatalf("stale lock → WARN: %+v", fs)
	}
	if !strings.Contains(fs[0].Fix, lock) {
		t.Fatalf("fix names the file: %+v", fs[0])
	}
}

// TestIndexStalenessSkipsArchived is the fixture TestIndexStalenessAndBranch
// ERRORs/WARNs with (an ungated gate, a plan.index.json spec_hash that no
// longer matches spec.md), replayed on an archived bundle: its artifacts
// are frozen history, so a later edit to spec.md — e.g. by a subsequent run
// reusing the same branch — must not turn every archived run into an ERROR
// under --all.
func TestIndexStalenessSkipsArchived(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("s")
	st.Phase = bundle.PhaseArchived
	st.Gates = map[string]string{"spec": "ok", "plan": "ok"}
	bundle.SaveState(d.Bundle("s"), st)
	os.WriteFile(filepath.Join(d.Bundle("s"), "spec.md"), []byte("# spec\n"), 0o600)
	os.WriteFile(filepath.Join(d.Bundle("s"), "plan.md"), []byte("# plan\n"), 0o600)
	staleIndex := `{"schema":1,"spec_hash":"sha256:stale","tasks":[` +
		`{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]}]}`
	os.WriteFile(filepath.Join(d.Bundle("s"), "plan.index.json"), []byte(staleIndex), 0o600)
	o := doctor.Options{
		All: true, Now: time.Now(), WaveStaleAfter: time.Hour, LockTTL: time.Hour,
		ValidateOpts: noOpts, Resolve: func(string) bool { return true },
	}
	fs := doctor.RunWith(context.Background(), d, o, []doctor.Check{doctor.IndexStaleness})
	if l := levels(fs, "index-staleness"); len(l) != 1 || l[0] != "PASS" {
		t.Fatalf("archived artifacts are history, not stale: %+v", fs)
	}
}

// TestRunWrapperUsesRealDurations covers the plan-1 wrapper: Run left both
// thresholds at the zero value, so every wave was past a "0s" staleness
// budget and every heartbeat past a "0s" lock TTL — a five-minute-old wave
// whose session is alive and answering came back WARN, telling the user to
// recover a wave whose agents are still working. The wrapper carries
// config's shipped defaults instead.
func TestRunWrapperUsesRealDurations(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("live")
	now := time.Now()
	st.ActiveWave = &bundle.ActiveWave{
		N: 0, Slice: 1, Attempt: 1, StartedAt: now.Add(-5 * time.Minute), SessionID: "S", Tasks: []int{1},
	}
	st.Session = &bundle.Session{ID: "S", Heartbeat: now}
	if err := bundle.SaveState(d.Bundle("live"), st); err != nil {
		t.Fatal(err)
	}
	fs := doctor.Run(context.Background(), d, false, []doctor.Check{doctor.StaleWave}, noOpts)
	if l := levels(fs, "stale-wave"); len(l) != 1 || l[0] != "PASS" {
		t.Fatalf("a five-minute wave with a live session is not stale: %+v", fs)
	}
}

// TestSliceLessActiveWaveWarnDoesNotMaskAnError covers the ordering inside
// state-schema: the slice-less-active-wave WARN returned as soon as it
// fired, so a bundle that also had something wrong the user must act on —
// here a pending gate with no id — was reported as a WARN about an old
// build and nothing else. The WARN is the level only while no ERROR fires.
func TestSliceLessActiveWaveWarnDoesNotMaskAnError(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("both")
	st.ActiveWave = &bundle.ActiveWave{N: 0, Attempt: 1, StartedAt: time.Now(), Tasks: []int{1}}
	st.PendingGate = &bundle.PendingGate{OpenedAt: time.Now()}
	if err := bundle.SaveState(d.Bundle("both"), st); err != nil {
		t.Fatal(err)
	}
	fs := doctor.Run(context.Background(), d, false, []doctor.Check{doctor.StateSchema}, noOpts)
	if l := levels(fs, "state-schema"); len(l) != 1 || l[0] != "ERROR" {
		t.Fatalf("an ERROR must win over the slice WARN: %+v", fs)
	}
	for _, f := range fs {
		if f.Check == "state-schema" && !strings.Contains(f.Message, "pending_gate") {
			t.Fatalf("the finding must name what has to be fixed: %+v", f)
		}
	}
}

// TestUncommittedArchiveNeedsTheDirtyHook covers review I1's report: an
// archived run with anything still outstanding under its bundle never got
// its archive commit, and doctor says so. The hook is what makes that
// askable at all — a caller that wires none (this package's own unit tests,
// and every caller that predates it) simply does not run the check rather
// than guessing.
func TestUncommittedArchiveNeedsTheDirtyHook(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("done")
	st.Phase = bundle.PhaseArchived
	if err := bundle.SaveState(d.Bundle("done"), st); err != nil {
		t.Fatal(err)
	}
	opts := func(dirty func(string) bool) doctor.Options {
		return doctor.Options{
			All: true, Now: time.Now(), RepoRoot: d.RepoRoot, Dirty: dirty,
			WaveStaleAfter: time.Hour, LockTTL: time.Hour,
			Resolve: func(string) bool { return true }, ValidateOpts: noOpts,
		}
	}
	only := []doctor.Check{doctor.StateSchema}
	fs := doctor.RunWith(context.Background(), d, opts(nil), only)
	if len(fs) != 1 || fs[0].Level != "PASS" {
		t.Fatalf("no hook → nothing is asked of git: %+v", fs)
	}
	asked := ""
	fs = doctor.RunWith(context.Background(), d,
		opts(func(rel string) bool { asked = rel; return true }), only)
	if len(fs) != 1 || fs[0].Level != "ERROR" || fs[0].Message != "archived run has an uncommitted bundle" {
		t.Fatalf("%+v", fs)
	}
	if asked != "docs/takt/done" {
		t.Fatalf("the hook is asked about the bundle, repo-relative: %q", asked)
	}
	fs = doctor.RunWith(context.Background(), d, opts(func(string) bool { return false }), only)
	if len(fs) != 1 || fs[0].Level != "PASS" {
		t.Fatalf("a committed archive passes: %+v", fs)
	}
}

// TestArchivedBundleIsJudgedWithoutAllOnlyWhenDirty pins the one hole in the
// archived skip. An archived run's artifacts are frozen history, so `takt
// doctor` passes over it unless asked with --all — except when its bundle
// was never committed, which is the one thing about an archived run that
// still needs doing and the one run nobody calls `takt next` on again
// (review I1). Only state-schema is run for it; the rest of the skip stands.
func TestArchivedBundleIsJudgedWithoutAllOnlyWhenDirty(t *testing.T) {
	t.Parallel()
	d := newDir(t)
	st := healthy("done")
	st.Phase = bundle.PhaseArchived
	if err := bundle.SaveState(d.Bundle("done"), st); err != nil {
		t.Fatal(err)
	}
	opts := func(dirty bool) doctor.Options {
		return doctor.Options{
			Now: time.Now(), RepoRoot: d.RepoRoot, Dirty: func(string) bool { return dirty },
			WaveStaleAfter: time.Hour, LockTTL: time.Hour,
			Resolve: func(string) bool { return true }, ValidateOpts: noOpts,
		}
	}
	perBundle := func(fs []doctor.Finding) []doctor.Finding {
		var out []doctor.Finding
		for _, f := range fs {
			if f.Slug != "" {
				out = append(out, f)
			}
		}
		return out
	}
	if got := perBundle(doctor.RunWith(context.Background(), d, opts(false), doctor.Default)); len(got) != 0 {
		t.Fatalf("a committed archive stays skipped without --all: %+v", got)
	}
	all := doctor.RunWith(context.Background(), d, opts(true), doctor.Default)
	got := perBundle(all)
	if len(got) != 1 || got[0].Check != "state-schema" || got[0].Level != "ERROR" ||
		got[0].Message != "archived run has an uncommitted bundle" {
		t.Fatalf("only state-schema judges an uncommitted archive without --all: %+v", all)
	}
	if !doctor.HasError(all) {
		t.Fatal("it must be an ERROR, so `takt doctor` exits 1")
	}
	// A caller that narrowed the checks past state-schema still gets the skip.
	if got = perBundle(doctor.RunWith(context.Background(), d, opts(true),
		[]doctor.Check{doctor.Branch})); len(got) != 0 {
		t.Fatalf("state-schema is not forced on a caller that did not ask for it: %+v", got)
	}
}
