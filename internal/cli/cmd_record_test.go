//nolint:testpackage // tests an unexported helper
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gitx"
	"github.com/monrad/takt/internal/testutil"
)

// trailerMarkers is the marker-prefix axis of the accepted-shape
// cross-product; each entry already carries the whitespace a marker must be
// followed by, since that spacing is the one thing the grammar makes
// mandatory.
var trailerMarkers = []string{"", "- ", "* ", "+ ", "> ", "# ", "1. "}

// trailerDecorations is the decoration axis. Each of the four slots — the run
// before the key, the run after its colon, the run opening the value and the
// run closing it — varies over this list independently of the other three,
// because that independence is the grammar.
var trailerDecorations = []string{"", "*", "**", "_", "__", "`"}

// trailerKeyCases pairs each key with the value it carries through the
// cross-product.
var trailerKeyCases = []struct{ key, value string }{
	{trailerStatus, "done"},
	{trailerSummary, "fixed the parser"},
	{trailerBlockers, "none"},
}

// trailerCase is one line and the value the key named by key must record.
type trailerCase struct {
	line, key, want string
}

// TestParseReportAcceptsTheDecorationCrossProduct walks the whole product of
// the accepted-shape axes — marker x run-before-key x run-after-colon x
// run-opening-value x run-closing-value x key — rather than a diagonal
// through it, because every decoration slot is independent and a mismatched
// pair is as acceptable as a matched one. The expectation is computed from the
// same opener rule the parser follows: a closing run is punctuation only on a
// line that opened something, so the one combination whose only decoration is
// the closer keeps it. The assertions are inline: tens of thousands of subtest
// frames would cost more than the strings they check.
func TestParseReportAcceptsTheDecorationCrossProduct(t *testing.T) {
	t.Parallel()
	for _, marker := range trailerMarkers {
		for _, dkOpen := range trailerDecorations {
			for _, dkClose := range trailerDecorations {
				checkTrailerValueSlots(t, marker, dkOpen, dkClose)
			}
		}
	}
}

// checkTrailerValueSlots finishes the cross-product for one marker and one
// pair of runs around the key, varying the two runs around the value and the
// key itself.
func checkTrailerValueSlots(t *testing.T, marker, dkOpen, dkClose string) {
	t.Helper()
	for _, do := range trailerDecorations {
		for _, dc := range trailerDecorations {
			for _, kc := range trailerKeyCases {
				line := marker + dkOpen + kc.key + ":" + dkClose + " " + do + kc.value + dc
				want := kc.value
				if dkOpen == "" && dkClose == "" && do == "" && dc != "" {
					want = kc.value + dc // a closer with no opener is left in place
				}
				got := parsedTrailerField(kc.key, line)
				if got != want {
					t.Errorf("parseReport(%q) %s = %q, want %q (M=%q Dk-open=%q Dk-close=%q Do=%q Dc=%q)",
						line, kc.key, got, want, marker, dkOpen, dkClose, do, dc)
				}
			}
		}
	}
}

// TestParseReportMarkerBoundaries pins the edges of the marker grammar, which
// an implementation can miss while satisfying every other row: a marker is one
// character (or one to six '#', or ASCII digits then '.' or ')') followed by
// at least one space or tab.
func TestParseReportMarkerBoundaries(t *testing.T) {
	t.Parallel()
	checkTrailerCases(t, []trailerCase{
		{"###### STATUS: done", trailerStatus, "done"},
		{"0. STATUS: done", trailerStatus, "done"},
		{"007. STATUS: done", trailerStatus, "done"},
		{"2) STATUS: done", trailerStatus, "done"},
		{"> - 1. **STATUS: done**", trailerStatus, "done"},
		{"*`STATUS: done", trailerStatus, "done"},
		{"-\tSTATUS: done", trailerStatus, "done"},
	})
	// "--" and ">>" are neither markers nor decoration, so the key no longer
	// starts the line; "**" is not a marker either, and step 2 takes the run
	// without the space after it, which leaves the key unanchored. A
	// non-ASCII digit and a signed number are not ordered markers.
	for _, line := range []string{
		"####### STATUS: done",
		"-- STATUS: done",
		">> STATUS: done",
		"** STATUS: done",
		"١. STATUS: done",
		"+1. STATUS: done",
	} {
		assertNoTrailer(t, line)
	}
}

