package wave_test

import (
	"strconv"
	"testing"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/wave"
)

func lensRec(lens string, fs ...wave.LensFinding) *wave.LensRecord {
	return &wave.LensRecord{Lens: lens, Wave: 0, Slice: 1, Attempt: 1, Findings: fs}
}

func lf(sev, file string, line, task int, title string) wave.LensFinding {
	return wave.LensFinding{
		Finding: backend.Finding{Severity: sev, File: file, Line: line, Title: title, Detail: "d"},
		Task:    task,
	}
}

func TestMergeCandidatesMergesSameFileLineKeepsHighestSeverity(t *testing.T) {
	t.Parallel()
	recs := map[string]*wave.LensRecord{
		"correctness": lensRec("correctness", lf("major", "a.go", 4, 3, "from correctness")),
		"intent":      lensRec("intent", lf("blocking", "a.go", 4, 3, "from intent")),
	}
	got := wave.MergeCandidates([]string{"correctness", "intent"}, recs)
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	c := got[0]
	if c.ID != "c1" || c.Severity != "blocking" || c.Title != "from correctness" {
		t.Fatalf("merged = %+v; want c1, blocking, title from the earliest lens", c)
	}
	if len(c.Lenses) != 2 {
		t.Fatalf("lenses = %v, want both", c.Lenses)
	}
}

func TestMergeCandidatesOrdersAndIDsAreStable(t *testing.T) {
	t.Parallel()
	recs := map[string]*wave.LensRecord{
		"correctness": lensRec("correctness",
			lf("minor", "b.go", 9, 2, "later file"), lf("blocking", "a.go", 7, 3, "same file later line")),
		"tests": lensRec("tests", lf("major", "a.go", 2, 3, "first")),
	}
	got := wave.MergeCandidates([]string{"correctness", "tests"}, recs)
	if len(got) != 3 {
		t.Fatalf("candidates = %d, want 3", len(got))
	}
	// Sorted by file, then line; ids follow that order.
	wantOrder := []string{"a.go:2", "a.go:7", "b.go:9"}
	for i, w := range wantOrder {
		if got[i].ID != []string{"c1", "c2", "c3"}[i] {
			t.Fatalf("id[%d] = %s", i, got[i].ID)
		}
		if key := got[i].File + ":" + itoa(got[i].Line); key != w {
			t.Fatalf("order[%d] = %s, want %s", i, key, w)
		}
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func TestConfirmedByTaskSplitsAttributedAndNot(t *testing.T) {
	t.Parallel()
	rec := &wave.InternalRecord{
		Candidates: []wave.Candidate{
			{
				ID: "c1",
				Finding: backend.Finding{
					Severity: "blocking",
					File:     "a.go",
					Line:     1,
					Title:    "x",
				},
				Task:   3,
				Lenses: []string{"intent"},
			},
			{
				ID: "c2",
				Finding: backend.Finding{
					Severity: "minor",
					File:     "z.go",
					Line:     2,
					Title:    "y",
				},
				Task:   0,
				Lenses: []string{"docs"},
			},
			{
				ID: "c3",
				Finding: backend.Finding{
					Severity: "major",
					File:     "b.go",
					Line:     3,
					Title:    "z",
				},
				Task:   3,
				Lenses: []string{"tests"},
			},
		},
		Confirmed: []string{"c1", "c2"},
	}
	byTask := rec.ConfirmedByTask()
	if len(byTask[3]) != 1 || byTask[3][0].Title != "x" {
		t.Fatalf("task 3 confirmed = %+v", byTask[3])
	}
	if len(byTask[0]) != 1 || byTask[0][0].Title != "y" {
		t.Fatalf("unattributed confirmed = %+v", byTask[0])
	}
}

func TestLensAndInternalRecordsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lr := *lensRec("docs", lf("nit", "README.md", 1, 0, "stale"))
	if err := wave.WriteLensRecord(dir, lr); err != nil {
		t.Fatal(err)
	}
	got, err := wave.ReadLensRecord(dir, 0, 1, 1, "docs")
	if err != nil || got == nil || got.Findings[0].Title != "stale" {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	r, errAbsent := wave.ReadLensRecord(dir, 0, 1, 2, "docs")
	if errAbsent != nil || r != nil {
		t.Fatalf("absent attempt must read nil,nil: %+v, %v", r, errAbsent)
	}
	ir := wave.InternalRecord{Wave: 0, Slice: 1, Attempt: 1, Confirmed: []string{}}
	if errWrite := wave.WriteInternalRecord(dir, ir); errWrite != nil {
		t.Fatal(errWrite)
	}
	gotInternal, errInternal := wave.ReadInternalRecord(dir, 0, 1, 1)
	if errInternal != nil || gotInternal == nil {
		t.Fatalf("internal round trip: %+v, %v", gotInternal, errInternal)
	}
	all, errAll := wave.AllInternalRecords(dir, 0)
	if errAll != nil || len(all) != 1 {
		t.Fatalf("AllInternalRecords = %v, %v", all, errAll)
	}
}
