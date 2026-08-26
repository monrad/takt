//nolint:testpackage // tests an unexported helper
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