// TestParseReportWorkedExamples walks the accepted shapes the spec spells out,
// including the malformed decoration it deliberately accepts: the parser
// exists to stop a correct result being thrown away over punctuation.
func TestParseReportWorkedExamples(t *testing.T) {
	t.Parallel()
	checkTrailerCases(t, []trailerCase{
		{"STATUS: done", trailerStatus, "done"},
		{"**STATUS:** done", trailerStatus, "done"},
		{"`STATUS:` `done`", trailerStatus, "done"},
		{"> 1. **STATUS: done**", trailerStatus, "done"},
		{"**STATUS: done", trailerStatus, "done"},  // never closed
		{"*STATUS:** done", trailerStatus, "done"}, // mismatched pair
		{"STATUS:done", trailerStatus, "done"},     // no space after the colon
		{"_SUMMARY:_ fixed the parser", trailerSummary, "fixed the parser"},
		{"+ __BLOCKERS:__ none", trailerBlockers, "none"},
	})
}

// TestParseReportOpenerRule proves both sides of the rule that decides about a
// closing run: a line that opened decoration loses it, and a line that never
// opened any keeps every byte it arrived with. The last row is the documented
// non-goal — "STATUS: done**" keeps its stars and therefore fails recordTask's
// done|failed|blocked check — asserted so a future change to it is a
// deliberate one.
func TestParseReportOpenerRule(t *testing.T) {
	t.Parallel()
	checkTrailerCases(t, []trailerCase{
		{"STATUS: **done**", trailerStatus, "done"},
		{"**STATUS: done**", trailerStatus, "done"},
		{"SUMMARY: changed wildcard *", trailerSummary, "changed wildcard *"},
		{"SUMMARY: fixed *parseReport*", trailerSummary, "fixed *parseReport*"},
		{"STATUS: done**", trailerStatus, "done**"},
	})
}

// TestParseReportWholeLineWrapIsBlunt pins the one place the rule is knowingly
// lossy: the whole-line closer and the emphasis closer are one run of three
// stars, no parser can separate them, and the value loses the inner one.
func TestParseReportWholeLineWrapIsBlunt(t *testing.T) {
	t.Parallel()
	checkTrailerCases(t, []trailerCase{
		{"**SUMMARY: fixed *parseReport***", trailerSummary, "fixed *parseReport"},
	})
}

// TestParseReportMustNotMatch has one row per line of the spec's
// must-not-match table. Rejection is structural — the key must be uppercase,
// must carry its colon, and must start the line — and that is the whole set.
func TestParseReportMustNotMatch(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"the digest is rejected when STATUS: is missing",
		"see STATUS: done in the brief",
		"status: done",
		"Status: done",
		"STATUS done",
		"-STATUS: done",
		"####### STATUS: done",
	} {
		assertNoTrailer(t, line)
	}
}

// TestParseReportUndecoratedTrailerIsUnchanged is the regression half of the
// contract: a plain trailer parses exactly as it did before the tolerance was
// added, lowercased status and all.
func TestParseReportUndecoratedTrailerIsUnchanged(t *testing.T) {
	t.Parallel()
	msg := "I did the work.\n\nSTATUS: Done\nSUMMARY: fixed the parser\nBLOCKERS: none\n"
	status, summary, blockers := parseReport(msg)
	if status != "done" || summary != "fixed the parser" || blockers != "none" {
		t.Errorf("parseReport(plain trailer) = %q, %q, %q", status, summary, blockers)
	}
}

