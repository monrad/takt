package spec_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/spec"
)

// wellFormedHeading is the heading as this run's own spec.md writes it: a
// numbered `## ` section, which is how every spec in this repository numbers
// its sections.
const wellFormedHeading = "## 11. Assumptions & Open Decisions"

// wellFormed is shaped like this run's own spec.md §11: prose sections, then
// the assumptions table with the four mandated columns.
const wellFormed = `# Retro as the project's record, not takt's telemetry

## 10. Out of scope

Rewriting the three existing retros. They are historical records.

## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| How do the sections reach retro.md? | takt renders a skeleton | Markdown needs no escaping | user-confirmed |
| Does the rewrite take the run lock? | Yes, as next does | It replaces two files in sequence | assumed |
| Where does the skeleton live? | Beside retro-inputs.json | finish/ holds the derived artifacts | assumed |
`

// wantWellFormed is the literal expectation for wellFormed's rows. Every test
// of another spelling of the same table asserts against these values rather
// than against a second call to the parser, so each test stands on its own: a
// parser that always returned nothing fails every one of them.
func wantWellFormed() []spec.Assumption {
	return []spec.Assumption{
		{
			Question:  "How do the sections reach retro.md?",
			Decision:  "takt renders a skeleton",
			Rationale: "Markdown needs no escaping",
			Source:    "user-confirmed",
		},
		{
			Question:  "Does the rewrite take the run lock?",
			Decision:  "Yes, as next does",
			Rationale: "It replaces two files in sequence",
			Source:    "assumed",
		},
		{
			Question:  "Where does the skeleton live?",
			Decision:  "Beside retro-inputs.json",
			Rationale: "finish/ holds the derived artifacts",
			Source:    "assumed",
		},
	}
}

func TestParseAssumptionsWellFormed(t *testing.T) {
	t.Parallel()
	want := wantWellFormed()
	got := spec.ParseAssumptions([]byte(wellFormed))
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	if got[0].Question != want[0].Question {
		t.Errorf("first question = %q, want %q", got[0].Question, want[0].Question)
	}
	if got[0].Decision != want[0].Decision {
		t.Errorf("first decision = %q, want %q", got[0].Decision, want[0].Decision)
	}
	if got[0].Rationale != want[0].Rationale {
		t.Errorf("first rationale = %q, want %q", got[0].Rationale, want[0].Rationale)
	}
	if got[0].Source != want[0].Source {
		t.Errorf("first source = %q, want %q", got[0].Source, want[0].Source)
	}
	if !slices.Equal(got, want) {
		t.Errorf("parsed %+v, want %+v", got, want)
	}
	crlf := spec.ParseAssumptions([]byte(strings.ReplaceAll(wellFormed, "\n", "\r\n")))
	if !slices.Equal(crlf, want) {
		t.Errorf("CRLF input parsed as %+v, want %+v", crlf, want)
	}
}

func TestParseAssumptionsReorderedColumns(t *testing.T) {
	t.Parallel()
	const reordered = `## 11. Assumptions & Open Decisions

| source | rationale | question | decision |
| --- | --- | --- | --- |
| user-confirmed | Markdown needs no escaping | How do the sections reach retro.md? | takt renders a skeleton |
| assumed | It replaces two files in sequence | Does the rewrite take the run lock? | Yes, as next does |
| assumed | finish/ holds the derived artifacts | Where does the skeleton live? | Beside retro-inputs.json |
`
	want := wantWellFormed()
	got := spec.ParseAssumptions([]byte(reordered))
	if !slices.Equal(got, want) {
		t.Fatalf("reordered columns parsed as %+v, want %+v", got, want)
	}
}

func TestParseAssumptionsHeadingForms(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"as the design quotes it":     "## Assumptions & Open Decisions",
		"upper case, trailing text":   "## ASSUMPTIONS & OPEN DECISIONS (locked)",
		"numbered with a parenthesis": "## 11) Assumptions & open decisions",
	}
	want := wantWellFormed()
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := spec.ParseAssumptions([]byte(strings.Replace(wellFormed, wellFormedHeading, h, 1)))
			if !slices.Equal(got, want) {
				t.Fatalf("heading %q parsed as %+v, want %+v", h, got, want)
			}
		})
	}
}

// TestParseAssumptionsSkipsPipesInProse guards the table search: prose holding
// a stray pipe is not a markdown table, so it must be stepped over rather than
// mistaken for the header row of one.
func TestParseAssumptionsSkipsPipesInProse(t *testing.T) {
	t.Parallel()
	const prose = "\nThe choice was skeleton | JSON blob, and | -- | is not a delimiter row.\n"
	in := strings.Replace(wellFormed, wellFormedHeading+"\n", wellFormedHeading+"\n"+prose, 1)
	want := wantWellFormed()
	got := spec.ParseAssumptions([]byte(in))
	if !slices.Equal(got, want) {
		t.Fatalf("table after pipe-bearing prose parsed as %+v, want %+v", got, want)
	}
}

