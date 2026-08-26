//nolint:testpackage // tests an unexported helper
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/monrad/takt/internal/backend"
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