// TestParseReportTakesTheLastOccurrence quotes the brief's template earlier in
// the message and puts the real, decorated trailer last: the template lines
// must not leak into the digest.
func TestParseReportTakesTheLastOccurrence(t *testing.T) {
	t.Parallel()
	msg := strings.Join([]string{
		"The brief asked me to end with exactly these three lines:",
		"",
		"STATUS: done | failed | blocked",
		"SUMMARY: <one or two sentences>",
		"BLOCKERS: <what stopped you, or \"none\">",
		"",
		"Here is the real report.",
		"",
		"- **STATUS:** done",
		"- **SUMMARY:** parseReport now tolerates decorated trailers",
		"- **BLOCKERS:** none",
	}, "\n")
	status, summary, blockers := parseReport(msg)
	if status != "done" {
		t.Errorf("status = %q, want %q", status, "done")
	}
	if want := "parseReport now tolerates decorated trailers"; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
	if blockers != "none" {
		t.Errorf("blockers = %q, want %q", blockers, "none")
	}
}

// checkTrailerCases asserts one table of lines against the field each names.
func checkTrailerCases(t *testing.T, cases []trailerCase) {
	t.Helper()
	for _, c := range cases {
		if got := parsedTrailerField(c.key, c.line); got != c.want {
			t.Errorf("parseReport(%q) %s = %q, want %q", c.line, c.key, got, c.want)
		}
	}
}

// assertNoTrailer asserts that line records nothing at all.
func assertNoTrailer(t *testing.T, line string) {
	t.Helper()
	status, summary, blockers := parseReport(line)
	if status != "" || summary != "" || blockers != "" {
		t.Errorf("parseReport(%q) = %q, %q, %q, want three empty fields",
			line, status, summary, blockers)
	}
}

// parsedTrailerField parses line on its own and returns the field key names.
func parsedTrailerField(key, line string) string {
	status, summary, blockers := parseReport(line)
	switch key {
	case trailerSummary:
		return summary
	case trailerBlockers:
		return blockers
	default:
		return status
	}
}

// The `record` verbs in this file are called directly rather than through
// Main: this file compiles into package cli (it tests unexported helpers),
// so the external test package's command driver is out of reach. Calling the
// verb is enough for what #16 settled — the loss is folded into the document
// the verb itself prints.

// streakGoals is the smallest goals.md `record --agent goal-assessor`
// accepts: one goal, so one verdict is a whole assessment.
const streakGoals = "# Goals — demo\n\n## Anchor\n```text\nadd a greeting\n```\n\n## Goals\n" +
	"- G1 — it works · signal: test · evidence: go test\n"

// streakVerdicts is an assessment takt can use. It judges G1 missed rather
// than achieved on purpose: an all-achieved assessment goes on through
// markGoalsChecked, whose own goal_check append is part of the substantive
// write and correctly fails the command — a different contract from the
// bookkeeping reset these tests are about.
const streakVerdicts = "```json\n" +
	`[{"id":"G1","verdict":"missed","evidence":"nothing greets yet","citations":["README.md:1"]}]` +
	"\n```\n"

// streakClauses is an alignment reply takt can use.
const streakClauses = "```json\n" +
	`{"clauses":[{"id":"C1","text":"greet the user","span":"add a greeting"}]}` +
	"\n```\n"

// streakTarget builds the smallest run a record verb operates on: a
// repository to take HEAD from, a bundle directory in the finish phase, the
// goals.md an assessment is judged against, and one rejection of type
// invalid already on the event log — so the record that follows has a streak
// to end and endAttemptStreak actually reaches its append.
func streakTarget(t *testing.T, invalid string) *runTarget {
	t.Helper()
	root := testutil.NewRepo(t)
	repo, err := gitx.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	bdir := filepath.Join(root, "docs", "takt", "demo")
	if err = os.MkdirAll(bdir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bdir, "goals.md"), []byte(streakGoals), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = bundle.AppendEvent(bdir, invalid, map[string]any{keyProblems: []string{"unusable reply"}}); err != nil {
		t.Fatal(err)
	}
	return &runTarget{
		ws: &workspace{Repo: repo}, slug: "demo", bdir: bdir,
		st: &bundle.State{Slug: "demo", Topic: "add a greeting", Phase: bundle.PhaseFinish},
	}
}

