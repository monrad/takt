//nolint:testpackage // tests an unexported helper
package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
)

//nolint:gocognit // table-driven test covering all six revise/gate/verdict combinations from the brief's test table
func TestAcceptRevisionRecordsOnlyForANonBlockingSpecRework(t *testing.T) {
	t.Parallel()
	const idx = `{"schema":1,"spec_hash":"x","tasks":[{"id":1,"title":"a","description":"d",` +
		`"files":["a.go"],"verify":["true"]}]}`
	cases := []struct {
		name    string
		which   string
		verdict string
		sev     map[string]int
		want    bool
	}{
		{"spec rework, minors only", "spec", "rework", map[string]int{"minor": 2}, true},
		{"spec rework, no findings at all", "spec", "rework", nil, true},
		{"spec rework, blocking", "spec", "rework", map[string]int{"blocking": 1}, false},
		{"spec reject", "spec", "reject", nil, false},
		{"spec error", "spec", "error", nil, false},
		{"plan rework", "plan", "rework", map[string]int{"minor": 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bdir := t.TempDir()
			for name, body := range map[string]string{
				"spec.md": "# spec\n", "plan.md": "# plan\n", "plan.index.json": idx,
			} {
				if err := os.WriteFile(filepath.Join(bdir, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			hash, _, err := gate.Hash(c.which, bdir)
			if err != nil {
				t.Fatal(err)
			}
			rc := gate.Receipt{Gate: c.which, Hash: hash, Verdict: c.verdict,
				Severities: c.sev, TS: time.Now()}
			if err = gate.WriteReceipt(bdir, rc); err != nil {
				t.Fatal(err)
			}
			if err = acceptRevision(bdir, c.which); err != nil {
				t.Fatal(err)
			}
			events, err := bundle.ReadEvents(bdir)
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for _, e := range events {
				if e.Type == gate.EvRevisionAccepted {
					got = true
					if e.Data["hash"] != hash || e.Data["gate"] != c.which {
						t.Fatalf("event must name the gate and the reviewed hash: %+v", e.Data)
					}
				}
			}
			if got != c.want {
				t.Fatalf("revision recorded = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAcceptRevisionIgnoresAStaleReceipt(t *testing.T) {
	t.Parallel()
	bdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bdir, "spec.md"), []byte("# spec v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc := gate.Receipt{Gate: "spec", Hash: "sha256:stale", Verdict: "rework", TS: time.Now()}
	if err := gate.WriteReceipt(bdir, rc); err != nil {
		t.Fatal(err)
	}
	if err := acceptRevision(bdir, "spec"); err != nil {
		t.Fatal(err)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == gate.EvRevisionAccepted {
			t.Fatal("a receipt that does not answer at the current hash must record nothing")
		}
	}
}
