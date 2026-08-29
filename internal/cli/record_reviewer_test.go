package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/testutil"
	"github.com/monrad/takt/internal/wave"
)

// reviewerRun builds a bundle in phase execute with an already-dispatched
// wave 0 slice 1 attempt 1, one task (id 3, files a.go and b.go), the
// two-lens frozen set the design freezes at init, and a recorded done digest
// for task 3 at attempt 1 — the dispatch every `record --agent reviewer`
// test in this file records against (task-7 brief Step 1).
func reviewerRun(t *testing.T) (string, string) {
	t.Helper()
	root, bdir := setupRunWith(t)
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	idx := `{"schema":1,"spec_hash":"x","tasks":[
 {"id":3,"title":"c","description":"create c.go","files":["a.go","b.go"],"verify":["true"],"depends_on":[],"goals":[],"class":"docs"}]}`
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", idx)
	testutil.WriteFile(t, root, "docs/takt/demo/plan.md", "# plan\n")
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.Phase = bundle.PhaseExecute
	st.Config.Review.Lenses = []string{"correctness", "intent"}
	st.Tasks = []bundle.Task{
		{ID: 3, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go", "b.go"}, Class: "docs"},
	}
	st.ActiveWave = &bundle.ActiveWave{N: 0, Slice: 1, Attempt: 1, Tasks: []int{3}, StartedAt: time.Now().UTC()}
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, root, "reviewer fixture")
	testutil.WriteFile(t, root, "a.go", "package a\n")
	testutil.WriteFile(t, root, "b.go", "package b\n")
	record(t, root, 3, 1, "done", "wrote a.go and b.go")
	return root, bdir
}

// writeMsg writes body to a scratch file and returns its path — the shape
// `record --from` reads an agent's final message from.
func writeMsg(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "msg.txt")
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// writeLensRecord seeds one lens's record for wave 0 slice 1 attempt 1.
func writeLensRecord(t *testing.T, bdir, lens string, findings []wave.LensFinding) {
	t.Helper()
	rec := wave.LensRecord{
		Lens: lens, Wave: 0, Slice: 1, Attempt: 1, Model: "sonnet",
		RecordedAt: time.Now().UTC(), Findings: findings,
	}
	if err := wave.WriteLensRecord(bdir, rec); err != nil {
		t.Fatal(err)
	}
}

// lastEventOfType returns the newest event of typ, failing the test if none
// was appended.
func lastEventOfType(t *testing.T, bdir, typ string) bundle.Event {
	t.Helper()
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range slices.Backward(events) {
		if e.Type == typ {
			return e
		}
	}
	t.Fatalf("no %s event found among %d events", typ, len(events))
	return bundle.Event{}
}

