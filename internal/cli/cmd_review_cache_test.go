//nolint:testpackage // tests an unexported helper
package cli

import (
	"testing"
	"time"

	"github.com/monrad/takt/internal/gate"
)

func TestCachedReceiptAnswersOnlyAReviewersVerdictAtTheCurrentHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rc   gate.Receipt
		hash string
		want bool
	}{
		{
			"approve at hash",
			gate.Receipt{Gate: "spec", Hash: "h1", Verdict: gate.VerdictApprove, TS: time.Now()},
			"h1",
			true,
		},
		{"rework at hash", gate.Receipt{Gate: "spec", Hash: "h1", Verdict: "rework", TS: time.Now()}, "h1", true},
		{
			"stale hash",
			gate.Receipt{Gate: "spec", Hash: "h0", Verdict: gate.VerdictApprove, TS: time.Now()},
			"h1",
			false,
		},
		{
			"error verdict",
			gate.Receipt{Gate: "spec", Hash: "h1", Verdict: gate.VerdictError, TS: time.Now()},
			"h1",
			false,
		},
		{"evidenced skip", gate.Receipt{Gate: "spec", Hash: "h1", Verdict: gate.VerdictError, TS: time.Now(),
			Skipped: &gate.Skipped{Reason: "outage", EvidencePath: "gates/spec.evidence.txt"}}, "h1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bdir := t.TempDir()
			if err := gate.WriteReceipt(bdir, c.rc); err != nil {
				t.Fatal(err)
			}
			if _, got := cachedReceipt(bdir, "spec", c.hash); got != c.want {
				t.Fatalf("cachedReceipt = %v, want %v", got, c.want)
			}
		})
	}
	if _, got := cachedReceipt(t.TempDir(), "spec", "h1"); got {
		t.Fatal("no receipt must not be a cache hit")
	}
}
