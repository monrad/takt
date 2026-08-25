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
	"strings"
	"text/template"
)

//go:embed templates/*.md
var files embed.FS

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
	SpecExcerpt     string
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

// AlignmentData fills the two alignment templates.
type AlignmentData struct {
	Mode, Anchor, Token           string
	Clauses                       []Clause
	SpecText, PlanText, IndexText string
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
type GoalAssessorData struct {
	Slug          string
	Token         string
	GoalsText     string
	DiffStat      string
	VerifySummary string
	Goals         []GoalLine
}

// ReviewData fills the three reviewer templates.
type ReviewData struct {
	Gate, Title, Token, Schema string
	Files                      map[string]string
	Diff                       string
	TaskDescription            string
	VerifyOutput               string
}

// RunData fills the `run` op instruction templates.
type RunData struct{ Slug, Topic, SpecPath, GoalsPath string }

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