// hasEventOfType reports whether any event of typ was appended.
func hasEventOfType(t *testing.T, bdir, typ string) bool {
	t.Helper()
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// countEventsOfType counts how many events of typ were appended.
func countEventsOfType(t *testing.T, bdir, typ string) int {
	t.Helper()
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

const correctnessLensMsg = "Here is my review.\n\n```json\n" +
	`{"lens":"correctness","findings":[` +
	`{"severity":"major","file":"a.go","line":4,"title":"t1","detail":"d1"},` +
	`{"severity":"minor","file":"other.go","line":1,"title":"t2","detail":"d2"},` +
	`{"severity":"nit","title":"no file"}]}` +
	"\n```\n"

func TestRecordLensWritesTheRecordAndAttributesTasks(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	msg := writeMsg(t, correctnessLensMsg)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	rec, err := wave.ReadLensRecord(bdir, 0, 1, 1, "correctness")
	if err != nil || rec == nil {
		t.Fatalf("lens record: %v %v", rec, err)
	}
	if _, err = os.Stat(wave.LensRecordPath(bdir, 0, 1, 1, "correctness")); err != nil {
		t.Fatalf("waves/0/lens-correctness.s1.a1.json must exist: %v", err)
	}
	if len(rec.Findings) != 2 {
		t.Fatalf("findings = %+v", rec.Findings)
	}
	if rec.Findings[0].File != "a.go" || rec.Findings[0].Task != 3 {
		t.Fatalf("a.go must attribute to task 3: %+v", rec.Findings[0])
	}
	if rec.Findings[1].File != "other.go" || rec.Findings[1].Task != 0 {
		t.Fatalf("other.go declares no task: %+v", rec.Findings[1])
	}
	if len(rec.Dropped) != 1 || rec.Dropped[0] != (wave.DroppedFinding{Title: "no file", Reason: "no file cited"}) {
		t.Fatalf("dropped = %+v", rec.Dropped)
	}
	ev := lastEventOfType(t, bdir, "lens_recorded")
	if ev.Data["mode"] != "correctness" {
		t.Fatalf("lens_recorded event = %+v", ev.Data)
	}
}

// TestRecordLensValidReplyEndsTheRejectionStreak covers review finding 2: no
// test previously seeded a reviewer_invalid streak and asserted the
// following valid reply appends reviewer_attempts_reset with reason
// "recorded" — the pairing endAttemptStreak relies on (spec §5.3 rows 10,
// 11, 21, mirrored for the reviewer agent).
func TestRecordLensValidReplyEndsTheRejectionStreak(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	badMsg := writeMsg(t, "no JSON block here at all, just prose.")
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", badMsg, "--slug", "demo")
	if code != 0 || out["valid"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if !hasEventOfType(t, bdir, "reviewer_invalid") {
		t.Fatal("reviewer_invalid event must be appended by the unusable reply")
	}
	goodMsg := writeMsg(t, correctnessLensMsg)
	code, out, errb = runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", goodMsg, "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	ev := lastEventOfType(t, bdir, "reviewer_attempts_reset")
	if ev.Data["reason"] != "recorded" {
		t.Fatalf("reviewer_attempts_reset event = %+v", ev.Data)
	}
}

func TestRecordLensUnusableReplyIsProblemsNotFailure(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)

	// No JSON block at all.
	msg := writeMsg(t, "I looked at the diff and it seems fine, no notes.")
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 0 || out["valid"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if !hasEventOfType(t, bdir, "reviewer_invalid") {
		t.Fatal("a reviewer_invalid event must be appended")
	}
	if _, err := os.Stat(wave.LensRecordPath(bdir, 0, 1, 1, "correctness")); !os.IsNotExist(err) {
		t.Fatalf("no record must be written for an unusable reply: %v", err)
	}

	// A finding with an unknown severity.
	badSeverity := "```json\n" +
		`{"lens":"correctness","findings":[{"severity":"huge","file":"a.go","line":1,"title":"t"}]}` +
		"\n```\n"
	msg2 := writeMsg(t, badSeverity)
	code, out, errb = runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", msg2, "--slug", "demo")
	if code != 0 || out["valid"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	problems, ok := out["problems"].([]any)
	if !ok || len(problems) == 0 {
		t.Fatalf("problems = %v", out["problems"])
	}
	found := false
	for _, p := range problems {
		if s, isStr := p.(string); isStr && strings.Contains(s, `"huge"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems must name the bad severity: %v", problems)
	}
	if _, err := os.Stat(wave.LensRecordPath(bdir, 0, 1, 1, "correctness")); !os.IsNotExist(err) {
		t.Fatalf("still no record on disk: %v", err)
	}
}

func TestRecordLensStaleAttemptIgnored(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	msg := writeMsg(t, correctnessLensMsg)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "2", "--from", msg, "--slug", "demo")
	if code != 0 || out["ignored"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if !hasEventOfType(t, bdir, "lens_ignored") {
		t.Fatal("a lens_ignored event must be appended")
	}
	if _, err := os.Stat(wave.LensRecordPath(bdir, 0, 1, 1, "correctness")); !os.IsNotExist(err) {
		t.Fatalf("nothing must be written for a stale attempt: %v", err)
	}
}

func TestRecordLensUnknownModeFails(t *testing.T) {
	t.Parallel()
	root, _ := reviewerRun(t)
	msg := writeMsg(t, correctnessLensMsg)
	code, _, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "simplicity", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 1 {
		t.Fatalf("a mode outside the frozen lens set (and not verify) must exit 1, got %d: %s", code, errb)
	}
}

// TestRecordReviewerNoActiveWaveFails and its two neighbours cover the rest
// of the contract's first row (design §5.1, §5.3): takt's own invariants —
// no active wave, an unreadable --from, and (already covered above by
// TestRecordLensUnknownModeFails) a --mode outside the frozen set — are a
// mis-wired session and exit 1, not a problem list at exit 0.
func TestRecordReviewerNoActiveWaveFails(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	st.ActiveWave = nil
	if err = bundle.SaveState(bdir, st); err != nil {
		t.Fatal(err)
	}
	msg := writeMsg(t, correctnessLensMsg)
	code, _, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 1 {
		t.Fatalf("no active wave must exit 1, got %d: %s", code, errb)
	}
}

func TestRecordReviewerUnreadableFromFails(t *testing.T) {
	t.Parallel()
	root, _ := reviewerRun(t)
	missing := filepath.Join(t.TempDir(), "missing.txt")
	code, _, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", missing, "--slug", "demo")
	if code != 1 {
		t.Fatalf("an unreadable --from must exit 1, got %d: %s", code, errb)
	}
}

// TestRecordVerifyMissingLensRecordFails covers the contract's last row: a
// verify dispatch that arrives before every lens has recorded is a mis-wired
// session (decide never dispatches verify otherwise), and exits 1 rather than
// being reported as a problem list.
func TestRecordVerifyMissingLensRecordFails(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	// "intent" never records.
	msg := writeMsg(t, `{"mode":"verify","verdicts":[]}`)
	code, _, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 1 {
		t.Fatalf("a verify dispatched before every lens recorded must exit 1, got %d: %s", code, errb)
	}
}

// TestRecordVerifyZeroCandidatesFails is the other half: every lens recorded
// but none of them found anything, so there is nothing for a verifier to
// judge — the same mis-wired-session exit as a missing lens record.
func TestRecordVerifyZeroCandidatesFails(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", nil)
	writeLensRecord(t, bdir, "intent", nil)
	msg := writeMsg(t, `{"mode":"verify","verdicts":[]}`)
	code, _, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 1 {
		t.Fatalf("zero candidates must exit 1, got %d: %s", code, errb)
	}
}

// assertVerifiedRecord checks the two candidates and two verdicts
// TestRecordVerifyWritesInternalRecordAndCarriesUnattributed's fixture
// produces: c1 attributed to task 3 in a.go, c2 unattributed (task 0) in
// other.go, both confirmed with the evidence and citation the verify
// message gave.
func assertVerifiedRecord(t *testing.T, rec *wave.InternalRecord) {
	t.Helper()
	if len(rec.Candidates) != 2 {
		t.Fatalf("candidates = %+v", rec.Candidates)
	}
	byID := map[string]wave.Candidate{}
	for _, c := range rec.Candidates {
		byID[c.ID] = c
	}
	if c1 := byID["c1"]; c1.Task != 3 || c1.File != "a.go" {
		t.Fatalf("c1 = %+v", c1)
	}
	if c2 := byID["c2"]; c2.Task != 0 || c2.File != "other.go" {
		t.Fatalf("c2 = %+v", c2)
	}
	if len(rec.Verdicts) != 2 {
		t.Fatalf("verdicts = %+v", rec.Verdicts)
	}
	vByID := map[string]wave.CandidateVerdict{}
	for _, v := range rec.Verdicts {
		vByID[v.ID] = v
	}
	v1, v2 := vByID["c1"], vByID["c2"]
	if v1.Verdict != wave.VerdictConfirmed || v1.Evidence != "read a.go:2-8; span shows it" ||
		len(v1.Citations) != 1 || v1.Citations[0] != "a.go:4" {
		t.Fatalf("c1 verdict = %+v", v1)
	}
	if v2.Verdict != wave.VerdictConfirmed || v2.Evidence != "read other.go; stale doc" ||
		len(v2.Citations) != 1 || v2.Citations[0] != "other.go:1" {
		t.Fatalf("c2 verdict = %+v", v2)
	}
}

func TestRecordVerifyWritesInternalRecordAndCarriesUnattributed(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	writeLensRecord(t, bdir, "intent", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "minor", File: "other.go", Line: 1, Title: "t2"}, Task: 0},
	})
	verifyMsg := "```json\n" +
		`{"mode":"verify","verdicts":[` +
		`{"id":"c1","verdict":"confirmed","evidence":"read a.go:2-8; span shows it","citations":["a.go:4"]},` +
		`{"id":"c2","verdict":"confirmed","evidence":"read other.go; stale doc","citations":["other.go:1"]}]}` +
		"\n```\n"
	msg := writeMsg(t, verifyMsg)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	rec, err := wave.ReadInternalRecord(bdir, 0, 1, 1)
	if err != nil || rec == nil {
		t.Fatalf("internal record: %v %v", rec, err)
	}
	if len(rec.Confirmed) != 2 || rec.Confirmed[0] != "c1" || rec.Confirmed[1] != "c2" {
		t.Fatalf("confirmed = %v", rec.Confirmed)
	}
	assertVerifiedRecord(t, rec)
	fups, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fups.Items) != 1 {
		t.Fatalf("exactly one unattributed follow-up expected, got %d: %+v", len(fups.Items), fups.Items)
	}
	item := fups.Items[0]
	if item.Source != gate.SourceInternal || item.Wave == nil || *item.Wave != 0 ||
		item.Task != 0 || item.Title != "t2" {
		t.Fatalf("follow-up = %+v", item)
	}
	if !hasEventOfType(t, bdir, "internal_review_recorded") {
		t.Fatal("an internal_review_recorded event must be appended")
	}
}

// TestRecordVerifyReplayIsIdempotent covers review finding 1: unlike
// recordLens, recordVerify had no short-circuit for a dispatch already
// verified, so a same-attempt replay re-appended internal_review_recorded
// and re-carried the unattributed confirmed findings to follow-ups.json —
// breaking "every record is safe to run twice" (design §5.4). A replay must
// report success off the existing record without appending anything twice.
func TestRecordVerifyReplayIsIdempotent(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	writeLensRecord(t, bdir, "intent", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "minor", File: "other.go", Line: 1, Title: "t2"}, Task: 0},
	})
	verifyMsg := "```json\n" +
		`{"mode":"verify","verdicts":[` +
		`{"id":"c1","verdict":"confirmed","evidence":"read a.go:2-8; span shows it","citations":["a.go:4"]},` +
		`{"id":"c2","verdict":"confirmed","evidence":"read other.go; stale doc","citations":["other.go:1"]}]}` +
		"\n```\n"
	msg := writeMsg(t, verifyMsg)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("first record: %d %v %s", code, out, errb)
	}
	// Replay: the identical message recorded a second time for the same
	// (wave, slice, attempt).
	code, out, errb = runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", msg, "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("replay: %d %v %s", code, out, errb)
	}
	fups, err := gate.ReadFollowUps(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fups.Items) != 1 {
		t.Fatalf("a replay must not duplicate follow-ups, got %d: %+v", len(fups.Items), fups.Items)
	}
	if n := countEventsOfType(t, bdir, "internal_review_recorded"); n != 1 {
		t.Fatalf("a replay must not re-append internal_review_recorded, got %d events", n)
	}
}

