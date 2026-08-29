package cli_test

import (
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/wave"
)

// TestWaveClosedEventCarriesSliceAndReviewFindings pins what the retro reads
// (#23, #25): the wave_closed event names the slice it answers — without it a
// sliced wave's closes cannot be paired with their dispatches — and carries
// the findings this attempt's task reviews raised, so an attempt whose close
// record a later attempt deletes still counted once. The record keeps the
// same number.
func TestWaveClosedEventCarriesSliceAndReviewFindings(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	bumpTask3Attempt(t, bdir)
	env := map[string]string{
		"TAKT_FAKE_REVIEW": `{"verdict":"approve","summary":"ok",` +
			`"findings":[{"severity":"minor","file":"a.go","line":1,"title":"nit title","detail":"nit detail"}]}`,
	}
	code, o, errb := runIn(t, root, env, "close-wave", "--slug", "demo")
	if code != 0 || o["committed"] != true {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	var closed *bundle.Event
	for i, e := range events {
		if e.Type == "wave_closed" {
			closed = &events[i]
		}
	}
	if closed == nil {
		t.Fatalf("no wave_closed event: %+v", events)
	}
	if closed.Data["slice"] != float64(1) {
		t.Fatalf("wave_closed slice = %v, want 1: %+v", closed.Data["slice"], closed.Data)
	}
	if closed.Data["review_findings"] != float64(1) {
		t.Fatalf("wave_closed review_findings = %v, want 1: %+v", closed.Data["review_findings"], closed.Data)
	}
	c, err := wave.ReadClose(bdir, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.ReviewFindings != 1 {
		t.Fatalf("close record = %+v, want review_findings 1", c)
	}
}
