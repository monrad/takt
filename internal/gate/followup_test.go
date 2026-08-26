package gate_test

import (
	"testing"
	"time"

	"github.com/monrad/takt/internal/gate"
)

func TestFollowUpsAppendAndRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	empty, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("an absent file must read as no follow-ups, got %d", len(empty.Items))
	}
	first := gate.FollowUp{Gate: gate.Spec, Severity: "minor", File: "spec.md", Line: 42,
		Title: "wording", Detail: "ambiguous", Source: gate.SourceApprove, TS: time.Now()}
	if err = gate.AppendFollowUps(dir, first); err != nil {
		t.Fatal(err)
	}
	second := gate.FollowUp{Gate: gate.Plan, Severity: "nit", Title: "typo",
		Source: gate.SourceOverride, TS: time.Now()}
	if err = gate.AppendFollowUps(dir, second); err != nil {
		t.Fatal(err)
	}
	got, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("appends must accumulate, got %d", len(got.Items))
	}
	if got.Items[0].Line != 42 || got.Items[0].Source != gate.SourceApprove {
		t.Fatalf("first item lost detail: %+v", got.Items[0])
	}
	if got.Items[1].Gate != gate.Plan {
		t.Fatalf("second item lost its gate: %+v", got.Items[1])
	}
}

func TestAppendNoFollowUpsWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := gate.AppendFollowUps(dir); err != nil {
		t.Fatal(err)
	}
	got, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 {
		t.Fatal("appending nothing must not create the file")
	}
}
