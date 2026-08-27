//nolint:testpackage // tests an unexported helper
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/gate"
)

func TestWriteResultJSONRoundTripsFindings(t *testing.T) {
	t.Parallel()
	bdir := t.TempDir()
	res := backend.ReviewResult{
		Verdict: "rework", Summary: "s",
		Findings: []backend.Finding{
			{Severity: "blocking", File: "spec.md", Line: 4, Title: "t", Detail: "d"},
		},
		Provider: "fake", Model: "fake",
	}
	path := filepath.Join(bdir, "reviews", "spec.json")
	if err := writeResultJSON(path, res); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got backend.ReviewResult
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got.Findings))
	}
	if got.Findings[0].Severity != "blocking" || got.Findings[0].Line != 4 {
		t.Fatalf("finding lost detail in the round trip: %+v", got.Findings[0])
	}
	if got.Verdict != "rework" {
		t.Fatalf("verdict = %q", got.Verdict)
	}
}

func TestCarryFindingsRecordsEveryFindingWithItsSeverity(t *testing.T) {
	t.Parallel()
	bdir := t.TempDir()
	fs := []backend.Finding{
		{Severity: "minor", File: "spec.md", Line: 42, Title: "wording", Detail: "ambiguous"},
		{Severity: "nit", Title: "typo"},
	}
	if err := carryFindings(bdir, "spec", fs, gate.SourceApprove); err != nil {
		t.Fatal(err)
	}
	got, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("want 2 carried findings, got %d", len(got.Items))
	}
	if got.Items[0].Severity != "minor" || got.Items[0].Line != 42 {
		t.Fatalf("severity and location must survive: %+v", got.Items[0])
	}
	if got.Items[0].Source != gate.SourceApprove || got.Items[0].Gate != "spec" {
		t.Fatalf("provenance must survive: %+v", got.Items[0])
	}
	if err = carryFindings(bdir, "spec", nil, gate.SourceApprove); err != nil {
		t.Fatal(err)
	}
	after, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 2 {
		t.Fatal("carrying no findings must add nothing")
	}
}

// TestPriorFindingsForScopedPassNeedsABlockingRework: the scoped confirming
// pass is bought by a blocking finding and by nothing else, and both halves
// of that judgement — the verdict and the severities — are read off
// reviews/spec.json, the record of the last pass a reviewer actually
// answered. The receipt is deliberately not consulted, so each case here
// leaves one on disk that disagrees.
func TestPriorFindingsForScopedPassNeedsABlockingRework(t *testing.T) {
	t.Parallel()
	blocking := backend.Finding{Severity: "blocking", File: "spec.md", Line: 4, Title: "t1", Detail: "d1"}
	minor := backend.Finding{Severity: "minor", File: "spec.md", Line: 9, Title: "t2", Detail: "d2"}
	cases := []struct {
		name     string
		verdict  string
		findings []backend.Finding
		want     int
	}{
		{"blocking rework", backend.VerdictRework, []backend.Finding{blocking, minor}, 2},
		{"rework without blocking", backend.VerdictRework, []backend.Finding{minor}, 0},
		{"rework with no findings at all", backend.VerdictRework, nil, 0},
		{"approve", backend.VerdictApprove, []backend.Finding{minor}, 0},
		{"reject", backend.VerdictReject, []backend.Finding{blocking}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bdir := t.TempDir()
			res := backend.ReviewResult{Verdict: c.verdict, Findings: c.findings}
			if err := writeResultJSON(filepath.Join(bdir, "reviews", "spec.json"), res); err != nil {
				t.Fatal(err)
			}
			// A receipt that says the opposite must not change the answer.
			rc := gate.Receipt{Gate: gate.Spec, Hash: "sha256:old", Verdict: gate.VerdictError, TS: time.Now()}
			if err := gate.WriteReceipt(bdir, rc); err != nil {
				t.Fatal(err)
			}
			got := priorFindingsForScopedPass(bdir)
			if len(got) != c.want {
				t.Fatalf("prior findings = %d, want %d", len(got), c.want)
			}
			if c.want > 0 && (got[0].Title != "t1" || got[0].Line != 4) {
				t.Fatalf("finding lost detail: %+v", got[0])
			}
		})
	}
	if got := priorFindingsForScopedPass(t.TempDir()); got != nil {
		t.Fatalf("no stored review at all must scope nothing, got %v", got)
	}
}

// TestAnErroredPassDoesNotWidenTheScopedPass replays three passes through the
// writers runReview itself uses. Pass 1 asks for rework over something
// blocking. Pass 2 falls over in the backend — which records an error receipt
// (the run has to see the failure) and, since storeFindings ignores an error
// verdict, leaves the findings alone. The confirming pass those blocking
// findings bought must still be scoped to them: nobody has confirmed they
// were addressed, and a backend outage is not a reviewer's answer.
//
// Sourcing the decision from the receipt got this wrong. The errored pass
// replaced the receipt's rework verdict with error, the scoped rubric was
// silently swapped for the full one, and the run fell back into the
// unbounded re-review loop the spec gate's fixed point exists to end.
func TestAnErroredPassDoesNotWidenTheScopedPass(t *testing.T) {
	t.Parallel()
	bdir := t.TempDir()
	pass1 := backend.ReviewResult{
		Verdict: backend.VerdictRework, Summary: "one blocking",
		Findings: []backend.Finding{
			{Severity: "blocking", File: "spec.md", Line: 1, Title: "wrong claim",
				Detail: "executeRun does not set ActiveWave"},
			{Severity: "minor", File: "spec.md", Line: 9, Title: "wording", Detail: "ambiguous"},
		},
	}
	record(t, bdir, "sha256:h1", pass1)
	record(t, bdir, "sha256:h2", backend.ReviewResult{Verdict: backend.VerdictError, Summary: "review failed"})

	got := priorFindingsForScopedPass(bdir)
	if len(got) != 2 {
		t.Fatalf("an errored pass must leave the confirming pass scoped to the blocking pass's "+
			"findings, got %d", len(got))
	}
	if got[0].Severity != "blocking" || got[0].Title != "wrong claim" ||
		got[0].Detail != "executeRun does not set ActiveWave" {
		t.Fatalf("the finding that bought the pass must survive intact: %+v", got[0])
	}
	// The other half of the rule: a pass that really did approve confirms the
	// blocking finding was addressed, so the pass after it is not scoped.
	record(t, bdir, "sha256:h3", backend.ReviewResult{Verdict: backend.VerdictApprove, Summary: "addressed"})
	if after := priorFindingsForScopedPass(bdir); after != nil {
		t.Fatalf("an approving pass answers the findings; nothing may scope the next one: %v", after)
	}
}

// record writes the two artifacts one review pass leaves behind, through the
// same helpers runReview uses, so a test replaying passes cannot drift from
// what the command actually stores.
func record(t *testing.T, bdir, hash string, res backend.ReviewResult) {
	t.Helper()
	if err := storeFindings(bdir, gate.Spec, res); err != nil {
		t.Fatal(err)
	}
	rc := gate.Receipt{
		Gate: gate.Spec, Hash: hash, Verdict: res.Verdict,
		Severities: res.SeverityCounts(), TS: time.Now(),
	}
	if err := gate.WriteReceipt(bdir, rc); err != nil {
		t.Fatal(err)
	}
}

func TestReadReviewResultTreatsAnAbsentFileAsNoFindings(t *testing.T) {
	t.Parallel()
	res, err := readReviewResult(t.TempDir(), "spec")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("want no findings, got %d", len(res.Findings))
	}
}
