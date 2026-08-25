package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