func TestRecordVerifyEnforcesTheEvidenceBar(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	writeLensRecord(t, bdir, "intent", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "minor", File: "other.go", Line: 1, Title: "t2"}, Task: 0},
	})

	// c1 confirmed with no evidence.
	noEvidence := "```json\n" +
		`{"mode":"verify","verdicts":[` +
		`{"id":"c1","verdict":"confirmed","evidence":"","citations":["a.go:4"]},` +
		`{"id":"c2","verdict":"false_positive","evidence":"checked, not real"}]}` +
		"\n```\n"
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", writeMsg(t, noEvidence), "--slug", "demo")
	if code != 0 || out["valid"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if !problemsMention(t, out, "evidence") {
		t.Fatalf("problems must mention the missing evidence: %v", out["problems"])
	}
	if rec, _ := wave.ReadInternalRecord(bdir, 0, 1, 1); rec != nil {
		t.Fatalf("nothing must be written on a rejected reply: %+v", rec)
	}

	// c2 has no verdict at all.
	missingC2 := "```json\n" +
		`{"mode":"verify","verdicts":[` +
		`{"id":"c1","verdict":"confirmed","evidence":"read a.go:2-8","citations":["a.go:4"]}]}` +
		"\n```\n"
	code, out, errb = runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", writeMsg(t, missingC2), "--slug", "demo")
	if code != 0 || out["valid"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if !problemsMention(t, out, "c2") {
		t.Fatalf("problems must name candidate c2: %v", out["problems"])
	}
	if rec, _ := wave.ReadInternalRecord(bdir, 0, 1, 1); rec != nil {
		t.Fatalf("nothing must be written on a rejected reply: %+v", rec)
	}

	// An unknown candidate id.
	unknownID := "```json\n" +
		`{"mode":"verify","verdicts":[` +
		`{"id":"c1","verdict":"confirmed","evidence":"read a.go:2-8","citations":["a.go:4"]},` +
		`{"id":"c2","verdict":"false_positive","evidence":"checked, not real"},` +
		`{"id":"c9","verdict":"confirmed","evidence":"x","citations":["z.go:1"]}]}` +
		"\n```\n"
	code, out, errb = runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", writeMsg(t, unknownID), "--slug", "demo")
	if code != 0 || out["valid"] != false {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if rec, _ := wave.ReadInternalRecord(bdir, 0, 1, 1); rec != nil {
		t.Fatalf("nothing must be written on a rejected reply: %+v", rec)
	}
}

// problemsMention reports whether any problem string in out["problems"]
// contains sub.
func problemsMention(t *testing.T, out map[string]any, sub string) bool {
	t.Helper()
	problems, ok := out["problems"].([]any)
	if !ok {
		return false
	}
	for _, p := range problems {
		if s, isStr := p.(string); isStr && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestRecordLensAfterVerifyIsIgnored(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	writeLensRecord(t, bdir, "intent", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "minor", File: "other.go", Line: 1, Title: "t2"}, Task: 0},
	})
	before, err := os.ReadFile(wave.LensRecordPath(bdir, 0, 1, 1, "correctness"))
	if err != nil {
		t.Fatal(err)
	}
	internal := wave.InternalRecord{
		Wave: 0, Slice: 1, Attempt: 1, Model: "sonnet", RecordedAt: time.Now().UTC(),
		Lenses: []string{"correctness", "intent"}, Candidates: []wave.Candidate{}, Verdicts: []wave.CandidateVerdict{},
		Confirmed: []string{},
	}
	if err = wave.WriteInternalRecord(bdir, internal); err != nil {
		t.Fatal(err)
	}
	newReply := "```json\n" +
		`{"lens":"correctness","findings":[{"severity":"major","file":"a.go","line":9,"title":"late","detail":"d"}]}` +
		"\n```\n"
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", writeMsg(t, newReply), "--slug", "demo")
	if code != 0 || out["ignored"] != true || out["reason"] != "internal review already verified" {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if !hasEventOfType(t, bdir, "lens_ignored") {
		t.Fatal("a lens_ignored event must be appended")
	}
	after, err := os.ReadFile(wave.LensRecordPath(bdir, 0, 1, 1, "correctness"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the seeded lens record must be left untouched:\nbefore=%s\nafter=%s", before, after)
	}
}

// The two ways the run's event log can lose an attempt-streak reset (#16).
// They are different failures at different seams and the tests below keep
// them apart: breakEventAppend leaves the log readable, so ReadEvents
// succeeds, the streak is judged and it is the AppendEvent that is refused;
// breakEventRead fails ReadEvents on the first byte, so the streak cannot be
// judged at all and AppendEvent is never reached.

// breakEventAppend makes the run's event log read-only: the streak is still
// there to be read, and only the write of the reset is lost.
func breakEventAppend(t *testing.T, bdir string) {
	t.Helper()
	p := bundle.EventsPath(bdir)
	if err := os.Chmod(p, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_ = f.Close()
		t.Skip("this user can write a mode-400 file, so the lost-append path cannot be provoked")
	}
}

// breakEventRead replaces the run's event log with a directory: opening it
// succeeds and the first read fails, which is the loss that stops takt from
// judging the streak — and takes the seeded rejection with it, which is why
// it is worth naming rather than swallowing.
func breakEventRead(t *testing.T, bdir string) {
	t.Helper()
	p := bundle.EventsPath(bdir)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o750); err != nil {
		t.Fatal(err)
	}
}

// seedReviewerRejection puts one reviewer_invalid on the log, so the record
// that follows has a streak to end and endAttemptStreak actually reaches its
// append.
func seedReviewerRejection(t *testing.T, root, bdir, mode string) {
	t.Helper()
	bad := writeMsg(t, "no JSON block here at all, just prose.")
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", mode, "--attempt", "1", "--from", bad, "--slug", "demo")
	if code != 0 || out["valid"] != false {
		t.Fatalf("seeding the streak: %d %v %s", code, out, errb)
	}
	if !hasEventOfType(t, bdir, "reviewer_invalid") {
		t.Fatal("the streak must be seeded before the log is broken")
	}
}

// assertStreakWarning insists on the wire shape #16's ruling settled: exit 0,
// and exactly one warning naming the loss.
func assertStreakWarning(t *testing.T, code int, out map[string]any, errb string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("a lost streak reset must not change the exit code: %d %s", code, errb)
	}
	w := warningsOf(t, out)
	if len(w) != 1 || !strings.Contains(w[0], "attempt-streak reset not recorded") {
		t.Fatalf("the warning must name the loss in one sentence: %v", w)
	}
}

// seedVerifyDispatch writes the two lens records the verify dispatch merges
// its candidates from, and returns the verifier reply that confirms both.
func seedVerifyDispatch(t *testing.T, bdir string) string {
	t.Helper()
	writeLensRecord(t, bdir, "correctness", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "major", File: "a.go", Line: 4, Title: "t1"}, Task: 3},
	})
	writeLensRecord(t, bdir, "intent", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "minor", File: "other.go", Line: 1, Title: "t2"}, Task: 0},
	})
	return "```json\n" +
		`{"mode":"verify","verdicts":[` +
		`{"id":"c1","verdict":"confirmed","evidence":"read a.go:2-8; span shows it","citations":["a.go:4"]},` +
		`{"id":"c2","verdict":"confirmed","evidence":"read other.go; stale doc","citations":["other.go:1"]}]}` +
		"\n```\n"
}

