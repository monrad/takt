package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/testutil"
)

func TestStatusSingleBundle(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 0 {
		t.Fatal(errb)
	}
	testutil.WriteFile(
		t,
		root,
		"docs/takt/demo/goals.md",
		"# Goals — demo\n\n## Anchor\n```text\ntopic\n```\n\n## Goals\n- G1 — it works · signal: test · evidence: go test\n",
	)
	code, got, errb := runIn(t, root, nil, "status", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["slug"] != "demo" || got["phase"] != "brainstorm" || got["branch"] != "takt/demo" {
		t.Fatalf("out = %v", got)
	}
	tasks := got["tasks"].(map[string]any)
	if tasks["total"] != float64(0) {
		t.Fatalf("tasks = %v", tasks)
	}
	goals := got["goals"].([]any)
	if len(goals) != 1 || goals[0].(map[string]any)["id"] != "G1" {
		t.Fatalf("goals = %v", goals)
	}
	if _, ok := got["gates_live"]; !ok {
		t.Fatal("live gate status missing")
	}

	var out strings.Builder
	cli.Main([]string{"status"}, &out, &out, func(k string) string {
		if k == "HOME" {
			return root + "/.home"
		}
		return ""
	}, root)
	text := out.String()
	for _, want := range []string{"demo", "brainstorm", "takt/demo", "G1"} {
		if !strings.Contains(text, want) {
			t.Errorf("text status lacks %q:\n%s", want, text)
		}
	}
}

func TestStatusNeedsSlugWhenSeveral(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	// No checkout between the two inits: staying on takt/one means the
	// second init adopts it (spec D9) instead of cutting takt/two, so both
	// bundles' state.json land in the same working tree and are visible to
	// ListSlugs together — the precondition this test needs. Checking out
	// main in between (as an earlier draft of this test did) instead makes
	// the second init create its own branch; ordinary git checkout then
	// removes docs/takt/one from the working tree (it was only committed on
	// takt/one), so the "several active" precondition is never reached.
	runIn(t, root, nil, "init", "--slug", "one", "t")
	runIn(t, root, nil, "init", "--slug", "two", "t")
	code, _, errb := runIn(t, root, nil, "status", "--json")
	if code != 1 || !strings.Contains(errb, "one") || !strings.Contains(errb, "two") {
		t.Fatalf("several bundles must ask for --slug: %d %s", code, errb)
	}
	slugCode, got, _ := runIn(t, root, nil, "status", "--json", "--slug", "one")
	if slugCode != 0 || got["slug"] != "one" {
		t.Fatalf("--slug must select: %d %v", slugCode, got)
	}
}

func TestStatusNoRun(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "status", "--json"); code != 1 || !strings.Contains(errb, "no active run") {
		t.Fatalf("%d %s", code, errb)
	}
}

// TestStatusAlignmentContradictedIsContraction covers fix round 1: spec
// §7.3 ("narrowed/dropped/contradicted are reported as contraction, widened
// as creep") named "contradicted" as a contraction verdict alongside
// narrowed/dropped; statusAlignment must bucket it there too, not drop it
// from both lists.
func TestStatusAlignmentContradictedIsContraction(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	verdicts := filepath.Join(t.TempDir(), "verdicts.txt")
	body := "```json\n" +
		`{"mode":"verdicts","verdicts":[` +
		`{"id":"A1","verdict":"covered","evidence":"task 1"},` +
		`{"id":"A2","verdict":"contradicted","evidence":"plan does the opposite of A2"}]}` +
		"\n```\n"
	if err := os.WriteFile(verdicts, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := runIn(
		t,
		root,
		nil,
		"record",
		"--agent",
		"alignment-auditor",
		"--mode",
		"verdicts",
		"--from",
		verdicts,
		"--slug",
		"demo",
	); code != 0 {
		t.Fatal(errb)
	}
	code, got, errb := runIn(t, root, nil, "status", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	alignment := got["alignment"].(map[string]any)
	counts := alignment["counts"].(map[string]any)
	if counts["contradicted"] != float64(1) {
		t.Fatalf("counts = %v", counts)
	}
	contraction := alignment["contraction"].([]any)
	if len(contraction) != 1 || contraction[0] != "A2" {
		t.Fatalf("contraction = %v, want just [A2]", contraction)
	}
	if creep, ok := alignment["creep"].([]any); ok && len(creep) != 0 {
		t.Fatalf("A2 must not also land in creep: %v", creep)
	}
}

// TestStatusRejectsInvalidSlug covers review finding 1: every command that
// takes --slug must reject a bad value before touching the filesystem, not
// just init.
func TestStatusRejectsInvalidSlug(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 0 {
		t.Fatal(errb)
	}
	code, _, errb := runIn(t, root, nil, "status", "--json", "--slug", "My Feature")
	if code != 2 || !strings.Contains(errb, "slug") {
		t.Fatalf("exit %d, want 2 with a slug error: %s", code, errb)
	}
}
