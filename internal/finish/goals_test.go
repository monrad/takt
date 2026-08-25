package finish_test

import (
	"testing"

	"github.com/monrad/takt/internal/finish"
)

func TestParseVerdictsValidatesAgainstGoalIDs(t *testing.T) {
	t.Parallel()
	ids := []string{"G1", "G2"}
	good := []byte(`[{"id":"G1","verdict":"achieved","evidence":"go test passed","citations":["a_test.go:12"]},
	                  {"id":"G2","verdict":"partial","evidence":"docs missing","citations":[]}]`)
	vs, err := finish.ParseVerdicts(good, ids)
	if err != nil || len(vs) != 2 || vs[1].Verdict != "partial" {
		t.Fatalf("%v %+v", err, vs)
	}
	bad := map[string]string{
		"missing goal": `[{"id":"G1","verdict":"achieved","evidence":"x"}]`,
		"unknown goal": `[{"id":"G1","verdict":"achieved","evidence":"x"},{"id":"G9","verdict":"missed","evidence":"x"}]`,
		"duplicate":    `[{"id":"G1","verdict":"achieved","evidence":"x"},{"id":"G1","verdict":"missed","evidence":"x"}]`,
		"bad verdict":  `[{"id":"G1","verdict":"done","evidence":"x"},{"id":"G2","verdict":"missed","evidence":"x"}]`,
		"empty evidence": `[{"id":"G1","verdict":"achieved","evidence":""},` +
			`{"id":"G2","verdict":"missed","evidence":"x"}]`,
		"not a list": `{"id":"G1"}`,
	}
	for name, js := range bad {
		if _, perr := finish.ParseVerdicts([]byte(js), ids); perr == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestGoalsRecordUnmetHonoursWaivers(t *testing.T) {
	t.Parallel()
	r := finish.GoalsRecord{Verdicts: []finish.GoalVerdict{
		{ID: "G1", Verdict: "achieved"}, {ID: "G2", Verdict: "missed"}, {ID: "G3", Verdict: "partial"},
	}, Waived: map[string]string{"G3": "later"}}
	u := r.Unmet()
	if len(u) != 1 || u[0].ID != "G2" {
		t.Fatalf("%+v", u)
	}
	dir := t.TempDir()
	if err := finish.WriteGoals(dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := finish.ReadGoals(dir)
	if err != nil || got == nil || len(got.Verdicts) != 3 || got.Waived["G3"] != "later" {
		t.Fatalf("%v %+v", err, got)
	}
}
