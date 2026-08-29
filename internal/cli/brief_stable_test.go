//nolint:testpackage // tests an unexported helper
package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/op"
)

// TestReuseBriefTokenFallsBackWhenTheRenderRefusesTheOldToken pins the one
// fallback that is not a missing file: the brief on disk carries a token,
// but the content has since grown that very token — a rejected agent reply
// quoted back on a retry is the way it happens — so brief.Quote refuses to
// delimit with it and the re-render fails. Reusing it anyway would hand the
// agent a brief whose END marker sits in the middle of the data, so the
// helper reports no reuse and writeStableBrief writes its fresh-token render
// instead (spec §5.4).
func TestReuseBriefTokenFallsBackWhenTheRenderRefusesTheOldToken(t *testing.T) {
	t.Parallel()
	const tok = "UNTRUSTED-ARTIFACT-00112233445566aa"
	p := filepath.Join(t.TempDir(), "planner.a1.md")
	body := "BEGIN " + tok + " spec.md\nthe agent echoed " + tok + " back\nEND " + tok + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := ""
	refuse := func(want string) (string, string, error) {
		asked = want
		return "", "", errors.New("brief: delimiter token collides with the content; regenerate the token")
	}
	text, unchanged := reuseBriefToken(p, refuse)
	if text != "" || unchanged {
		t.Fatalf(`reuseBriefToken = (%q, %v), want ("", false)`, text, unchanged)
	}
	if asked != tok {
		t.Fatalf("the re-render must be asked for the token on disk: %q != %q", asked, tok)
	}
}

// TestWriteStableBriefRendersOnce pins the render count. Writing a brief
// used to render it three times — once for the name, once for the fresh
// text, once with the token on disk — of which the first two were the same
// render, and a brief render is not free: the planner's reads the whole
// bundle's facts (#51). The first write renders once; a replay renders
// twice, the fresh one and the byte comparison against the file, and writes
// nothing.
func TestWriteStableBriefRendersOnce(t *testing.T) {
	t.Parallel()
	const tok = "UNTRUSTED-ARTIFACT-0011223344556677"
	body := "BEGIN " + tok + " spec.md\nthe spec\nEND " + tok + "\n"
	dir := t.TempDir()
	renders := 0
	// The render ignores the token it is handed, so the re-render with the
	// token on disk reproduces the file and the replay is a no-op write.
	render := func(string) (string, string, error) {
		renders++
		return body, "planner.a1.md", nil
	}
	p, err := writeStableBrief(dir, render)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "briefs", "planner.a1.md") {
		t.Fatalf("brief path %q", p)
	}
	first, err := os.ReadFile(p)
	if err != nil || string(first) != body {
		t.Fatalf("%v %q", err, first)
	}
	if renders != 1 {
		t.Fatalf("a first write must render once, rendered %d times", renders)
	}
	if _, err = writeStableBrief(dir, render); err != nil {
		t.Fatal(err)
	}
	if renders != 3 {
		t.Fatalf("a replay must render twice more (fresh, then the byte comparison), total %d", renders)
	}
	again, err := os.ReadFile(p)
	if err != nil || string(again) != string(first) {
		t.Fatalf("a replay must leave the brief byte-identical: %v %q", err, again)
	}
}

// TestVerifyBriefDoesNotWriteTheSliceDiff pins the other half of rendering
// once: the verifier's brief is rendered inside writeStableBriefAt's token
// comparison, so anything its render does is done again on every replay.
// Writing the slice diff used to be one of those things; dispatchAgent now
// writes it once before the closure is built and hands the path down, the
// way dispatchLenses always has (#51). The render is therefore free of that
// side effect — it is handed a path it never checks — and this is what says
// so: a render that recomputed the diff would leave the file behind, and
// could not even reach the git calls it takes, since this brief has neither
// a context nor a repository.
func TestVerifyBriefDoesNotWriteTheSliceDiff(t *testing.T) {
	t.Parallel()
	const tok = "UNTRUSTED-ARTIFACT-99887766554433aa"
	dir := t.TempDir()
	aw := &bundle.ActiveWave{N: 2, Slice: 1, Attempt: 3}
	r := &nextRun{
		slug: "demo", bdir: dir, ws: &workspace{},
		st: &bundle.State{Phase: bundle.PhaseExecute, ActiveWave: aw},
	}
	diff := sliceDiffPath(dir, aw.N, aw.Slice, aw.Attempt)
	ag := op.Agent{Agent: op.AgentReviewer}
	text, name, err := r.verifyBrief(&ag, tok, diff)
	if err != nil || name != "" {
		t.Fatalf("verifyBrief: %v %q", err, name)
	}
	if !strings.Contains(text, diff) {
		t.Fatalf("the brief must point at the diff it was handed:\n%s", text)
	}
	if _, serr := os.Stat(diff); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("rendering the brief must not write the slice diff: %v", serr)
	}
}

// prFixtureGoals is a goals.md the parser accepts: an anchor block and one
// goal.
const prFixtureGoals = "# Goals — demo\n\n## Anchor\n```text\nadd a greeting\n```\n\n" +
	"## Goals\n- G1 — it works · signal: test · evidence: go test\n"

