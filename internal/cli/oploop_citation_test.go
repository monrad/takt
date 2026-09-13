package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// greetIndex is a plan that shares no file name with validIndex: the scripted
// implementer writes whatever a plan declares, so the walk runs exactly as
// the fixture's does — except that nothing in the tree is called a.go.
const greetIndex = `{"schema":1,"spec_hash":"%s","tasks":[
 {"id":1,"title":"greet","description":"add greet.go","files":["greet.go"],"verify":["true"],"depends_on":[],"goals":["G1"],"class":"bounded"},
 {"id":2,"title":"test","description":"add greet_test.go","files":["greet_test.go"],"verify":["true"],"depends_on":[1],"goals":["G1"],"class":"implement"}]}`

// TestScriptedAssessorCitesTheRunsOwnFiles is the live end-to-end run's
// failure (issue #21) made hermetic. `takt record --agent goal-assessor`
// resolves every citation against the tree (finish.CheckCitations), so the
// driver's scripted assessment has to cite a file the run it is driving
// really wrote — the first file the plan declares — rather than a name the
// default fixture happens to use. Under a plan that declares no a.go, a
// citation of a.go:1 is rejected as "not a file" and the walk never reaches
// the archive; the live test's greet module is exactly such a plan.
func TestScriptedAssessorCitesTheRunsOwnFiles(t *testing.T) {
	t.Parallel()
	root, bdir := setupRun(t)
	d := &driver{t: t, root: root, bdir: bdir, env: map[string]string{"TAKT_SESSION": "S"}, plan: greetIndex}
	if reason := d.play(80); reason != stopArchived {
		t.Fatalf("the run must end archived, stopped %q (ops: %v)", reason, d.ops)
	}
	rec, err := os.ReadFile(filepath.Join(bdir, "finish", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec), `"greet.go:1"`) {
		t.Fatalf("the assessment must cite the file the plan declared, got:\n%s", rec)
	}
}
