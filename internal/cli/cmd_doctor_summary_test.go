package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/doctor"
)

func TestCountFindingsAndRenderSummary(t *testing.T) {
	t.Parallel()
	passOnly := []doctor.Finding{
		{Level: "PASS", Check: "index-lock", Message: "no stranded index.lock"},
		{Level: "PASS", Check: "state-schema", Slug: "demo", Message: "ok"},
	}
	if n := countFindings(passOnly); n != 0 {
		t.Fatalf("PASS-only findings = %d, want 0", n)
	}
	var buf bytes.Buffer
	renderDoctor(&buf, passOnly, 0)
	got := buf.String()
	if !strings.Contains(got, "2 check(s), all PASS") {
		t.Fatalf("clean summary = %q", got)
	}
	if strings.Contains(got, "finding(s)") {
		t.Fatalf("clean summary must not say finding(s): %q", got)
	}

	mixed := []doctor.Finding{
		{Level: "PASS", Check: "state-schema", Slug: "demo", Message: "ok"},
		{Level: "WARN", Check: "session", Slug: "demo", Message: "stale"},
		{Level: "ERROR", Check: "plan-disjoint", Slug: "demo", Message: "overlap", Fix: "edit plan"},
	}
	if n := countFindings(mixed); n != 2 {
		t.Fatalf("mixed findings = %d, want 2", n)
	}
	if e := countErrors(mixed); e != 1 {
		t.Fatalf("errors = %d, want 1", e)
	}
	buf.Reset()
	renderDoctor(&buf, mixed, 1)
	got = buf.String()
	if !strings.Contains(got, "2 finding(s), 1 error(s)") {
		t.Fatalf("mixed summary = %q", got)
	}
	if strings.Contains(got, "all PASS") {
		t.Fatalf("mixed summary must not say all PASS: %q", got)
	}
	if !strings.Contains(got, "fix: edit plan") {
		t.Fatalf("missing fix line: %q", got)
	}
}