// writeStreakMsg writes an agent's final message to a scratch file and
// returns the path `--from` would name.
func writeStreakMsg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "msg.txt")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// callRecord runs one record verb against a captured Env and decodes the
// JSON document it printed.
func callRecord(t *testing.T, fn func(Env) int) (int, map[string]any, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := fn(Env{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }})
	var got map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON: %q", out.String())
		}
	}
	return code, got, errb.String()
}

// readOnlyEventLog makes the run's event log read-only: ReadEvents still
// succeeds, so the streak is judged and it is only the append of the reset
// that is refused. This is the lost-append half of #16.
func readOnlyEventLog(t *testing.T, bdir string) {
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

// directoryEventLog replaces the run's event log with a directory: opening
// it succeeds and the first read fails, so ReadEvents fails and AppendEvent
// is never reached. This is the lost-read half of #16, and it takes the
// seeded rejection with it — which is exactly why a read that fails is a
// loss worth naming rather than swallowing.
func directoryEventLog(t *testing.T, bdir string) {
	t.Helper()
	p := bundle.EventsPath(bdir)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o750); err != nil {
		t.Fatal(err)
	}
}

// assertStreakLoss insists on the wire shape #16's ruling settled: exit 0,
// and a warnings array holding exactly one sentence naming the loss.
func assertStreakLoss(t *testing.T, code int, out map[string]any, errb string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("a lost streak reset must not change the exit code: %d %s", code, errb)
	}
	raw, ok := out[keyWarnings]
	if !ok {
		t.Fatalf("the loss must be reported under %q: %v", keyWarnings, out)
	}
	list, ok := raw.([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("warnings must be a non-empty array of strings: %#v", raw)
	}
	s, ok := list[0].(string)
	if !ok || !strings.Contains(s, "attempt-streak reset not recorded") {
		t.Fatalf("the warning must name the loss in one sentence: %#v", list[0])
	}
}

// assertGoalsRecordIntact checks the keys `record --agent goal-assessor` has
// always printed, so the warning is provably additive.
func assertGoalsRecordIntact(t *testing.T, out map[string]any) {
	t.Helper()
	if sha, ok := out[keySHA].(string); !ok || sha == "" {
		t.Fatalf("the assessed sha must survive the loss: %v", out)
	}
	if out["all_achieved"] != false {
		t.Fatalf("all_achieved must survive the loss: %v", out)
	}
	unmet, ok := out["unmet"].([]any)
	if !ok || len(unmet) != 1 {
		t.Fatalf("the unmet list must survive the loss: %v", out)
	}
}

// TestRecordGoalsReportsALostStreakResetAppend is the goals record's half of
// #16's ruling for the append loss: goals.record.json is already written
// when endAttemptStreak runs, so a refused append reports itself as a
// warning and changes neither the exit code nor a single existing key.
func TestRecordGoalsReportsALostStreakResetAppend(t *testing.T) {
	t.Parallel()
	tgt := streakTarget(t, evGoalsInvalid)
	from := writeStreakMsg(t, streakVerdicts)
	readOnlyEventLog(t, tgt.bdir)
	code, out, errb := callRecord(t, func(env Env) int {
		return recordGoals(t.Context(), env, tgt, from)
	})
	assertStreakLoss(t, code, out, errb)
	assertGoalsRecordIntact(t, out)
	if rec, err := finish.ReadGoals(tgt.bdir); err != nil || rec == nil {
		t.Fatalf("the substantive write landed before the loss: %v %v", rec, err)
	}
}