// TestRecordLensReportsALostStreakResetAppend is the lens record's half of
// #16's ruling, for the append loss: the lens record is already on disk when
// endAttemptStreak runs, so a refused append reports itself as a warning and
// leaves the exit code and every existing key exactly as they were.
func TestRecordLensReportsALostStreakResetAppend(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	seedReviewerRejection(t, root, bdir, "correctness")
	breakEventAppend(t, bdir)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", writeMsg(t, correctnessLensMsg), "--slug", "demo")
	assertStreakWarning(t, code, out, errb)
	if out["valid"] != true || out["mode"] != "correctness" || out["findings"] != float64(2) {
		t.Fatalf("the record's own keys must be untouched: %v", out)
	}
	rec, err := wave.ReadLensRecord(bdir, 0, 1, 1, "correctness")
	if err != nil || rec == nil {
		t.Fatalf("the substantive write landed before the loss: %v %v", rec, err)
	}
}

// TestRecordLensReportsAnUnreadableEventLog is the same ruling for the other
// loss: the read fails first, so there is no judgment to make and no append
// to attempt, and the streak is left counting.
func TestRecordLensReportsAnUnreadableEventLog(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	seedReviewerRejection(t, root, bdir, "correctness")
	breakEventRead(t, bdir)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", writeMsg(t, correctnessLensMsg), "--slug", "demo")
	assertStreakWarning(t, code, out, errb)
	if out["valid"] != true || out["mode"] != "correctness" || out["findings"] != float64(2) {
		t.Fatalf("the record's own keys must be untouched: %v", out)
	}
}