// prFixtureRecord is a finish/goals.json that decodes.
const prFixtureRecord = `{"sha":"deadbeef","verdicts":` +
	`[{"id":"G1","verdict":"achieved","evidence":"the test passes","citations":[]}],"at":"2026-01-01T00:00:00Z"}`

// prFixture is a goals-on bundle preparePushPR can be run against: a spec,
// a goals.md and a decodable record, on no repository — the run's bundle is
// outside one, so the body points at the directory itself.
func prFixture(t *testing.T) (*nextRun, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "finish"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"spec.md":           "# Add a greeting\n\nThe repository greets.\n",
		"goals.md":          prFixtureGoals,
		"finish/goals.json": prFixtureRecord,
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := &nextRun{slug: "demo", bdir: dir, ws: &workspace{}, st: &bundle.State{
		Topic: "add a greeting", Phase: bundle.PhaseFinish, Config: bundle.RunConfig{Goals: true},
	}}
	return r, dir
}

// TestPreparePushPRWritesTheBodyAndNamesIt covers the op's happy path at the
// unit the end-to-end test cannot reach into: the body is written where the
// inputs point, and both inputs are filled from what the run wrote. The
// fixture's workspace is a zero *workspace* (Dir.InRepo false), so the
// commitBundle call preparePushPR now makes is the documented
// external-bundle no-op ("", false, nil) rather than a commit — asserted
// directly below, so the fixture explains why a bundle outside a repository
// is not committed.
func TestPreparePushPRWritesTheBodyAndNamesIt(t *testing.T) {
	t.Parallel()
	r, dir := prFixture(t)
	data, inputs := brief.RunData{}, map[string]any{}
	if err := r.preparePushPR(context.Background(), &data, inputs); err != nil {
		t.Fatal(err)
	}
	if data.PRTitle != "Add a greeting" || inputs["pr_title"] != "Add a greeting" {
		t.Fatalf("%q %v", data.PRTitle, inputs["pr_title"])
	}
	if data.PRBodyPath != finish.PRPath(dir) || inputs["pr_body_path"] != finish.PRPath(dir) {
		t.Fatalf("%q %v", data.PRBodyPath, inputs["pr_body_path"])
	}
	body, err := os.ReadFile(finish.PRPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "- G1 — it works — achieved") ||
		!strings.Contains(string(body), "Bundle: "+dir+"/") {
		t.Fatalf("body:\n%s", body)
	}
	_, committed, err := commitBundle(context.Background(), r.ws, r.bdir, r.slug, "pr body")
	if committed || err != nil {
		t.Fatalf("bundle outside a repository must not be committed: committed=%v err=%v", committed, err)
	}
}

// assertPreparePushPRRefuses runs the preparation on a broken bundle and
// pins that it failed leaving nothing behind: no finish/pr.md for the next
// call to hand a session, no input half-filled. It returns the error, so the
// caller can say what the message has to name.
func assertPreparePushPRRefuses(t *testing.T, r *nextRun, dir string) error {
	t.Helper()
	data, inputs := brief.RunData{}, map[string]any{}
	err := r.preparePushPR(context.Background(), &data, inputs)
	if err == nil {
		t.Fatal("a broken bundle must not produce a pull request")
	}
	if _, serr := os.Stat(finish.PRPath(dir)); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("a failed preparation must write no body: %v", serr)
	}
	if len(inputs) != 0 || data.PRTitle != "" || data.PRBodyPath != "" {
		t.Fatalf("a failed preparation must fill no input: %v %+v", inputs, data)
	}
	return err
}

// TestPreparePushPRStopsOnAnyUnreadableInput pins the op's strictness at its
// own reads. Two of the three are reached in a real `takt next` only through
// this method; the malformed record is not reachable through one at all,
// because the finish facts decode the same file earlier in the call and fail
// first. That is exactly why the branch is asserted here: the rule is that a
// pull request is never opened describing goals takt could not read, and it
// has to hold at this end too, whichever reader gets there first.
//
// The two missing files are named by the errors themselves; the undecodable
// record is not, because encoding/json names no file — in the real call
// factsHint is what supplies the path.
//
// (The test lives beside the brief tests because it needs the unexported
// method, and internal/cli's finish tests are an external package.)
func TestPreparePushPRStopsOnAnyUnreadableInput(t *testing.T) {
	t.Parallel()
	// The value says whether the file is corrupted rather than removed: a
	// removed record is not a broken bundle at all — finish.ReadGoals reports
	// it as "no record" and every goal is then rendered "not assessed".
	for name, corrupt := range map[string]bool{
		"spec.md": false, "goals.md": false, "finish/goals.json": true,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, dir := prFixture(t)
			p := filepath.Join(dir, filepath.FromSlash(name))
			breakIt := os.Remove
			if corrupt {
				breakIt = func(q string) error { return os.WriteFile(q, []byte("{"), 0o600) }
			}
			if err := breakIt(p); err != nil {
				t.Fatal(err)
			}
			err := assertPreparePushPRRefuses(t, r, dir)
			if !corrupt && !strings.Contains(err.Error(), name) {
				t.Fatalf("the error must name %s: %v", name, err)
			}
		})
	}
}
