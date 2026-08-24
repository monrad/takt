package gitx_test

import (
	"testing"

	"github.com/monrad/takt/internal/gitx"
)

func TestParsePorcelainZ(t *testing.T) {
	t.Parallel()
	// " M a.go\0?? new.txt\0R  new-name.go\0old-name.go\0"
	in := []byte(" M a.go\x00?? new.txt\x00R  new-name.go\x00old-name.go\x00")
	got, err := gitx.ParsePorcelainZ(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []gitx.Entry{
		{X: ' ', Y: 'M', Path: "a.go"},
		{X: '?', Y: '?', Path: "new.txt"},
		{X: 'R', Y: ' ', Path: "new-name.go", OrigPath: "old-name.go"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParsePorcelainZEmpty(t *testing.T) {
	t.Parallel()
	got, err := gitx.ParsePorcelainZ(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParsePorcelainZTruncated(t *testing.T) {
	t.Parallel()
	if _, err := gitx.ParsePorcelainZ([]byte("M")); err == nil {
		t.Fatal("expected an error for a truncated record")
	}
}
