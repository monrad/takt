package gate_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
)

const index = `{"schema":1,"spec_hash":"x","tasks":[{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]}]}`

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHashCoversArtifactsAndIgnoresWave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "spec.md", "# spec\n")
	write(t, dir, "goals.md", "# goals\n")
	write(t, dir, "plan.md", "# plan\n")
	write(t, dir, "plan.index.json", index)
	h1, present, err := gate.Hash(gate.Plan, dir)
	if err != nil || len(present) != 3 {
		t.Fatalf("%v %v", err, present)
	}
	withWave := `{"schema":1,"spec_hash":"x","tasks":[{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"],"wave":3}]}`
	write(t, dir, "plan.index.json", withWave)
	if h2, _, _ := gate.Hash(gate.Plan, dir); h2 != h1 {
		t.Fatal("the display-only wave must not move the plan hash")
	}
	write(t, dir, "plan.md", "# plan v2\n")
	if h3, _, _ := gate.Hash(gate.Plan, dir); h3 == h1 {
		t.Fatal("editing plan.md must move the hash")
	}
	s1, _, _ := gate.Hash(gate.Spec, dir)
	write(t, dir, "goals.md", "# goals v2\n")
	if s2, _, _ := gate.Hash(gate.Spec, dir); s2 == s1 {
		t.Fatal("editing goals.md must move the spec hash")
	}
}

func TestHashMissingArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "spec.md", "# spec\n")
	if _, present, err := gate.Hash(gate.Spec, dir); err != nil || len(present) != 1 {
		t.Fatalf("goals.md may be absent: %v %v", err, present)
	}
	if _, _, err := gate.Hash(gate.Plan, dir); err == nil {
		t.Fatal("a missing plan.md must be an error")
	}
}

func TestReceiptRoundTripAndStatus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "spec.md", "# spec\n")
	h, _, _ := gate.Hash(gate.Spec, dir)
	if r, err := gate.ReadReceipt(dir, gate.Spec); err != nil || r != nil {
		t.Fatalf("absent receipt: %v %v", r, err)
	}
	st, _ := gate.Compute(dir, gate.Spec, nil)
	if st.Satisfied || st.Hash != h {
		t.Fatalf("%+v", st)
	}
	rc := gate.Receipt{
		Gate:     gate.Spec,
		Hash:     h,
		Verdict:  "rework",
		Reviewer: gate.Reviewer{Provider: "fake", Model: "m"},
		Findings: "reviews/spec.md",
		TS:       time.Now(),
	}
	if err := gate.WriteReceipt(dir, rc); err != nil {
		t.Fatal(err)
	}
	st, _ = gate.Compute(dir, gate.Spec, nil)
	if st.Satisfied || st.Verdict != "rework" {
		t.Fatalf("rework must not satisfy: %+v", st)
	}
	rc.Verdict = "approve"
	_ = gate.WriteReceipt(dir, rc)
	if st, _ = gate.Compute(dir, gate.Spec, nil); !st.Satisfied || st.Verdict != "approve" {
		t.Fatalf("%+v", st)
	}
	write(t, dir, "spec.md", "# spec edited\n")
	if st, _ = gate.Compute(dir, gate.Spec, nil); st.Satisfied {
		t.Fatal("an edit must re-arm the gate")
	}
}

func TestSkipAndOverrideSatisfy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "spec.md", "# spec\n")
	h, _, _ := gate.Hash(gate.Spec, dir)
	_ = gate.WriteReceipt(
		dir,
		gate.Receipt{
			Gate:    gate.Spec,
			Hash:    h,
			Verdict: "error",
			TS:      time.Now(),
			Skipped: &gate.Skipped{Reason: "copilot down", EvidencePath: "logs/x.stderr"},
		},
	)
	if st, _ := gate.Compute(dir, gate.Spec, nil); !st.Satisfied || st.Verdict != "skipped" {
		t.Fatalf("evidenced skip must satisfy: %+v", st)
	}
	dir2 := t.TempDir()
	write(t, dir2, "spec.md", "# spec\n")
	h2, _, _ := gate.Hash(gate.Spec, dir2)
	ev := []bundle.Event{{Type: "gate_overridden", Data: map[string]any{"gate": "spec", "hash": h2}}}
	if st, _ := gate.Compute(dir2, gate.Spec, ev); !st.Satisfied || st.Verdict != "overridden" {
		t.Fatalf("override event must satisfy: %+v", st)
	}
	if st, _ := gate.Compute(
		dir2,
		gate.Spec,
		[]bundle.Event{{Type: "gate_overridden", Data: map[string]any{"gate": "spec", "hash": "sha256:stale"}}},
	); st.Satisfied {
		t.Fatal("a stale override must not satisfy")
	}
}

func TestOverrideEventMalformedDataDoesNotPanic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "spec.md", "# spec\n")
	h, _, _ := gate.Hash(gate.Spec, dir)
	malformed := bundle.Event{
		Type: "gate_overridden",
		Data: map[string]any{"gate": []any{"spec"}, "hash": map[string]any{}},
	}
	if st, err := gate.Compute(dir, gate.Spec, []bundle.Event{malformed}); err != nil || st.Satisfied {
		t.Fatalf("a malformed override must fail the match, not panic: %+v %v", st, err)
	}
	wellFormed := bundle.Event{Type: "gate_overridden", Data: map[string]any{"gate": "spec", "hash": h}}
	if st, _ := gate.Compute(
		dir,
		gate.Spec,
		[]bundle.Event{malformed, wellFormed},
	); !st.Satisfied ||
		st.Verdict != "overridden" {
		t.Fatalf("a well-formed override alongside a malformed one must still satisfy: %+v", st)
	}
}
