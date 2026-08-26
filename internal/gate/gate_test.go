package gate_test

import (
	"os"
	"path/filepath"
	"strings"
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

// TestWriteReceiptLeavesNoTempOnSuccess pins the gates directory after a
// receipt is written: exactly the receipt. The write goes through the shared
// atomic writer, which flushes the bytes before the rename and removes its
// temporary file on every path out.
func TestWriteReceiptLeavesNoTempOnSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "spec.md", "# spec\n")
	h, _, _ := gate.Hash(gate.Spec, dir)
	rc := gate.Receipt{
		Gate: gate.Spec, Hash: h, Verdict: gate.VerdictApprove,
		Reviewer: gate.Reviewer{Provider: "fake", Model: "m"}, TS: time.Now(),
	}
	if err := gate.WriteReceipt(dir, rc); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "gates"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != gate.Spec+".json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("gates/ must hold only the receipt, got %s", strings.Join(names, ", "))
	}
}

// specAt writes a spec.md with the given body and returns the gate's hash.
func specAt(t *testing.T, dir, body string) string {
	t.Helper()
	write(t, dir, "spec.md", body)
	h, _, err := gate.Hash(gate.Spec, dir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func revisionAt(hash string) bundle.Event {
	return bundle.Event{
		Type: gate.EvRevisionAccepted,
		Data: map[string]any{"gate": gate.Spec, "hash": hash},
	}
}

func TestRevisionAcceptedSatisfiesOnlyAfterAnEdit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h1 := specAt(t, dir, "# spec v1\n")
	rc := gate.Receipt{Gate: gate.Spec, Hash: h1, Verdict: gate.VerdictRework,
		Severities: map[string]int{"minor": 2}, TS: time.Now()}
	if err := gate.WriteReceipt(dir, rc); err != nil {
		t.Fatal(err)
	}
	ev := []bundle.Event{revisionAt(h1)}

	s, err := gate.Compute(dir, gate.Spec, ev)
	if err != nil {
		t.Fatal(err)
	}
	if s.Satisfied {
		t.Fatal("answering revise and editing nothing must leave the gate open")
	}

	specAt(t, dir, "# spec v2\n")
	s, err = gate.Compute(dir, gate.Spec, ev)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Satisfied || s.Verdict != gate.VerdictRevised {
		t.Fatalf("an edit after revise must close the gate: %+v", s)
	}
}

func TestReceiptAtTheCurrentHashOutranksARevisionEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h1 := specAt(t, dir, "# spec v1\n")
	h2 := specAt(t, dir, "# spec v2\n")
	// The user revised, then ran `takt review spec --force` and was rejected.
	rc := gate.Receipt{Gate: gate.Spec, Hash: h2, Verdict: gate.VerdictReject, TS: time.Now()}
	if err := gate.WriteReceipt(dir, rc); err != nil {
		t.Fatal(err)
	}
	s, err := gate.Compute(dir, gate.Spec, []bundle.Event{revisionAt(h1)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Satisfied || s.Verdict != gate.VerdictReject {
		t.Fatalf("a fresh verdict must not be masked by a stale revision: %+v", s)
	}
}

func TestNewestRevisionEventWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h1 := specAt(t, dir, "# spec v1\n")
	h2 := specAt(t, dir, "# spec v2\n")
	s, err := gate.Compute(dir, gate.Spec, []bundle.Event{revisionAt(h1), revisionAt(h2)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Satisfied {
		t.Fatal("the newest revision was taken at the current hash, so nothing has been edited since")
	}
}

func TestRevisionEventForOneGateDoesNotSatisfyTheOther(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h1 := specAt(t, dir, "# spec v1\n")
	write(t, dir, "plan.md", "# plan\n")
	write(t, dir, "plan.index.json", index)
	specAt(t, dir, "# spec v2\n")
	s, err := gate.Compute(dir, gate.Plan, []bundle.Event{revisionAt(h1)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Satisfied {
		t.Fatal("a spec revision must never satisfy the plan gate")
	}
}

func TestComputeReportsBlockingFromTheReceipt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h1 := specAt(t, dir, "# spec\n")
	rc := gate.Receipt{Gate: gate.Spec, Hash: h1, Verdict: gate.VerdictRework,
		Severities: map[string]int{"blocking": 1}, TS: time.Now()}
	if err := gate.WriteReceipt(dir, rc); err != nil {
		t.Fatal(err)
	}
	s, err := gate.Compute(dir, gate.Spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Blocking {
		t.Fatalf("a receipt tallying a blocking finding must report Blocking: %+v", s)
	}
}

func TestReceiptCarriesSeverityCounts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := gate.Receipt{Gate: gate.Spec, Hash: "h1", Verdict: gate.VerdictRework,
		Severities: map[string]int{"blocking": 1, "minor": 2}, TS: time.Now()}
	if err := gate.WriteReceipt(dir, rc); err != nil {
		t.Fatal(err)
	}
	got, err := gate.ReadReceipt(dir, gate.Spec)
	if err != nil || got == nil {
		t.Fatalf("%v %v", got, err)
	}
	if got.Severities["blocking"] != 1 || got.Severities["minor"] != 2 {
		t.Fatalf("severities lost in the round trip: %v", got.Severities)
	}
	old := gate.Receipt{Gate: gate.Plan, Hash: "h1", Verdict: gate.VerdictApprove, TS: time.Now()}
	if err = gate.WriteReceipt(dir, old); err != nil {
		t.Fatal(err)
	}
	prior, err := gate.ReadReceipt(dir, gate.Plan)
	if err != nil || prior == nil {
		t.Fatalf("%v %v", prior, err)
	}
	if prior.Severities["blocking"] != 0 {
		t.Fatal("a receipt written before severities existed must read as zero blocking")
	}
}
