// Package brief renders the prompts takt hands to subagents and reviewers
// (spec §7.3, §7.4, §8.4, §10). Templates are embedded; user-authored
// artifacts are always quoted between per-dispatch delimiter tokens and
// declared to be data, never instructions.
package brief

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template"
)

//go:embed templates/*.md
var files embed.FS

//go:embed lenses/*.md
var lensFiles embed.FS

// Lenses lists the shipped lens names, sorted — the registry config
// validation and the dispatch fan-out both read (design §7.2, §10). The
// embedded directory is the single source: adding a lens is dropping a file
// here and naming it in config.
func Lenses() []string {
	entries, err := lensFiles.ReadDir("lenses")
	if err != nil {
		return nil // embed cannot fail at runtime; nil keeps the contract total
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	slices.Sort(names)
	return names
}

// LensRubric returns the rubric body for one lens.
func LensRubric(name string) (string, error) {
	b, err := lensFiles.ReadFile("lenses/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("brief: unknown lens %q", name)
	}
	return string(b), nil
}

//nolint:gosec // G101 false positive: not a credential, a public delimiter-token prefix embedded in every rendered brief
const tokenPrefix = "UNTRUSTED-ARTIFACT-"

// Token returns a fresh delimiter token.
func Token() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return tokenPrefix + hex.EncodeToString(b[:]), nil
}

// tokenPattern matches a token as Token mints it: the prefix and sixteen
// hex digits, on a word boundary so a prefix embedded in prose is not one.
var tokenPattern = regexp.MustCompile(`\b` + regexp.QuoteMeta(tokenPrefix) + `[0-9a-f]{16}\b`)

// TokenOf returns the delimiter token a rendered brief was quoted with —
// the first token in the text — so a replay can re-render with the same
// token and compare bytes instead of minting a fresh one (spec §5.4).
func TokenOf(text string) (string, bool) {
	tok := tokenPattern.FindString(text)
	return tok, tok != ""
}

// Quote wraps content between BEGIN/END marker lines. The caller must
// regenerate the token when content already contains it.
func Quote(token, label, content string) (string, error) {
	if strings.Contains(content, token) {
		return "", errors.New("brief: delimiter token collides with the content; regenerate the token")
	}
	return "BEGIN " + token + " " + label + "\n" + content + "\nEND " + token + "\n", nil
}

// GoalLine is a goal reference inside a brief.
type GoalLine struct{ ID, Text string }

// Clause is one decomposed clause of the anchor.
type Clause struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Span string `json:"span"`
}

// ImplementerData fills templates/implementer.md.
type ImplementerData struct {
	Slug            string
	Task, Total     int
	Title           string
	Description     string
	Files           []string
	Verify          []string
	Goals           []GoalLine
	SpecPath        string // absolute path of the run's spec.md; the agent reads it as data
	Attempt         int
	PreviousModel   string
	PreviousFailure string
	Findings        []string
	Token           string
	BundleDirRel    string
}

// PlannerData fills templates/planner.md.
type PlannerData struct {
	Slug, Topic, SpecText, GoalsText, Schema, RepoRoot, Token string
	MaxFiles                                                  int
	Problems                                                  []string
	Attempt                                                   int
}

// AlignmentData fills the two alignment templates. Problems is what takt
// could not use about the auditor's previous reply, quoted back to it on
// the retry (spec §5.3 rows 10, 11).
type AlignmentData struct {
	Mode, Anchor, Token           string
	Clauses                       []Clause
	SpecText, PlanText, IndexText string
	Problems                      []string
}

