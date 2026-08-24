package bundle_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
)

func TestAppendAndReadEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := bundle.AppendEvent(dir, "init", map[string]any{"slug": "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := bundle.AppendEvent(dir, "phase", map[string]any{"to": "plan"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if lines := strings.Split(
		strings.TrimSpace(string(raw)),
		"\n",
	); len(lines) != 2 ||
		!strings.HasPrefix(lines[0], `{"ts":`) {
		t.Fatalf("events.jsonl = %q", raw)
	}
	evs, err := bundle.ReadEvents(dir)
	if err != nil || len(evs) != 2 || evs[1].Type != "phase" || evs[1].Data["to"] != "plan" || evs[0].TS.IsZero() {
		t.Fatalf("ReadEvents = %+v, %v", evs, err)
	}
}

func TestReadEventsMissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	evs, err := bundle.ReadEvents(t.TempDir())
	if err != nil || len(evs) != 0 {
		t.Fatalf("got %v, %v", evs, err)
	}
}