// TestParseAssumptionsRowsWithoutOuterPipes covers rows written without the
// optional outer pipes and starting with an issue reference such as `#63`. A
// leading hash only opens a heading when the hash run is followed by
// whitespace, so these are ordinary data rows: reading them as headings would
// end the table at the first one and silently drop it and everything after it.
func TestParseAssumptionsRowsWithoutOuterPipes(t *testing.T) {
	t.Parallel()
	const bare = `## 11. Assumptions & Open Decisions

question | decision | rationale | source
--- | --- | --- | ---
#63 | Yes | Required by the issue | user-confirmed
#64 | No | Out of scope for this run | assumed
Is the last row read? | Yes | Nothing truncated the table | assumed
`
	want := []spec.Assumption{
		{
			Question:  "#63",
			Decision:  "Yes",
			Rationale: "Required by the issue",
			Source:    "user-confirmed",
		},
		{
			Question:  "#64",
			Decision:  "No",
			Rationale: "Out of scope for this run",
			Source:    "assumed",
		},
		{
			Question:  "Is the last row read?",
			Decision:  "Yes",
			Rationale: "Nothing truncated the table",
			Source:    "assumed",
		},
	}
	got := spec.ParseAssumptions([]byte(bare))
	if !slices.Equal(got, want) {
		t.Fatalf("pipe-less rows parsed as %+v, want %+v", got, want)
	}
}

// TestParseAssumptionsHeadingEndsRows is the other half of the hash rule: a
// real heading, with no blank line before it, still ends the table.
func TestParseAssumptionsHeadingEndsRows(t *testing.T) {
	t.Parallel()
	const withSubheading = `## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| Locked? | Yes | The user said so | user-confirmed |
### A note on the above
| Read? | No | It sits below a heading | assumed |
`
	want := []spec.Assumption{{
		Question:  "Locked?",
		Decision:  "Yes",
		Rationale: "The user said so",
		Source:    "user-confirmed",
	}}
	got := spec.ParseAssumptions([]byte(withSubheading))
	if !slices.Equal(got, want) {
		t.Fatalf("rows below a subheading parsed as %+v, want %+v", got, want)
	}
}

func TestParseAssumptionsTolerant(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no section at all": `# spec

## 10. Out of scope

Nothing about assumptions here.
`,
		"heading but no table": `## 11. Assumptions & Open Decisions

The decisions of this run were all made in the issue thread.
`,
		"table belongs to the next section": `## 11. Assumptions & Open Decisions

Nothing was assumed.

## 12. Appendix

| question | decision | rationale | source |
| --- | --- | --- | --- |
| Is this table read? | No | It is under a later heading | assumed |
`,
		"missing source header": `## 11. Assumptions & Open Decisions

| question | decision | rationale | note |
| --- | --- | --- | --- |
| Is it parsed? | No | The source column is missing | assumed |
`,
		"short data row": `## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| Is it parsed? | No | A short row follows | assumed |
| Is this row short? | Yes |
`,
		"no separator row": `## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| Is it parsed? | No | The header row is followed by data | assumed |
`,
		"separator that is not markdown": `## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| ~~~ | ~~~ | ~~~ | ~~~ |
| Is it parsed? | No | That separator is not markdown | assumed |
`,
		"separator with the wrong number of columns": `## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- |
| Is it parsed? | No | The separator is one column short | assumed |
`,
		"separator cells too short to be one": `## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| - | -- | --- | --- |
| Is it parsed? | No | A delimiter cell needs three hyphens | assumed |
`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := spec.ParseAssumptions([]byte(in)); len(got) != 0 {
				t.Fatalf("got %d rows, want none: %+v", len(got), got)
			}
		})
	}
}

func TestParseAssumptionsReturnsEverySourceVerbatim(t *testing.T) {
	t.Parallel()
	const mixed = `## 11. Assumptions & Open Decisions

| question | decision | rationale | source |
| --- | --- | --- | --- |
| Locked? | Yes | The user said so | user-confirmed |
| Guessed? | Yes | Nobody was asked | assumed |
| Odd? | Yes | The parser does no filtering | Whatever The Agent Wrote |
`
	want := []string{"user-confirmed", "assumed", "Whatever The Agent Wrote"}
	got := spec.ParseAssumptions([]byte(mixed))
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Source != w {
			t.Errorf("row %d source = %q, want %q", i, got[i].Source, w)
		}
	}
}
