package doctor_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/doctor"
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

func healthy(slug string) *bundle.State {
	return &bundle.State{
		Schema: 1, Slug: slug, Topic: "t", Phase: bundle.PhaseExecute,
		CreatedAt: time.Now(),
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
