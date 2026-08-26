package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/testutil"
)

func TestPlanValidateFixture(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	src, _ := os.ReadFile("../plan/testdata/cedar-like.json")
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", string(src))
	code, got, errb := runIn(t, root, nil, "plan", "validate")
	if code != 0 {
		t.Fatalf("exit %d: %s / %v", code, errb, got)
	}
	if got["valid"] != true || got["tasks"] != float64(8) {
		t.Fatalf("out = %v", got)
	}
	waves := got["waves"].(map[string]any)
	if waves["6"] != float64(2) {
		t.Fatalf("waves = %v", waves)
	}
}

func TestPlanValidateReportsProblems(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json",
		`{"schema":1,"spec_hash":"x","tasks":[{"id":1,"title":"a","description":"d","files":["/abs.go"],"verify":[]}]}`)
	code, got, _ := runIn(t, root, nil, "plan", "validate")
	if code != 1 || got["valid"] != false {
		t.Fatalf("invalid plan must exit 1 with valid:false: %d %v", code, got)
	}
	problems := got["problems"].([]any)
	var jb strings.Builder
	for _, p := range problems {
		jb.WriteString(p.(string) + "\n")
	}
	joined := jb.String()
	if !strings.Contains(joined, "absolute") || !strings.Contains(joined, "verify") {
		t.Fatalf("problems = %s", joined)
	}
}

func TestPlanValidateUsesSpecHashAndGoals(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	testutil.WriteFile(
		t,
		root,
		"docs/takt/demo/goals.md",
		"# Goals — demo\n\n## Anchor\n```text\ntopic\n```\n\n## Goals\n- G1 — it works · signal: test · evidence: go test\n",
	)
	testutil.WriteFile(
		t,
		root,
		"docs/takt/demo/plan.index.json",
		`{"schema":1,"spec_hash":"sha256:stale","tasks":[{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"],"goals":["G7"]}]}`,
	)
	code, got, _ := runIn(t, root, nil, "plan", "validate")
	var jb strings.Builder
	for _, p := range got["problems"].([]any) {
		jb.WriteString(p.(string) + "\n")
	}
	joined := jb.String()
	if code != 1 || !strings.Contains(joined, "spec_hash") || !strings.Contains(joined, "unknown goal G7") ||
		!strings.Contains(joined, "G1 is served by no task") {
		t.Fatalf("%d %s", code, joined)
	}
}

// TestPlanValidateRejectsInvalidSlug covers review finding 1 for the second
// --slug consumer.
func TestPlanValidateRejectsInvalidSlug(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	code, _, errb := runIn(t, root, nil, "plan", "validate", "--slug", "../../escaped")
	if code != 2 || !strings.Contains(errb, "slug") {
		t.Fatalf("exit %d, want 2 with a slug error: %s", code, errb)
	}
}

// TestPlanValidateAcceptsPathBeforeFlags covers review finding 2 for
// `takt plan validate [path]`.
func TestPlanValidateAcceptsPathBeforeFlags(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	src, err := os.ReadFile("../plan/testdata/cedar-like.json")
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, root, "elsewhere/plan.index.json", string(src))
	// The positional path is resolved against the process cwd, so it must be
	// absolute here; what this test pins is the argument *order*.
	abs := filepath.Join(root, "elsewhere", "plan.index.json")
	code, got, errb := runIn(t, root, nil, "plan", "validate", abs, "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["tasks"] != float64(8) {
		t.Fatalf("out = %v", got)
	}
}

