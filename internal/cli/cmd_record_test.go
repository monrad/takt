//nolint:testpackage // tests an unexported helper
package cli

import (
	"strings"
	"testing"
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
