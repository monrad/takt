// Package spec parses the parts of a run's spec.md that takt itself reads, the
// way internal/goals parses goals.md.
//
// Today that is one table: the "Assumptions & Open Decisions" section that the
// brainstorm step mandates (internal/brief/templates/run-brainstorm.md), whose
// user-confirmed rows become the "locked decisions carried from spec" lines of
// the retro's Decisions section.
//
// The parser is tolerant by construction and returns no error, ever. A spec is
// prose written by an agent, not a machine-checked artifact: it may carry no
// assumptions section at all, a section with only prose under it, a table whose
// columns were renamed or reordered, or a row that was hand-edited into a
// shorter shape. None of that is a reason for a retro to fail, so every
// malformed shape yields an empty slice and the caller renders the sections it
// does have. A half-parsed table is worse than none, so a malformation found
// part-way through a table discards the rows read before it too.
package spec

import (
	"regexp"
	"strings"
)

// Assumption is one row of the assumptions table, with its cells as written.
// Source is the raw value ("user-confirmed", "assumed", …): the parser does no
// filtering, so the caller decides which sources it cares about.
type Assumption struct{ Question, Decision, Rationale, Source string }

// heading is the lower-cased heading text the section is found by. Trailing
// text on the line — "(locked)", a date, an editorial aside — is tolerated.
const heading = "assumptions & open decisions"

// sectionNumber matches the "11." or "11)" a spec writes in front of its
// heading text. Every spec in this repository numbers its sections, so the
// heading is matched on the text that follows the number as well as on the
// bare form the design quotes.
var sectionNumber = regexp.MustCompile(`^\d+[.):]?\s+`)

// separatorCell matches one cell of a markdown delimiter row: three or more
// hyphens with optional alignment colons. Some markdown dialects accept a
// single hyphen; requiring the three-hyphen form the brainstorm template writes
// keeps a line of prose that happens to hold pipes and a dash from being read
// as the start of a table.
var separatorCell = regexp.MustCompile(`^:?-{3,}:?$`)

// atxHeading matches a markdown ATX heading marker: a run of one to six hashes
// that closes the line or is followed by whitespace. The whitespace is what
// makes this a heading test rather than a hash test. Outer pipes are optional
// in a markdown row, so `#63 | Yes | Asked in the issue | user-confirmed` is a
// well-formed data row whose first cell happens to start with a hash; treating
// every leading hash as a heading would silently truncate the table there.
var atxHeading = regexp.MustCompile(`^#{1,6}(\s|$)`)

// ParseAssumptions reads the assumptions table out of a spec's bytes. It
// returns the rows of the first markdown table under the first
// "Assumptions & Open Decisions" heading, matching columns by header name
// rather than by position, and an empty slice for anything it cannot read.
func ParseAssumptions(b []byte) []Assumption {
	body := section(strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"))
	for i, ln := range body {
		// A table is a header row and the delimiter row that follows it.
		// Prose holding a stray pipe is not one, so the scan steps over it
		// instead of giving up: the contract is to read the first table
		// under the heading, wherever in the section it starts.
		if !isRow(ln) {
			continue
		}
		header := cells(ln)
		if i+1 >= len(body) || !isSeparator(body[i+1], len(header)) {
			continue
		}
		cols, ok := match(header)
		if !ok {
			break
		}
		return rows(body[i+2:], cols)
	}
	return []Assumption{}
}

// section returns the lines under the assumptions heading, up to the next `## `
// heading or the end of the file, and nil when there is no such heading.
func section(lines []string) []string {
	for i, ln := range lines {
		if !strings.HasPrefix(headingText(ln), heading) {
			continue
		}
		body := lines[i+1:]
		for j, bl := range body {
			if isSection(bl) {
				return body[:j]
			}
		}
		return body
	}
	return nil
}

// rows reads data rows until a blank line, a heading or the end of the section.
// A row too short for the matched columns discards the whole table.
func rows(body []string, c columns) []Assumption {
	out := make([]Assumption, 0, len(body))
	for _, ln := range body {
		if strings.TrimSpace(ln) == "" || isHeading(ln) {
			break
		}
		cs := cells(ln)
		if len(cs) < c.width {
			return []Assumption{}
		}
		out = append(out, Assumption{
			Question:  cs[c.question],
			Decision:  cs[c.decision],
			Rationale: cs[c.rationale],
			Source:    cs[c.source],
		})
	}
	return out
}

// columns is where each of the four columns sits in the header row, and how
// many cells a data row must therefore have.
type columns struct{ question, decision, rationale, source, width int }

// match locates the four columns by lower-cased header name. It reports false
// when any of them is missing, which is what makes a reordered table parse and
// a renamed one yield nothing.
func match(header []string) (columns, bool) {
	at := make(map[string]int, len(header))
	for i, h := range header {
		name := strings.ToLower(h)
		if _, dup := at[name]; !dup {
			at[name] = i // A repeated header name keeps its first column.
		}
	}
	q, okQuestion := at["question"]
	d, okDecision := at["decision"]
	r, okRationale := at["rationale"]
	s, okSource := at["source"]
	if !okQuestion || !okDecision || !okRationale || !okSource {
		return columns{}, false
	}
	return columns{question: q, decision: d, rationale: r, source: s, width: max(q, d, r, s) + 1}, true
}

// isHeading reports whether the line is a markdown ATX heading of any level,
// which is where a run of data rows ends.
func isHeading(ln string) bool {
	return atxHeading.MatchString(strings.TrimSpace(ln))
}

// isSection reports whether the line opens a new `## ` section, which is where
// the assumptions section itself ends.
func isSection(ln string) bool {
	return strings.HasPrefix(strings.TrimSpace(ln), "## ")
}

// headingText returns the lower-cased text of a `## ` heading with its marker
// and any leading section number removed, or "" when the line is not one:
// `## 11. Assumptions & Open Decisions` yields `assumptions & open decisions`.
func headingText(ln string) string {
	t := strings.TrimSpace(ln)
	if !isSection(t) {
		return ""
	}
	t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "##")))
	return sectionNumber.ReplaceAllString(t, "")
}

// isRow reports whether a line could be a table row.
func isRow(ln string) bool {
	return strings.Contains(ln, "|")
}

// isSeparator reports whether a line is a markdown delimiter row of exactly n
// cells. A header row not followed by one is not a table.
func isSeparator(ln string, n int) bool {
	cs := cells(ln)
	if len(cs) != n {
		return false
	}
	for _, c := range cs {
		if !separatorCell.MatchString(c) {
			return false
		}
	}
	return true
}

// cells splits a table row on "|", dropping the optional outer pipes and
// trimming each cell.
func cells(ln string) []string {
	s := strings.TrimSpace(ln)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "|"), "|")
	cs := strings.Split(s, "|")
	for i, c := range cs {
		cs[i] = strings.TrimSpace(c)
	}
	return cs
}
