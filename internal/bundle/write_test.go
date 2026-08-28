//nolint:testpackage // needs the renameFile seam
package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileAtomicReplacesTheWholeFileOrNothing pins what issue #5 is
// about: the four files an agent is handed — the stable briefs, the slice
// diff, the task brief, the logs ignore rule — are written whole or not at
// all. A rename that fails must leave the previous bytes exactly as they
// were, and must not create the file at all when there was none.
//
//nolint:paralleltest // mutates the renameFile seam
func TestWriteFileAtomicReplacesTheWholeFileOrNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "waves", "0", "task-1.a1.md")

	// A success creates the parent directories and writes the bytes
	// verbatim — no trailing newline is added, unlike WriteJSONAtomic.
	if err := WriteFileAtomic(p, []byte("first brief")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "first brief" {
		t.Fatalf("content = %q", got)
	}
	// And a second success replaces it whole, however much longer it is.
	if err := WriteFileAtomic(p, []byte("a second brief, rather longer")); err != nil {
		t.Fatal(err)
	}
	const want = "a second brief, rather longer"
	if got := readFile(t, p); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}

	orig := renameFile
	renameFile = func(string, string) error { return errors.New("disk on fire") }
	t.Cleanup(func() { renameFile = orig })

	if err := WriteFileAtomic(p, []byte("half a br")); err == nil {
		t.Fatal("expected the injected rename error")
	}
	if got := readFile(t, p); got != want {
		t.Fatalf("a failed write must leave the previous bytes: %q", got)
	}
	assertNoTemp(t, filepath.Dir(p))

	// The same for a path that did not exist: a failed rename leaves no
	// partial file behind for an agent to be handed.
	fresh := filepath.Join(dir, "waves", "0", "task-2.a1.md")
	if err := WriteFileAtomic(fresh, []byte("half a br")); err == nil {
		t.Fatal("expected the injected rename error")
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed write must leave nothing at the path: %v", err)
	}
	assertNoTemp(t, filepath.Dir(fresh))
}

// readFile reads p or fails the test.
func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertNoTemp fails when dir still holds one of the writer's temporaries.
func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
