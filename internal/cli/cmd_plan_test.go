package cli_test

import (
	"os"
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
