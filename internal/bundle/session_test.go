package bundle_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
)

func TestSessionSidecarRoundTripsUnderLogs(t *testing.T) {
	t.Parallel()
	bdir := t.TempDir()
	if got, err := bundle.ReadSession(bdir); err != nil || got != nil {
		t.Fatalf("absent sidecar: %v, %v", got, err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	want := &bundle.Session{ID: "S1", Host: "h", Heartbeat: now, Generated: true}
	if err := bundle.WriteSession(bdir, want); err != nil {
		t.Fatal(err)
	}
	if bundle.SessionPath(bdir) != filepath.Join(bdir, "logs", "session.json") {
		t.Fatalf("path: %s", bundle.SessionPath(bdir))
	}
	got, err := bundle.ReadSession(bdir)
	if err != nil || got == nil || *got != *want {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	if err = bundle.ClearSession(bdir); err != nil {
		t.Fatal(err)
	}
	if err = bundle.ClearSession(bdir); err != nil {
		t.Fatal("clearing a free run must succeed:", err)
	}
	if got, _ = bundle.ReadSession(bdir); got != nil {
		t.Fatal("cleared sidecar must read as free")
	}
}

func TestSessionSidecarThatCannotBeParsedIsAnError(t *testing.T) {
	t.Parallel()
	bdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bdir, "logs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle.SessionPath(bdir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.ReadSession(bdir); err == nil {
		t.Fatal("a corrupt lock must not read as free")
	}
	if err := os.WriteFile(bundle.SessionPath(bdir), []byte(`{"id":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.ReadSession(bdir); err == nil {
		t.Fatal("an empty holder id is not a lock")
	}
}
