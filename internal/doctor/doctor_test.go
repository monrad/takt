package doctor_test

import (
	"context"
	"os"
	"path/filepath"
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
	if fs := doctor.Run(context.Background(), d, false, doctor.Default, noOpts); len(fs) != 0 {
		t.Fatalf("archived must be skipped: %+v", fs)
	}
	if fs := doctor.Run(context.Background(), d, true, doctor.Default, noOpts); len(fs) == 0 {
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