// TestRecordVerifyReportsALostStreakResetAppend is the verify record's half
// of the append loss: the internal record and the carried follow-ups are
// written before endAttemptStreak runs, so the refused append is a warning
// and nothing else.
func TestRecordVerifyReportsALostStreakResetAppend(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	verifyMsg := seedVerifyDispatch(t, bdir)
	seedReviewerRejection(t, root, bdir, "verify")
	breakEventAppend(t, bdir)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", writeMsg(t, verifyMsg), "--slug", "demo")
	assertStreakWarning(t, code, out, errb)
	if out["valid"] != true || out["candidates"] != float64(2) || out["confirmed"] != float64(2) {
		t.Fatalf("the record's own keys must be untouched: %v", out)
	}
	rec, err := wave.ReadInternalRecord(bdir, 0, 1, 1)
	if err != nil || rec == nil {
		t.Fatalf("the substantive write landed before the loss: %v %v", rec, err)
	}
}

// TestRecordVerifyReportsAnUnreadableEventLog is the verify record's half of
// the read loss.
func TestRecordVerifyReportsAnUnreadableEventLog(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	verifyMsg := seedVerifyDispatch(t, bdir)
	seedReviewerRejection(t, root, bdir, "verify")
	breakEventRead(t, bdir)
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", writeMsg(t, verifyMsg), "--slug", "demo")
	assertStreakWarning(t, code, out, errb)
	if out["valid"] != true || out["candidates"] != float64(2) || out["confirmed"] != float64(2) {
		t.Fatalf("the record's own keys must be untouched: %v", out)
	}
}

