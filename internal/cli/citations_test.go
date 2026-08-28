package cli_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// atAssessorDispatch drives a fresh run (goals on) to the goal-assessor
// dispatch: through the whole loop, past `takt verify`, to the `takt next`
// that hands the assessor its brief. Each citation case builds its own,
// because three rejections in one run turn the next `next` into an
// `agent_invalid` ask (spec §4.4) and the case after that would be asserting
// against the cap rather than against the citation.
func atAssessorDispatch(t *testing.T) (*driver, string) {
	t.Helper()
	d, bdir := finishRun(t)
	driveToFinish(t, d)
	if code, _, errb := d.cmd("verify", "--slug", "demo"); code != 0 {
		t.Fatalf("verify: %d %s", code, errb)
	}
	o := d.nextOp()
	if ag := agentsOf(t, o); o["op"] != "dispatch" || len(ag) != 1 || ag[0]["agent"] != "goal-assessor" {
		t.Fatalf("expected the goal-assessor dispatch: %v", o)
	}
	return d, bdir
}

// assessorMessage writes the assessor's final message for one verdict on G1
// carrying exactly these citations, and returns its path. The citations are
// marshalled rather than pasted, so a backslash in one arrives as the agent
// would really have written it.
func assessorMessage(t *testing.T, d *driver, verdict string, citations []string) string {
	t.Helper()
	cs, err := json.Marshal(citations)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("```json\n[{\"id\":\"G1\",\"verdict\":%q,\"evidence\":\"ran it\",\"citations\":%s}]\n```\n",
		verdict, cs)
	return d.message(body)
}

// TestRecordGoalsRejectsBadCitations covers spec §E and goal G6: a reply
// whose citation is not `path:line`/`path:start-end`, names a path that
// escapes the repository or is not a regular file, resolves through a
// symlink to a file outside the tree, or cites a line past the end is
// rejected the way any unusable reply is — {valid:false, problems} at exit
// 0, one goals_invalid event, no goal record — and the assessor is asked
// again with the problem quoted in its brief.
func TestRecordGoalsRejectsBadCitations(t *testing.T) {
	t.Parallel()
	// The backslash case is a Go literal so the backslashes are real: the
	// `..` segment rule splits on both separators, so it is rejected
	// wherever takt runs.
	cases := []struct{ name, citation string }{
		{"absolute", "/etc/passwd:1"},
		{"traversal", "../x.go:1"},
		{"backslash traversal", `docs\..\a.go:1`},
		{"a directory", "docs:1"},
		{"past the end", "a.go:99"},
		{"no line", "a.go"},
		{"symlink out of the repository", symlinkCitation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assertCitationRejected(t, c.citation)
		})
	}
	t.Run("bad verdict and bad citation", func(t *testing.T) {
		t.Parallel()
		assertUnusableListIsRejectedAlone(t)
	})
}

// symlinkCitation is the case that needs a fixture of its own: an in-repo
// symlink whose target is a file in another directory altogether, so the
// path is lexically contained and still outside the tree.
const symlinkCitation = "link.go:1"

// assertCitationRejected records one bad citation against a fresh run and
// checks the whole rejection contract: exit 0 with {valid:false}, one
// problem naming the goal and the citation, no goal record, one
// goals_invalid event, and the assessor asked again with the problem quoted.
func assertCitationRejected(t *testing.T, citation string) {
	t.Helper()
	d, bdir := atAssessorDispatch(t)
	if citation == symlinkCitation {
		plantSymlink(t, d.root, "link.go")
	}
	msg := assessorMessage(t, d, "achieved", []string{citation})
	code, got, errb := d.cmd("record", "--agent", "goal-assessor", "--from", msg, "--slug", "demo")
	if code != 0 {
		t.Fatalf("a rejected assessment is a document, not a failure: %d %s", code, errb)
	}
	if got["valid"] != false {
		t.Fatalf("%s must be rejected: %v", citation, got)
	}
	problem := onlyProblemNaming(t, got, citation)
	if _, err := os.Stat(filepath.Join(bdir, "finish", "goals.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a rejected assessment must write no record: %v", err)
	}
	if n := countEvents(t, bdir, "goals_invalid"); n != 1 {
		t.Fatalf("one rejection appends one goals_invalid, got %d", n)
	}
	assertReAsked(t, d, problem)
}

