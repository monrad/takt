package finish_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

func TestVerifyRecordRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if r, err := finish.ReadVerify(dir); err != nil || r != nil {
		t.Fatalf("absent record must be (nil, nil): %v %+v", err, r)
	}
	want := finish.VerifyRecord{
		SHA:      "abc",
		Passed:   false,
		Commands: []string{"go test ./..."},
		Results: []wave.VerifyResult{
			{Command: "go test ./...", Exit: 1, Tail: "FAIL"},
		},
		At: time.Now().UTC().Round(time.Second),
	}
	if err := finish.WriteVerify(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := finish.ReadVerify(dir)
	if err != nil || got == nil || got.SHA != "abc" || got.Passed || len(got.Results) != 1 || got.Results[0].Exit != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	if fi, statErr := os.Stat(
		filepath.Join(dir, "finish", "verify.json"),
	); statErr != nil ||
		fi.Mode().Perm() != 0o600 {
		t.Fatalf("record mode: %v %v", statErr, fi)
	}
}

func TestUnionCommandsDedupesInFirstAppearanceOrder(t *testing.T) {
	t.Parallel()
	idx := plan.Index{Tasks: []plan.Task{
		{ID: 1, Verify: []string{"go test ./a", " go vet ./... "}},
		{ID: 2, Verify: []string{"go vet ./...", "", "go test ./b"}},
	}}
	got := finish.UnionCommands(idx, []string{"go test ./a", "golangci-lint run"})
	want := []string{"go test ./a", "go vet ./...", "go test ./b", "golangci-lint run"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
	if len(finish.UnionCommands(plan.Index{}, nil)) != 0 {
		t.Fatal("empty index → no commands")
	}
}

func TestExtraCommandsAppend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if x, err := finish.ReadExtra(dir); err != nil || len(x) != 0 {
		t.Fatalf("absent → empty: %v %v", err, x)
	}
	if err := finish.AppendExtra(dir, "make check"); err != nil {
		t.Fatal(err)
	}
	if err := finish.AppendExtra(dir, "make check"); err != nil {
		t.Fatal(err)
	}
	x, err := finish.ReadExtra(dir)
	if err != nil || len(x) != 1 || x[0] != "make check" {
		t.Fatalf("append is idempotent: %v %v", err, x)
	}
}