// TestRecordPlannerRequiresPlanMD covers review M8 and its residual N0: the
// planner writes plan.md as well as plan.index.json (spec §13), and the plan
// gate hashes it. A planner that wrote only the index must be invalid at the
// seam `takt next` decides from — not only in `takt record`'s answer. Judged
// in record alone, the index still read valid to Decide, which took row 9
// and emitted `exec takt review plan`; that command dies in gate.Hash on the
// file nobody wrote, and dies again every turn, because nothing counts a
// planner attempt or re-dispatches the planner. So the walk below asserts
// both halves: what `record` says, and what the loop does next.
func TestRecordPlannerRequiresPlanMD(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	specH := specHash(t, bdir)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", strings.Replace(validIndex, "%s", specH, 1))
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Phase = bundle.PhasePlan
	st.Config.Alignment = false // the plan review gate is what this test is about
	if serr := bundle.SaveState(bdir, st); serr != nil {
		t.Fatal(serr)
	}

	code, out, errb := runIn(t, root, nil, "record", "--agent", "planner", "--from", "/dev/null", "--slug", "demo")
	if code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if out["valid"] != false {
		t.Fatalf("an index with no plan.md is not a valid plan: %v", out)
	}
	problems, ok := out["problems"].([]any)
	if !ok || len(problems) == 0 || !strings.Contains(fmt.Sprint(problems), "plan.md") {
		t.Fatalf("the reason must name plan.md: %v", out)
	}

	// The decision seam: an invalid plan goes back to the planner (row 8),
	// counting the attempt. It must never reach `exec takt review plan`.
	_, o, _ := next(t, root, nil)
	if o["op"] != "dispatch" {
		t.Fatalf("an index with no plan.md must re-dispatch the planner, got %v", o)
	}
	ag := agentsOf(t, o)[0]
	if ag["agent"] != "planner" {
		t.Fatalf("%v", ag)
	}
	if brief, bok := ag["brief"].(string); !bok || !strings.HasSuffix(brief, "planner.a2.md") {
		t.Fatalf("the planner attempt must be counted, got brief %v", ag["brief"])
	} else if b, rerr := os.ReadFile(brief); rerr != nil || !strings.Contains(string(b), "plan.md is missing or empty") {
		t.Fatalf("the planner must be told what it left out: %v %s", rerr, b)
	}

	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan\n")
	if code, out, errb = runIn(t, root, nil,
		"record", "--agent", "planner", "--from", "/dev/null", "--slug", "demo"); code != 0 || out["valid"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, o, _ = next(t, root, nil); o["op"] != "exec" ||
		!strings.HasPrefix(o["command"].(string), "takt review plan") {
		t.Fatalf("a complete plan moves on to the plan review: %v", o)
	}
	if rc, _, rerrb := runIn(t, root, nil, "review", "plan", "--slug", "demo"); rc != 0 {
		t.Fatalf("and that review must actually run: %s", rerrb)
	}
}

// TestRecordPlannerStampsSpecHash covers review fix round 1: the planner
// has no Bash and no way to compute a sha256, so whatever it writes for
// spec_hash — empty or a wrong guess — must be discarded and replaced with
// takt's own hash of spec.md when the plan is recorded, not treated as a
// validation failure.
func TestRecordPlannerStampsSpecHash(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, agentHash string }{
		{"empty", ""},
		{"wrong", "sha256:not-the-real-hash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root, bdir := setupRun(t)
			testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
			testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan\n")
			testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json",
				strings.Replace(validIndex, "%s", c.agentHash, 1))

			code, out, errb := runIn(t, root, nil,
				"record", "--agent", "planner", "--from", "/dev/null", "--slug", "demo")
			if code != 0 || out["valid"] != true {
				t.Fatalf("%d %v %s", code, out, errb)
			}

			raw, err := os.ReadFile(filepath.Join(bdir, "plan.index.json"))
			if err != nil {
				t.Fatal(err)
			}
			var idx map[string]any
			if uerr := json.Unmarshal(raw, &idx); uerr != nil {
				t.Fatal(uerr)
			}
			if want := specHash(t, bdir); idx["spec_hash"] != want {
				t.Fatalf("spec_hash on disk = %v, want takt's own hash %s", idx["spec_hash"], want)
			}
		})
	}
}