// plantSymlink puts a symlink at root/name pointing at a file outside the
// repository — the case lexical containment cannot catch.
func plantSymlink(t *testing.T, root, name string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
}

// assertUnusableListIsRejectedAlone pins the ordering spec §E states:
// ParseVerdicts returns one error and no verdicts when the list itself is
// unusable, so there is nothing left to resolve citations against and the
// reply is rejected on that one problem — the citation is never mentioned.
func assertUnusableListIsRejectedAlone(t *testing.T) {
	t.Helper()
	d, _ := atAssessorDispatch(t)
	msg := assessorMessage(t, d, "maybe", []string{"a.go:99"})
	code, got, errb := d.cmd("record", "--agent", "goal-assessor", "--from", msg, "--slug", "demo")
	if code != 0 || got["valid"] != false {
		t.Fatalf("%d %v %s", code, got, errb)
	}
	problems := problemList(t, got)
	if len(problems) != 1 || !strings.Contains(problems[0], "maybe") {
		t.Fatalf("an unusable list is rejected on its own problem: %v", problems)
	}
	if strings.Contains(problems[0], "a.go:99") {
		t.Fatalf("no verdicts parsed, so no citation was checked: %v", problems)
	}
}

// TestRecordGoalsAcceptsWellFormedCitations is the other half of G6: a
// citation into a real file — a single line or a range — and an empty
// citations list are all accepted, and the goal record is written.
func TestRecordGoalsAcceptsWellFormedCitations(t *testing.T) {
	t.Parallel()
	// a.go is what the scripted implementer wrote for the fixture plan's
	// task 1: one line, so `a.go:1` and `a.go:1-1` are both in range.
	cases := []struct {
		name      string
		citations []string
	}{
		{"a line", []string{"a.go:1"}},
		{"a range", []string{"a.go:1-1"}},
		{"no citations", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, bdir := atAssessorDispatch(t)
			msg := assessorMessage(t, d, "achieved", c.citations)
			code, got, errb := d.cmd("record", "--agent", "goal-assessor", "--from", msg, "--slug", "demo")
			if code != 0 || got["valid"] == false || got["all_achieved"] != true {
				t.Fatalf("%v must be accepted: %d %v %s", c.citations, code, got, errb)
			}
			if _, err := os.Stat(filepath.Join(bdir, "finish", "goals.json")); err != nil {
				t.Fatalf("an accepted assessment writes the record: %v", err)
			}
		})
	}
}

// problemList is the problems a rejected `record --agent` reported, as text.
func problemList(t *testing.T, got map[string]any) []string {
	t.Helper()
	raw, ok := got["problems"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("a rejection must say what is wrong: %v", got)
	}
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		s, isText := p.(string)
		if !isText {
			t.Fatalf("a problem must be text: %v", p)
		}
		out = append(out, s)
	}
	return out
}

// onlyProblemNaming asserts the rejection is one problem naming both the
// goal and the citation — the assessor is told which citation of which
// verdict it has to fix — and returns it.
func onlyProblemNaming(t *testing.T, got map[string]any, citation string) string {
	t.Helper()
	problems := problemList(t, got)
	if len(problems) != 1 {
		t.Fatalf("one bad citation is one problem: %v", problems)
	}
	if !strings.Contains(problems[0], "G1") || !strings.Contains(problems[0], citation) {
		t.Fatalf("the problem must name the goal and the citation: %q", problems[0])
	}
	return problems[0]
}

// assertReAsked checks the rejection left the dispatch pending: the next
// `takt next` hands the assessor its brief again, with the problem quoted
// in it (spec §5.3 row 21).
func assertReAsked(t *testing.T, d *driver, problem string) {
	t.Helper()
	o := d.nextOp()
	ag := agentsOf(t, o)
	if o["op"] != "dispatch" || len(ag) != 1 || ag[0]["agent"] != "goal-assessor" {
		t.Fatalf("a rejected record leaves the dispatch pending: %v", o)
	}
	path, ok := ag[0]["brief"].(string)
	if !ok {
		t.Fatalf("dispatch without a brief: %v", ag[0])
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), problem) {
		t.Fatalf("the retry brief must quote %q:\n%s", problem, b)
	}
}
