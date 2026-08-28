package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/testutil"
)

// erroredSpecGate walks a run to the point where the spec gate is armed and
// plants a receipt whose verdict is error — the shape a backend failure
// leaves behind, with reviews/spec.md untouched and still describing the
// previous pass.
func erroredSpecGate(t *testing.T, reason string) (string, string) {
	t.Helper()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	runIn(t, root, nil, "done", "--step", "brainstorm", "--slug", "demo")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md", goalsMD)
	runIn(t, root, nil, "done", "--step", "goals", "--slug", "demo")
	h, _, err := gate.Hash(gate.Spec, bdir)
	if err != nil {
		t.Fatal(err)
	}
	if werr := gate.WriteReceipt(bdir, gate.Receipt{
		Gate: gate.Spec, Hash: h, Verdict: gate.VerdictError, Reason: reason, TS: time.Now(),
	}); werr != nil {
		t.Fatal(werr)
	}
	return root, bdir
}

// optionChoices lists an ask op's choices in the order they were printed.
func optionChoices(t *testing.T, o map[string]any) []string {
	t.Helper()
	opts, ok := o["options"].([]any)
	if !ok {
		t.Fatalf("ask op without options: %v", o)
	}
	var out []string
	for _, raw := range opts {
		m, mok := raw.(map[string]any)
		if !mok {
			t.Fatalf("option is not an object: %v", raw)
		}
		c, cok := m["choice"].(string)
		if !cok {
			t.Fatalf("option without a choice: %v", m)
		}
		out = append(out, c)
	}
	return out
}

// TestAnswerRetryOnAnErroredGateWritesNothingAndClearsIt drives the whole
// error path a user actually meets: an errored spec review asks how to
// proceed, naming the backend's reason and offering to re-run the reviewer,
// and answering retry clears the gate without recording anything — no
// override, no accepted revision. Because nothing was written, the same
// question comes back on the next `takt next` if the review is not re-run:
// the error receipt still answers at the hash, which is what makes the
// session's obligation self-enforcing rather than a promise (#43.2).
func TestAnswerRetryOnAnErroredGateWritesNothingAndClearsIt(t *testing.T) {
	t.Parallel()
	root, bdir := erroredSpecGate(t, "backend fell over")

	code, o, errb := next(t, root, nil)
	if code != 0 || o["op"] != "ask" || o["gate"] != "gate_review" {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	ctx, _ := o["context"].(map[string]any)
	if ctx["reason"] != "backend fell over" {
		t.Fatalf("the ask must carry the receipt's reason: %v", ctx)
	}
	q, _ := o["question"].(string)
	for _, want := range []string{"errored: backend fell over", "still describes the previous pass"} {
		if !strings.Contains(q, want) {
			t.Fatalf("question = %q, want it to mention %q", q, want)
		}
	}
	if got := optionChoices(t, o); strings.Join(got, ",") != "retry,accept,stop" {
		t.Fatalf("options = %v, want retry, accept, stop", got)
	}

	code, res, errb := runIn(t, root, nil, "answer", "--gate", "gate_review", "--choice", "retry", "--slug", "demo")
	if code != 0 || res["cleared"] != true {
		t.Fatalf("%d %v %s", code, res, errb)
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "gate_overridden" || e.Type == gate.EvRevisionAccepted {
			t.Fatalf("retry must write nothing about the verdict: %+v", e)
		}
	}

	// The review was not re-run, so the same gate is back.
	code, o, errb = next(t, root, nil)
	if code != 0 || o["op"] != "ask" || o["gate"] != "gate_review" {
		t.Fatalf("an unanswered error receipt must re-arm the gate: %d %v %s", code, o, errb)
	}
}

// TestAnswerRetryOnAnErrorWithNoReasonStillSaysWhatHappened: a receipt
// written before the reason field existed carries none. The question must
// still read as an account of an errored pass rather than trailing off after
// a colon.
func TestAnswerRetryOnAnErrorWithNoReasonStillSaysWhatHappened(t *testing.T) {
	t.Parallel()
	root, _ := erroredSpecGate(t, "")
	code, o, errb := next(t, root, nil)
	if code != 0 || o["gate"] != "gate_review" {
		t.Fatalf("%d %v %s", code, o, errb)
	}
	q, _ := o["question"].(string)
	if !strings.Contains(q, "(no reason recorded)") {
		t.Fatalf("question = %q, want it to say the reason is missing", q)
	}
}
