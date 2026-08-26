//nolint:testpackage // needs the renameFile seam / shared internal fixture
package bundle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *State {
	return &State{
		Schema:      SchemaVersion,
		TaktVersion: "0.0.0-dev",
		Slug:        "demo",
		Topic:       "add a thing",
		Phase:       PhaseBrainstorm,
		CreatedAt:   time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC),
		Branch:      "takt/demo",
		Base:        "main",
		BaseSHA:     "abc123",
		Config: RunConfig{
			Autonomy:    "auto",
			Review:      ReviewConfig{Spec: true, Plan: true, Tasks: true},
			Goals:       true,
			Alignment:   true,
			MaxParallel: 8,
			MaxRework:   1,
		},
		Gates: map[string]string{"spec": "pending", "plan": "pending"},
		Tasks: []Task{},
	}
}

func TestSaveLoadRoundTripAndKeyOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := SaveState(dir, sample()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	text := string(raw)
	for _, pair := range [][2]string{{`"schema"`, `"slug"`}, {`"slug"`, `"phase"`}, {`"phase"`, `"branch"`}, {`"tasks"`, `"disposition"`}} {
		if strings.Index(text, pair[0]) > strings.Index(text, pair[1]) {
			t.Errorf("key order: %s must precede %s", pair[0], pair[1])
		}
	}
	if !strings.HasSuffix(text, "}\n") {
		t.Error("state.json must end with a newline")
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "demo" || got.Phase != PhaseBrainstorm || got.Config.MaxParallel != 8 ||
		got.Gates["plan"] != "pending" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Tasks == nil {
		t.Fatal("empty tasks must round-trip as [] not null")
	}
}

//nolint:paralleltest // mutates the renameFile seam
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, sample()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))

	orig := renameFile
	renameFile = func(string, string) error { return errors.New("disk on fire") }
	t.Cleanup(func() { renameFile = orig })

	s := sample()
	s.Phase = PhasePlan
	if err := SaveState(dir, s); err == nil {
		t.Fatal("expected the injected rename error")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(after) != string(before) {
		t.Fatal("a failed save must leave the previous state.json byte-identical")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestLoadRejectsNewerSchemaAndBadPhase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schema": 99, "slug": "x", "phase": "brainstorm"}`), 0o644)
	if _, err := LoadState(dir); err == nil {
		t.Fatal("newer schema must be refused")
	}
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schema": 1, "slug": "x", "phase": "flying"}`), 0o644)
	if _, err := LoadState(dir); err == nil {
		t.Fatal("unknown phase must be refused")
	}
}

// TestSchemaOneStateLoadsAndIsSavedAsSchemaTwoWithoutTheSession pins the
// migration: schema 1 carried the advisory lock in state.json, a tracked
// file. Such a file must still load — encoding/json drops the unknown
// session key, which is the whole migration — and the next save must stamp
// schema 2 and write no session key at all.
func TestSchemaOneStateLoadsAndIsSavedAsSchemaTwoWithoutTheSession(t *testing.T) {
	t.Parallel()
	bdir := t.TempDir()
	st := sample()
	st.Schema = 1
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(b), `"schema":1,`,
		`"schema":1,"session":{"id":"old","host":"h","heartbeat":"2026-08-24T18:02:11Z"},`, 1)
	if err = os.WriteFile(StatePath(bdir), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(bdir)
	if err != nil {
		t.Fatal("a schema-1 state with a session key must load:", err)
	}
	if err = SaveState(bdir, loaded); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(StatePath(bdir))
	if !strings.Contains(string(saved), `"schema": 2`) || strings.Contains(string(saved), `"session"`) {
		t.Fatalf("saved state must be schema 2 without a session key:\n%s", saved)
	}
	if _, err = LoadState(bdir); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTasks(t *testing.T) {
	t.Parallel()
	s := sample()
	s.Tasks = []Task{{ID: 1, Wave: 0, Status: "sleeping", Files: []string{"a.go"}, Class: "implement"}}
	if err := s.Validate(); err == nil {
		t.Fatal("unknown task status must be rejected")
	}
	s.Tasks[0].Status = StatusPending
	s.Tasks[0].Files = []string{"/abs.go"}
	if err := s.Validate(); err == nil {
		t.Fatal("absolute task file must be rejected")
	}
	s.Tasks[0].Files = []string{"a.go"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Task(1) == nil || s.Task(2) != nil {
		t.Fatal("Task(id) lookup")
	}
}
