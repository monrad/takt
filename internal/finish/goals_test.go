package finish_test

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParseVerdictsReturnsGoalsMdOrder(t *testing.T) {
	t.Parallel()
	ids := []string{"G1", "G2", "G3"}
	shuffled := []byte(`[{"id":"G3","verdict":"missed","evidence":"nothing"},
	                     {"id":"G1","verdict":"achieved","evidence":"go test passed"},
	                     {"id":"G2","verdict":"partial","evidence":"docs missing"}]`)
	vs, err := finish.ParseVerdicts(shuffled, ids)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vs {
		if v.ID != ids[i] {
			t.Fatalf("verdict %d is %s, want %s: %+v", i, v.ID, ids[i], vs)
		}
	}
	u := finish.GoalsRecord{Verdicts: vs}.Unmet()
	if len(u) != 2 || u[0].ID != "G2" || u[1].ID != "G3" {
		t.Fatalf("Unmet must follow goals.md order: %+v", u)
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

// citationRoot builds the tree the citation tests judge against: a 3-line
// `a.go`, a 1-line `..foo.go` (a contained file whose name starts with the
// forbidden segment), a `dir` holding a 1-line `b.go`, an empty directory
// `d`, and `out.go` — an in-repo symlink to a file in another directory
// altogether. It returns the root.
func citationRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n\nfunc A() {}\n")
	write("..foo.go", "package a\n")
	write("dir/b.go", "package b\n")
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out.go")); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestCitationGrammarAndContainment covers spec §E: a citation is
// `path:line` or `path:start-end`, repo-relative, resolving to a regular
// file inside the repository, with the range inside that file.
func TestCitationGrammarAndContainment(t *testing.T) {
	t.Parallel()
	root := citationRoot(t)
	// `dir\..\a.go` is written as a Go literal so the backslashes are real:
	// on Windows it is a traversal, on Linux one odd file name — and the
	// segment rule rejects it on both.
	const backslashed = `dir\..\a.go:1`
	cases := []struct {
		citation string
		want     string // "" when the citation is good
	}{
		{"a.go:2", ""},
		{"a.go:1-3", ""},
		{"dir/b.go:1", ""},
		{"..foo.go:1", ""}, // a contained file, not a traversal
		{backslashed, "escapes the repository"},
		{"a.go:4", "line 4 is past the end (3 lines)"},
		{"a.go:0", "line 0 is not a line"},
		{"a.go:3-2", "not path:line or path:start-end"},
		{"a.go", "not path:line or path:start-end"},
		{"a.go:x", "not path:line or path:start-end"},
		{"/etc/passwd:1", "escapes the repository"},
		{"../a.go:1", "escapes the repository"},
		{"d:1", "not a file"},
		{"missing.go:1", "not a file"},
		{"out.go:1", "resolves outside the repository"},
	}
	for _, c := range cases {
		vs := []finish.GoalVerdict{{ID: "G1", Verdict: "achieved", Evidence: "x", Citations: []string{c.citation}}}
		got := finish.CheckCitations(vs, root)
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("%s must be accepted, got %v", c.citation, got)
			}
			continue
		}
		if len(got) != 1 {
			t.Errorf("%s: want one problem, got %v", c.citation, got)
			continue
		}
		if !strings.HasPrefix(got[0], "G1: ") || !strings.Contains(got[0], `"`+c.citation+`"`) {
			t.Errorf("%s: a problem names the goal and quotes the citation: %q", c.citation, got[0])
		}
		if !strings.HasSuffix(got[0], c.want) {
			t.Errorf("%s: problem %q must end in %q", c.citation, got[0], c.want)
		}
	}
}

// TestCheckCitationsOrdersProblemsAndAllowsAnEmptyList pins the two things
// the caller depends on: no citations is not a violation, and the problems
// come back in verdict order and then citation order, so the assessor reads
// them in the order it wrote the list.
func TestCheckCitationsOrdersProblemsAndAllowsAnEmptyList(t *testing.T) {
	t.Parallel()
	root := citationRoot(t)
	empty := []finish.GoalVerdict{
		{ID: "G1", Verdict: "achieved", Evidence: "the suite passed", Citations: []string{}},
		{ID: "G2", Verdict: "missed", Evidence: "nothing does it", Citations: nil},
	}
	if got := finish.CheckCitations(empty, root); len(got) != 0 {
		t.Fatalf("an empty citations list is allowed: %v", got)
	}
	vs := []finish.GoalVerdict{
		{ID: "G1", Verdict: "achieved", Evidence: "x", Citations: []string{"a.go:1", "a.go:9"}},
		{ID: "G2", Verdict: "partial", Evidence: "x", Citations: []string{"missing.go:1"}},
	}
	got := finish.CheckCitations(vs, root)
	if len(got) != 2 || !strings.HasPrefix(got[0], `G1: citation "a.go:9"`) ||
		!strings.HasPrefix(got[1], `G2: citation "missing.go:1"`) {
		t.Fatalf("problems in verdict then citation order: %v", got)
	}
}
