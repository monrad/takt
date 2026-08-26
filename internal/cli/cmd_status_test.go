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

// TestStatusShowsFinishBlock covers task 7: once a run reaches the finish
// phase, status carries a "finish" block built from the finish/ records and
// the disposition — and that block is absent before the run gets there.
func TestStatusShowsFinishBlock(t *testing.T) {
	t.Parallel()
	d, _, _ := finishedRun(t)
	if code, _, errb := d.cmd("answer", "--gate", "branch_finish", "--choice", "keep", "--slug", "demo"); code != 0 {
		t.Fatal(errb)
	}
	if o := d.nextOp(); o["op"] != "stop" || o["reason"] != "archived" {
		t.Fatalf("%v", o)
	}
	code, got, errb := d.cmd("status", "--json", "--slug", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	fin, ok := got["finish"].(map[string]any)
	if !ok {
		t.Fatalf("no finish block: %v", got)
	}
	if sha, _ := fin["verified_sha"].(string); sha == "" {
		t.Fatalf("verified_sha empty: %v", fin)
	}
	if fin["disposition"] != "keep" {
		t.Fatalf("disposition = %v", fin["disposition"])
	}
	if fin["applied"] != true {
		t.Fatalf("applied = %v", fin["applied"])
	}

	var out strings.Builder
	cli.Main([]string{"status", "--slug", "demo"}, &out, &out, func(k string) string {
		if k == "HOME" {
			return d.root + "/.home"
		}
		return ""
	}, d.root)
	text := out.String()
	for _, want := range []string{"verify: passed at ", "disposition: keep (applied)"} {
		if !strings.Contains(text, want) {
			t.Errorf("text status lacks %q:\n%s", want, text)
		}
	}

	// A run still in execute phase carries no finish block.
	root2, _ := setupRun(t)
	code2, got2, errb2 := runIn(t, root2, nil, "status", "--json")
	if code2 != 0 {
		t.Fatalf("exit %d: %s", code2, errb2)
	}
	if _, hasFinish := got2["finish"]; hasFinish {
		t.Fatalf("execute phase must have no finish key: %v", got2)
	}
}

// TestStatusShowsTheSessionHolder covers the status surface of the sidecar
// (spec §11): who holds the run and how long ago they last called, read from
// logs/session.json rather than from state.json, and `none` once the lock is
// released.
func TestStatusShowsTheSessionHolder(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	next(t, root, map[string]string{"TAKT_SESSION": "S"})
	c, r, e := runIn(t, root, nil, "status", "--json", "--slug", "demo")
	if c != 0 {
		t.Fatal(e)
	}
	sess, _ := r["session"].(map[string]any)
	if sess["id"] != "S" || sess["host"] == "" || sess["heartbeat"] == nil || sess["age"] == nil {
		t.Fatalf("session block: %v", r["session"])
	}
	if out := statusText(t, root); !strings.Contains(out, "session: S@") {
		t.Fatalf("text status must name the holder:\n%s", out)
	}
	runIn(t, root, nil, "unlock", "--slug", "demo")
	if _, r, _ = runIn(t, root, nil, "status", "--json", "--slug", "demo"); r["session"] != nil {
		t.Fatalf("a free run has session null: %v", r["session"])
	}
	if out := statusText(t, root); !strings.Contains(out, "session: none") {
		t.Fatalf("text status after unlock:\n%s", out)
	}
}