// TestRecordGoalsReportsAnUnreadableEventLog is the same ruling for the
// other loss: the read fails first, so there is nothing to judge and no
// append to attempt, and the streak is left counting.
func TestRecordGoalsReportsAnUnreadableEventLog(t *testing.T) {
	t.Parallel()
	tgt := streakTarget(t, evGoalsInvalid)
	from := writeStreakMsg(t, streakVerdicts)
	directoryEventLog(t, tgt.bdir)
	code, out, errb := callRecord(t, func(env Env) int {
		return recordGoals(t.Context(), env, tgt, from)
	})
	assertStreakLoss(t, code, out, errb)
	assertGoalsRecordIntact(t, out)
}

// TestRecordAlignmentReportsALostStreakResetAppend is the alignment
// record's half of the append loss: alignment.json is on disk before
// endAttemptStreak runs.
func TestRecordAlignmentReportsALostStreakResetAppend(t *testing.T) {
	t.Parallel()
	tgt := streakTarget(t, evAlignmentInvalid)
	from := writeStreakMsg(t, streakClauses)
	readOnlyEventLog(t, tgt.bdir)
	code, out, errb := callRecord(t, func(env Env) int {
		return recordAlignment(env, tgt.bdir, tgt.st, alignmentModeClauses, from)
	})
	assertStreakLoss(t, code, out, errb)
	if out[keyMode] != alignmentModeClauses || out["ok"] != true {
		t.Fatalf("the record's own keys must be untouched: %v", out)
	}
	a, err := readAlignment(tgt.bdir)
	if err != nil || a == nil || len(a.Clauses) != 1 {
		t.Fatalf("the substantive write landed before the loss: %+v %v", a, err)
	}
}

// TestRecordAlignmentReportsAnUnreadableEventLog is the alignment record's
// half of the read loss.
func TestRecordAlignmentReportsAnUnreadableEventLog(t *testing.T) {
	t.Parallel()
	tgt := streakTarget(t, evAlignmentInvalid)
	from := writeStreakMsg(t, streakClauses)
	directoryEventLog(t, tgt.bdir)
	code, out, errb := callRecord(t, func(env Env) int {
		return recordAlignment(env, tgt.bdir, tgt.st, alignmentModeClauses, from)
	})
	assertStreakLoss(t, code, out, errb)
	if out[keyMode] != alignmentModeClauses || out["ok"] != true {
		t.Fatalf("the record's own keys must be untouched: %v", out)
	}
}

// TestRecordPrintsNoWarningsKeyOnACleanRecord is the other half of the
// contract: absent when nothing was lost, so a healthy goals record and a
// healthy alignment record print exactly what they always have.
func TestRecordPrintsNoWarningsKeyOnACleanRecord(t *testing.T) {
	t.Parallel()
	goalsTgt := streakTarget(t, evGoalsInvalid)
	code, out, errb := callRecord(t, func(env Env) int {
		return recordGoals(t.Context(), env, goalsTgt, writeStreakMsg(t, streakVerdicts))
	})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, ok := out[keyWarnings]; ok {
		t.Fatalf("a clean goals record must print no warnings key: %v", out)
	}
	if ev := lastStreakEvent(t, goalsTgt.bdir, evGoalsReset); ev.Data[keyReason] != reasonRecorded {
		t.Fatalf("the reset must still be appended when nothing is broken: %+v", ev.Data)
	}
	alignTgt := streakTarget(t, evAlignmentInvalid)
	code, out, errb = callRecord(t, func(env Env) int {
		return recordAlignment(env, alignTgt.bdir, alignTgt.st, alignmentModeClauses, writeStreakMsg(t, streakClauses))
	})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, ok := out[keyWarnings]; ok {
		t.Fatalf("a clean alignment record must print no warnings key: %v", out)
	}
	if ev := lastStreakEvent(t, alignTgt.bdir, evAlignmentReset); ev.Data[keyReason] != reasonRecorded {
		t.Fatalf("the reset must still be appended when nothing is broken: %+v", ev.Data)
	}
}

// lastStreakEvent returns the newest event of typ, failing the test if the
// log holds none.
func lastStreakEvent(t *testing.T, bdir, typ string) bundle.Event {
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
	t.Fatalf("no %s event among %d events", typ, len(events))
	return bundle.Event{}
}