// ClauseLines renders the confirmed clauses as one block, so the verdicts
// template can quote them in a single delimiter pair. The text is the
// auditor's own earlier output and travels as data like every other
// agent-authored artifact (spec §10).
func (d AlignmentData) ClauseLines() string {
	var b strings.Builder
	for _, c := range d.Clauses {
		fmt.Fprintf(&b, "%s — %s\n", c.ID, c.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// GoalAssessorData feeds goal-assessor.md (spec §7.5 step 2, §10).
// Problems is what takt could not use about the assessor's previous reply,
// quoted back to it on the retry (spec §5.3 row 21).
type GoalAssessorData struct {
	Slug          string
	Token         string
	GoalsText     string
	DiffStat      string
	VerifySummary string
	Goals         []GoalLine
	Problems      []string
}

// LensTask is one task in a wave reviewed through a lens.
type LensTask struct {
	ID          int
	Title       string
	Description string
	Files       []string
}

// LensData feeds review-lens.md (spec §7.4 row 8, §10).
type LensData struct {
	Slug                 string
	Wave, Slice, Attempt int
	Lens                 string
	Rubric               string
	DiffPath             string // absolute path of the slice diff file
	Tasks                []LensTask
	Token                string
}

// TaskBlock renders one task for quoting: title, description and declared
// files in a single delimiter pair, so the template stays one quote call
// per task.
func (LensData) TaskBlock(t LensTask) string {
	return t.Title + "\n" + t.Description + "\nfiles: " + strings.Join(t.Files, ", ")
}

// VerifyCandidate is one candidate finding a lens review proposed, being
// verified in a pass over the candidates.
type VerifyCandidate struct {
	ID, Severity, File, Title, Detail string
	Line                              int
}

// VerifyData feeds review-verify.md (spec §7.4 row 9, §10).
type VerifyData struct {
	Slug                 string
	Wave, Slice, Attempt int
	DiffPath             string
	Token                string
	Candidates           []VerifyCandidate
}

// CandidateLines renders the merged candidates as one block for a single
// delimiter pair — distilled claims only, no lens names and no reasoning
// (design D6): a candidate is another agent's words about implementer-
// authored code, exactly the laundering path PriorFindingLines closes.
func (d VerifyData) CandidateLines() string {
	var b strings.Builder
	for _, c := range d.Candidates {
		fmt.Fprintf(&b, "%s %s %s:%d — %s: %s\n", c.ID, c.Severity, c.File, c.Line, c.Title, c.Detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

// PriorFinding is one finding from the pass a scoped review is confirming.
// brief keeps its own shape rather than importing internal/backend so this
// package stays a leaf; the caller maps backend.Finding onto it.
type PriorFinding struct {
	Severity string
	File     string
	Title    string
	Detail   string
	Line     int
}

// ReviewData fills the reviewer templates.
type ReviewData struct {
	Gate, Title, Token, Schema string
	Files                      map[string]string
	Diff                       string
	TaskDescription            string
	VerifyOutput               string
	// PriorFindings is non-empty only for the scoped confirming pass.
	PriorFindings []PriorFinding
}

// PriorFindingLines renders the previous pass's findings as one block, so
// the scoped template can quote them in a single delimiter pair the way
// alignment-verdicts.md quotes ClauseLines.
//
// They have to be quoted. A finding is a reviewer's summary of a
// user-authored spec.md, so a directive planted in the spec can be laundered
// through a finding's title or detail into the next reviewer's instructions
// unless it arrives inside the markers, declared as data — the invariant
// this package's doc comment states for every artifact it renders.
func (d ReviewData) PriorFindingLines() string {
	var b strings.Builder
	for _, f := range d.PriorFindings {
		fmt.Fprintf(&b, "%s %s:%d — %s: %s\n", f.Severity, f.File, f.Line, f.Title, lineSafe.Replace(f.Detail))
	}
	return strings.TrimRight(b.String(), "\n")
}

// lineSafe collapses the line breaks in a finding's detail — the one field
// that carries free prose — to single spaces. PriorFindingLines renders one
// finding per line and the reviewer counts findings by line, so a detail
// with a newline in it would otherwise arrive as several.
var lineSafe = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// RunData fills the `run` op instruction templates. Every field is set for
// every step — the templates pick out what they need — so a step that grows
// an input does not have to be threaded through the others.
type RunData struct{ Slug, Topic, SpecPath, GoalsPath, Branch, Base, InputsPath, RetroPath string }

var funcs = template.FuncMap{
	"quote": Quote,
	"join":  strings.Join,
}

// Render executes templates/<name>.md with data.
func Render(name string, data any) (string, error) {
	src, err := files.ReadFile("templates/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("brief: unknown template %q", name)
	}
	t, err := template.New(name).Funcs(funcs).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err = t.Execute(&out, data); err != nil {
		return "", fmt.Errorf("brief %s: %w", name, err)
	}
	return out.String(), nil
}
