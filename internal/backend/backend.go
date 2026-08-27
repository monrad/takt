// Package backend runs headless reviewers (spec §8): a Reviewer judges an
// artifact set or a diff and returns a verdict with findings. Prompts are
// rendered elsewhere (internal/brief); this package only executes them.
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Verdicts (spec §8.1).
const (
	VerdictApprove = "approve"
	VerdictRework  = "rework"
	VerdictReject  = "reject"
	VerdictError   = "error"
)

// ErrNoHealthyReviewer is returned by Select when the chain is exhausted.
var ErrNoHealthyReviewer = errors.New("backend: no healthy reviewer in the chain")

// Reviewer names, as used in Registry, ReviewRequest chains, and each
// reviewer's own Name()/provenance fields.
const (
	nameFake    = "fake"
	nameCopilot = "copilot"
	nameClaude  = "claude"
)

// ReviewRequest is one review to run. Prompt is the complete rendered text.
type ReviewRequest struct {
	Rubric   string
	Title    string
	Prompt   string
	RepoRoot string
	Model    string
	Effort   string
	Timeout  time.Duration
	LogDir   string
	LogID    string
}

// Finding is one reviewer finding.
type Finding struct {
	Severity string `json:"severity"` // blocking | major | minor | nit
	File     string `json:"file"`
	Line     int    `json:"line"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

// ReviewResult is the parsed reviewer output plus provenance.
type ReviewResult struct {
	Verdict  string        `json:"verdict"`
	Summary  string        `json:"summary"`
	Findings []Finding     `json:"findings"`
	Provider string        `json:"provider,omitempty"`
	Model    string        `json:"model,omitempty"`
	Reason   string        `json:"reason,omitempty"` // for VerdictError
	Elapsed  time.Duration `json:"-"`
	Raw      string        `json:"-"`
}

// SeverityCounts tallies a result's findings by severity. The gate decision
// reads this off the receipt rather than re-opening the findings file, so
// neither gate.Compute nor Decide has to parse a review to learn whether it
// found anything blocking. Nil when there are no findings, so an empty tally
// and a receipt written before severities existed read alike.
func (r ReviewResult) SeverityCounts() map[string]int {
	if len(r.Findings) == 0 {
		return nil
	}
	m := make(map[string]int, len(r.Findings))
	for _, f := range r.Findings {
		m[f.Severity]++
	}
	return m
}

// ResultSchema is handed to `claude --json-schema` and quoted in prompts.
const ResultSchema = `{"type":"object","required":["verdict","summary"],"properties":{"verdict":{"type":"string","enum":["approve","rework","reject"]},"summary":{"type":"string"},"findings":{"type":"array","items":{"type":"object","required":["severity","title"],"properties":{"severity":{"type":"string","enum":["blocking","major","minor","nit"]},"file":{"type":"string"},"line":{"type":"integer"},"title":{"type":"string"},"detail":{"type":"string"}}}}}}`

// Reviewer is a headless review backend.
type Reviewer interface {
	Name() string
	Healthy(ctx context.Context) error
	Review(ctx context.Context, req ReviewRequest) (ReviewResult, error)
}

// Registry returns every known reviewer keyed by name.
func Registry(getenv func(string) string) map[string]Reviewer {
	return map[string]Reviewer{
		nameFake:    &fakeReviewer{getenv: getenv},
		nameCopilot: &copilotReviewer{},
		nameClaude:  &claudeReviewer{},
	}
}

// Select returns the first healthy reviewer in chain (spec §8.1).
func Select(ctx context.Context, chain []string, reg map[string]Reviewer) (Reviewer, error) {
	var errs []string
	for _, name := range chain {
		r, ok := reg[name]
		if !ok {
			errs = append(errs, name+": unknown backend")
			continue
		}
		if err := r.Healthy(ctx); err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		return r, nil
	}
	return nil, fmt.Errorf("%w (%s)", ErrNoHealthyReviewer, strings.Join(errs, "; "))
}

// jsonFence is the fenced-code-block marker a reviewer wraps its JSON verdict
// in when it also emits prose.
const jsonFence = "```json"

// closeFence terminates a fenced code block.
const closeFence = "```"

// ExtractJSON finds the reviewer's JSON: the last fenced ```json block if
// one is present and valid, else the last balanced top-level {...} object
// in the text whose content is itself valid JSON.
func ExtractJSON(text string) ([]byte, error) {
	if b, ok := lastFencedJSON(text); ok {
		return b, nil
	}
	if b, ok := lastBalancedObject(text); ok {
		return b, nil
	}
	return nil, errors.New("backend: no JSON object found in reviewer output")
}

// lastFencedJSON returns the content of the last ```json ... ``` block, if
// any, and if that content is itself valid JSON.
func lastFencedJSON(text string) ([]byte, bool) {
	i := strings.LastIndex(text, jsonFence)
	if i < 0 {
		return nil, false
	}
	rest := text[i+len(jsonFence):]
	before, _, ok := strings.Cut(rest, closeFence)
	if !ok {
		return nil, false
	}
	cand := strings.TrimSpace(before)
	if !json.Valid([]byte(cand)) {
		return nil, false
	}
	return []byte(cand), true
}

// lastBalancedObject makes a single string-aware forward pass over text,
// tracking brace depth while skipping over JSON string literals (so braces
// inside a string value never perturb the count), and returns the last
// top-level {...} span found whose content validates as JSON. Later spans
// overwrite earlier ones, so the result is the last, not the first, match.
func lastBalancedObject(text string) ([]byte, bool) {
	var best []byte
	found := false
	depth, start := 0, 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"':
			i = skipString(text, i)
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue // stray close brace outside any object; ignore
			}
			depth--
			if depth == 0 {
				if cand := text[start : i+1]; json.Valid([]byte(cand)) {
					best, found = []byte(cand), true
				}
			}
		}
	}
	return best, found
}

// skipString returns the index of the closing, unescaped quote of the JSON
// string literal that opens at text[i] (a '"'), or len(text) if it is
// unterminated. Used by lastBalancedObject to step over string contents
// without their braces or escaped quotes disturbing brace counting.
func skipString(text string, i int) int {
	for i++; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++ // skip the escaped character, whatever it is
		case '"':
			return i
		}
	}
	return len(text)
}

// ParseResult decodes and validates a reviewer's JSON.
func ParseResult(b []byte) (ReviewResult, error) {
	var r ReviewResult
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("backend: reviewer JSON: %w", err)
	}
	switch r.Verdict {
	case VerdictApprove, VerdictRework, VerdictReject:
	default:
		return r, fmt.Errorf("backend: unknown verdict %q", r.Verdict)
	}
	return r, nil
}

// errorResult builds a VerdictError result.
func errorResult(provider, model, reason, raw string, elapsed time.Duration) ReviewResult {
	return ReviewResult{
		Verdict: VerdictError, Summary: "review failed", Reason: reason,
		Provider: provider, Model: model, Raw: raw, Elapsed: elapsed,
	}
}
