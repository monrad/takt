package plan_test

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/plan"
)

// maxFilesPerTaskForTest matches ValidateOpts.MaxFilesPerTask used across
// these cases; named to satisfy mnd.
const maxFilesPerTaskForTest = 12

func opts(t *testing.T) plan.ValidateOpts {
	t.Helper()
	return plan.ValidateOpts{
		RepoRoot:        t.TempDir(),
		MaxFilesPerTask: maxFilesPerTaskForTest,
		GoalIDs:         []string{"G1", "G2", "G3", "G4", "G5", "G6"},
		SpecHash:        "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		LookPath:        func(tok string) bool { return tok == "go" || tok == "true" },
	}
}

func hasProblem(ps []plan.Problem, taskID int, substr string) bool {
	for _, p := range ps {
		if p.TaskID == taskID && strings.Contains(p.Message, substr) {
			return true
		}
	}
	return false
}

func TestFixtureIsValid(t *testing.T) {
	t.Parallel()
	if ps := plan.Validate(loadFixture(t), opts(t)); len(ps) != 0 {
		t.Fatalf("expected no problems, got %v", ps)
	}
}

func TestValidateRules(t *testing.T) {
	t.Parallel()
	const tooManyFiles = 13
	cases := []struct {
		name   string
		mutate func(*plan.Index)
		taskID int
		want   string
	}{
		{"schema", func(i *plan.Index) { i.Schema = 2 }, 0, "schema"},
		{"ids not 1..n", func(i *plan.Index) { i.Tasks[7].ID = 42 }, 0, "1..n"},
		{"empty title", func(i *plan.Index) { i.Tasks[0].Title = "" }, 1, "title"},
		{"no files", func(i *plan.Index) { i.Tasks[0].Files = nil }, 1, "files"},
		{"absolute file", func(i *plan.Index) { i.Tasks[0].Files = []string{"/etc/passwd"} }, 1, "absolute"},
		{"escaping file", func(i *plan.Index) { i.Tasks[0].Files = []string{"../x.go"} }, 1, "escapes"},
		{"too many files", func(i *plan.Index) {
			i.Tasks[0].Files = make([]string, tooManyFiles)
			for k := range i.Tasks[0].Files {
				i.Tasks[0].Files[k] = "f" + string(rune('a'+k)) + ".go"
			}
		}, 1, "at most 12"},
		{
			"mechanical too big",
			func(i *plan.Index) { i.Tasks[1].Files = []string{"a", "b", "c", "d"} },
			2,
			"mechanical",
		},
		{"no verify", func(i *plan.Index) { i.Tasks[0].Verify = nil }, 1, "verify"},
		{
			"verify not on PATH",
			func(i *plan.Index) { i.Tasks[0].Verify = []string{"frobnicate --all"} },
			1,
			"not found on PATH",
		},
		{"unknown dep", func(i *plan.Index) { i.Tasks[0].DependsOn = []int{99} }, 1, "unknown task 99"},
		{"self dep", func(i *plan.Index) { i.Tasks[0].DependsOn = []int{1} }, 1, "itself"},
		{"cycle", func(i *plan.Index) { i.Tasks[0].DependsOn = []int{6} }, 0, "cycle"},
		{
			"overlap without order",
			func(i *plan.Index) { i.Tasks[7].Files = []string{"lib/go/cedar/schema/applicability.go"} },
			8,
			"share",
		},
		{"unknown goal", func(i *plan.Index) { i.Tasks[0].Goals = []string{"G9"} }, 1, "unknown goal"},
		{"goal unserved", func(i *plan.Index) { i.Tasks[7].Goals = []string{"G1"} }, 0, "G6"},
		{"unknown class", func(i *plan.Index) { i.Tasks[0].Class = "magic" }, 1, "class"},
		{"stale spec hash", func(i *plan.Index) { i.SpecHash = "sha256:ffff" }, 0, "spec_hash"},
		{"unrecorded spec hash", func(i *plan.Index) { i.SpecHash = "" }, 0, "not yet recorded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			idx := loadFixture(t)
			c.mutate(&idx)
			ps := plan.Validate(idx, opts(t))
			if !hasProblem(ps, c.taskID, c.want) {
				t.Fatalf("want problem on task %d containing %q, got %v", c.taskID, c.want, ps)
			}
		})
	}
}

// TestValidateSeparatesUnrecordedFromStaleSpecHash covers the review's M5:
// an index with no spec_hash at all has not been stamped yet — `takt record
// --agent planner` is what writes it — and reporting that as "drafted
// against an older spec" sends the reader looking for a drift between two
// specs when there is only one. A hash that is present and wrong still is
// drift, and still says so.
func TestValidateSeparatesUnrecordedFromStaleSpecHash(t *testing.T) {
	t.Parallel()
	idx := loadFixture(t)
	idx.SpecHash = ""
	ps := plan.Validate(idx, opts(t))
	if !hasProblem(ps, 0, "spec_hash not yet recorded — run `takt record --agent planner`") {
		t.Fatalf("an unstamped index must say so: %v", ps)
	}
	if hasProblem(ps, 0, "older spec") {
		t.Fatalf("an unstamped index must not be reported as drift: %v", ps)
	}

	idx.SpecHash = "sha256:ffff"
	if ps = plan.Validate(idx, opts(t)); !hasProblem(ps, 0, "older spec") {
		t.Fatalf("a spec_hash that is present and wrong is still drift: %v", ps)
	}
}

func TestOverlapWithTransitiveOrderIsFine(t *testing.T) {
	t.Parallel()
	idx := loadFixture(t)
	// task 6 depends on 5 which depends on 1; sharing a file between 6 and 1 is ordered.
	idx.Tasks[5].Files = append(idx.Tasks[5].Files, "lib/go/cedar/schema/applicability.go")
	if ps := plan.Validate(idx, opts(t)); len(ps) != 0 {
		t.Fatalf("transitively ordered overlap must be accepted: %v", ps)
	}
}

func TestValidateSkipsOptionalChecks(t *testing.T) {
	t.Parallel()
	idx := loadFixture(t)
	idx.SpecHash = "whatever"
	idx.Tasks[0].Goals = []string{"G9"}
	idx.Tasks[0].Verify = []string{"frobnicate"}
	o := plan.ValidateOpts{RepoRoot: t.TempDir(), MaxFilesPerTask: maxFilesPerTaskForTest}
	if ps := plan.Validate(idx, o); len(ps) != 0 {
		t.Fatalf("nil GoalIDs/LookPath and empty SpecHash must skip those checks: %v", ps)
	}
}

// TestValidateReportsDuplicateFileAsItsOwnProblem covers the review's minor
// finding: a file listed twice in one task used to surface as the nonsense
// "tasks 1 and 1 share x.go but neither depends on the other", which points
// the planner at a dependency it cannot add.
func TestValidateReportsDuplicateFileAsItsOwnProblem(t *testing.T) {
	t.Parallel()
	idx := loadFixture(t)
	dup := idx.Tasks[0].Files[0]
	idx.Tasks[0].Files = append(idx.Tasks[0].Files, dup)

	ps := plan.Validate(idx, opts(t))
	if !hasProblem(ps, 1, "duplicate file "+dup) {
		t.Fatalf("want a duplicate-file problem, got %v", ps)
	}
	for _, p := range ps {
		if strings.Contains(p.Message, "tasks 1 and 1") {
			t.Fatalf("a task must never be reported as sharing a file with itself: %v", ps)
		}
	}
	if len(ps) != 1 {
		t.Fatalf("a duplicate must produce exactly one problem, got %v", ps)
	}
}
