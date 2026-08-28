package op_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/op"
)

func TestOpJSONOmitsUnusedFields(t *testing.T) {
	t.Parallel()
	w := 0
	o := op.Op{
		Op:        op.Dispatch,
		Narration: "wave 0: 1 task",
		Wave:      &w,
		Attempt:   1,
		Agents: []op.Agent{
			{
				Task:  1,
				Agent: "implementer",
				Class: "bounded",
				Model: "sonnet",
				Brief: "/abs/b.md",
				Cwd:   "/repo",
				Label: "task 1",
			},
		},
		Record: "takt record --task <N> --attempt 1 --from <file>",
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"op":"dispatch"`, `"wave":0`, `"attempt":1`, `"model":"sonnet"`, `"record":`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	for _, absent := range []string{`"gate"`, `"question"`, `"command"`, `"reason"`, `"step"`, `"warnings"`} {
		if strings.Contains(s, absent) {
			t.Errorf("unexpected %s in %s", absent, s)
		}
	}
	var back op.Op
	if uerr := json.Unmarshal(b, &back); uerr != nil || back.Wave == nil || *back.Wave != 0 {
		t.Fatalf("round trip: %v %+v", uerr, back)
	}
}

func TestOpJSONWarningsPresentWhenSet(t *testing.T) {
	t.Parallel()
	o := op.Op{
		Op:        op.Stop,
		Narration: "n",
		Reason:    "archived",
		Warnings:  []string{"info/exclude not written: permission denied"},
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"warnings":["info/exclude not written: permission denied"]`) {
		t.Errorf("missing warnings in %s", s)
	}
}

func TestStopAndAskShapes(t *testing.T) {
	t.Parallel()
	b, _ := json.Marshal(op.Op{Op: op.Stop, Narration: "n", Reason: "archived"})
	if string(b) != `{"op":"stop","narration":"n","reason":"archived"}` {
		t.Fatalf("stop = %s", b)
	}
	ask := op.Op{
		Op:        op.Ask,
		Narration: "n",
		Gate:      "owner",
		Question:  "q?",
		Options:   []op.Option{{Choice: "abort", Label: "Abort", Description: "d"}},
		Answer:    "takt answer --gate owner --choice <choice>",
	}
	b, _ = json.Marshal(ask)
	if !strings.Contains(string(b), `"options":[{"choice":"abort"`) {
		t.Fatalf("ask = %s", b)
	}
}