// TestRecordReviewerPrintsNoWarningsKeyOnACleanRecord is the other half of
// the contract: absent when nothing was lost, so a healthy lens record and a
// healthy verify record print exactly what they always have.
func TestRecordReviewerPrintsNoWarningsKeyOnACleanRecord(t *testing.T) {
	t.Parallel()
	root, bdir := reviewerRun(t)
	seedReviewerRejection(t, root, bdir, "correctness")
	code, out, errb := runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "correctness", "--attempt", "1", "--from", writeMsg(t, correctnessLensMsg), "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, ok := out[keyWarningsJSON]; ok {
		t.Fatalf("a clean lens record must print no warnings key: %v", out)
	}
	writeLensRecord(t, bdir, "intent", []wave.LensFinding{
		{Finding: backend.Finding{Severity: "minor", File: "other.go", Line: 1, Title: "t2"}, Task: 0},
	})
	verifyMsg := "```json\n" +
		`{"mode":"verify","verdicts":[` +
		`{"id":"c1","verdict":"confirmed","evidence":"read a.go:2-8; span shows it","citations":["a.go:4"]},` +
		`{"id":"c2","verdict":"confirmed","evidence":"read other.go; stale doc","citations":["other.go:1"]}]}` +
		"\n```\n"
	code, out, errb = runIn(t, root, nil, "record", "--agent", "reviewer",
		"--mode", "verify", "--attempt", "1", "--from", writeMsg(t, verifyMsg), "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("%d %v %s", code, out, errb)
	}
	if _, ok := out[keyWarningsJSON]; ok {
		t.Fatalf("a clean verify record must print no warnings key: %v", out)
	}
}
