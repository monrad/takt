package finish

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
)

// GoalVerdict is one goal's assessment (spec §7.5 step 2).
type GoalVerdict struct {
	ID        string   `json:"id"`
	Verdict   string   `json:"verdict"` // achieved | partial | missed
	Evidence  string   `json:"evidence"`
	Citations []string `json:"citations"`
}

// GoalsRecord is finish/goals.json: the verdicts at SHA plus any waivers.
type GoalsRecord struct {
	SHA      string            `json:"sha"`
	Verdicts []GoalVerdict     `json:"verdicts"`
	Waived   map[string]string `json:"waived,omitempty"` // goal id → reason
	At       time.Time         `json:"at"`
}

// Unmet lists goals neither achieved nor waived, in goals.md order.
func (r GoalsRecord) Unmet() []GoalVerdict {
	var out []GoalVerdict
	for _, v := range r.Verdicts {
		if v.Verdict != verdictAchieved && r.Waived[v.ID] == "" {
			out = append(out, v)
		}
	}
	return out
}

// verdictAchieved is the one verdict that satisfies a goal; everything else
// is unmet until it is waived.
const verdictAchieved = "achieved"

// verdicts is the closed set of verdicts an assessor may return.
var verdicts = map[string]bool{verdictAchieved: true, "partial": true, "missed": true}

// GoalsPath is where the record lives.
func GoalsPath(bundleDir string) string { return filepath.Join(bundleDir, "finish", "goals.json") }

// ReadGoals returns (nil, nil) when no record exists.
func ReadGoals(bundleDir string) (*GoalsRecord, error) {
	b, err := os.ReadFile(GoalsPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // documented "no record" sentinel, as ReadVerify
	}
	if err != nil {
		return nil, err
	}
	var r GoalsRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WriteGoals writes the record atomically.
func WriteGoals(bundleDir string, r GoalsRecord) error {
	return bundle.WriteJSONAtomic(GoalsPath(bundleDir), r)
}

// ParseVerdicts validates the assessor's JSON: every goal id exactly once,
// a known verdict, non-empty evidence. Unknown ids are rejected so a
// hallucinated goal cannot be "achieved".
func ParseVerdicts(js []byte, ids []string) ([]GoalVerdict, error) {
	var vs []GoalVerdict
	if err := json.Unmarshal(js, &vs); err != nil {
		return nil, fmt.Errorf("verdicts are not a JSON list: %w", err)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	seen := map[string]GoalVerdict{}
	for i := range vs {
		v := &vs[i]
		if err := checkVerdict(*v, want, seen); err != nil {
			return nil, err
		}
		if v.Citations == nil {
			v.Citations = []string{}
		}
		seen[v.ID] = *v
	}
	// Returned in goals.md order, not the order the assessor happened to
	// emit: the record is the user's list of goals with a verdict against
	// each, and Unmet walks it in that same order.
	out := make([]GoalVerdict, 0, len(ids))
	for _, id := range ids {
		v, ok := seen[id]
		if !ok {
			return nil, fmt.Errorf("goal %s has no verdict", id)
		}
		out = append(out, v)
	}
	return out, nil
}

// checkVerdict is ParseVerdicts's per-entry validation; seen holds the
// entries already accepted, keyed by goal id.
func checkVerdict(v GoalVerdict, want map[string]bool, seen map[string]GoalVerdict) error {
	_, dup := seen[v.ID]
	switch {
	case !want[v.ID]:
		return fmt.Errorf("verdict for unknown goal %q", v.ID)
	case dup:
		return fmt.Errorf("goal %s judged twice", v.ID)
	case !verdicts[v.Verdict]:
		return fmt.Errorf("goal %s: verdict %q is not achieved|partial|missed", v.ID, v.Verdict)
	case strings.TrimSpace(v.Evidence) == "":
		return fmt.Errorf("goal %s: evidence is empty", v.ID)
	}
	return nil
}

// CheckCitations reports every citation in vs that does not name a real
// place in the repository at root, one problem per violation, in verdict
// order and then citation order. A citation is `path:line` or
// `path:start-end` with the path relative to the repository root (spec
// §4.5), naming a regular file, and the range inside that file; an empty
// citations list is allowed, since a verdict may rest on a command's exit
// status rather than on a line of code.
//
// Containment is decided on the resolved paths, not lexically: an in-repo
// symlink pointing outside the tree is a path the repository does not own,
// and citing through it would put a line nobody can check into
// finish/goals.json.
func CheckCitations(vs []GoalVerdict, root string) []string {
	var problems []string
	for _, v := range vs {
		for _, c := range v.Citations {
			if bad := checkCitation(root, c); bad != "" {
				problems = append(problems, fmt.Sprintf(`%s: citation "%s" — %s`, v.ID, c, bad))
			}
		}
	}
	return problems
}

// citation is one parsed `path:line` or `path:start-end`.
type citation struct {
	path       string
	start, end int
}

// badGrammar is what every citation that is not path:line or path:start-end
// is rejected with — the message names the two shapes rather than the way
// this one missed them, so the assessor is told the grammar it must meet.
const badGrammar = "not path:line or path:start-end"

// checkCitation returns what is wrong with one citation, or "" when it names
// a line range inside a regular file contained in the repository at root.
func checkCitation(root, c string) string {
	cit, bad := parseCitation(c)
	if bad != "" {
		return bad
	}
	if escapesRepo(cit.path) {
		return "escapes the repository"
	}
	resolved, bad := resolveInRepo(root, cit.path)
	if bad != "" {
		return bad
	}
	return checkRange(resolved, cit)
}

// parseCitation splits a citation at its LAST colon — a Windows-shaped path
// or a file name with a colon in it keeps the line range readable — and
// parses the right side as a line or a line range.
func parseCitation(c string) (citation, string) {
	i := strings.LastIndex(c, ":")
	if i < 0 {
		return citation{}, badGrammar
	}
	p, lines := c[:i], c[i+1:]
	start, end, ok := parseLineRange(lines)
	if p == "" || !ok {
		return citation{}, badGrammar
	}
	return citation{path: p, start: start, end: end}, ""
}

// parseLineRange reads `<line>` or `<start>-<end>`; a range whose start is
// past its end is not a range, so it fails the grammar rather than the
// bounds check.
func parseLineRange(s string) (int, int, bool) {
	if a, b, isRange := strings.Cut(s, "-"); isRange {
		start, okA := decimal(a)
		end, okB := decimal(b)
		if !okA || !okB || start > end {
			return 0, 0, false
		}
		return start, end, true
	}
	n, ok := decimal(s)
	return n, n, ok
}

// decimal parses a run of decimal digits. A sign is not a digit, so `+1` and
// `-1` are not line numbers; an overflowing number is not one either.
func decimal(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// escapesRepo reports whether p is not a repository-relative path: absolute,
// rooted, or carrying a `..` segment. The segments are split on BOTH
// separators, so `dir\..\a.go` is rejected wherever takt runs — on Windows
// it is a real traversal, and on Linux it is one odd file name that still
// contains the segment the rule forbids. The test is on segments, not on a
// prefix, so a contained file named `..foo.go` stays a valid citation.
func escapesRepo(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	return slices.Contains(strings.FieldsFunc(p, isPathSeparator), "..")
}

// isPathSeparator is [strings.FieldsFunc]'s split test for escapesRepo.
func isPathSeparator(r rune) bool { return r == '/' || r == '\\' }

// resolveInRepo resolves p under root through symlinks and returns the
// resolved path, or what is wrong with it. Both sides are resolved so that a
// root reached through a symlink — /tmp on macOS is one — does not make
// every path in it look outside.
func resolveInRepo(root, p string) (string, string) {
	resolvedRoot, _ := filepath.EvalSymlinks(root)
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, p))
	if err != nil {
		return "", notAFile
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	// `rel == ".."` or a `../` prefix, not a bare `..` prefix: a contained
	// file named `..foo.go` has a relative path starting with `..` and is
	// inside the repository.
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "resolves outside the repository"
	}
	return resolved, ""
}

// notAFile covers everything that is not a readable regular file: a missing
// path, a directory, a device — the assessor cited something no reader can
// open at a line.
const notAFile = "not a file"

// checkRange reports whether the resolved path is a regular file whose line
// count covers the citation's range.
func checkRange(resolved string, c citation) string {
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return notAFile
	}
	// The path comes from an agent's reply, but it is the one resolveInRepo
	// resolved and proved inside the repository root, so reading it opens
	// nothing the repository does not already own — gosec's G304 does not
	// fire here, and a //nolint:gosec directive would be reported unused.
	data, err := os.ReadFile(resolved)
	if err != nil {
		return notAFile
	}
	lines := lineCount(data)
	switch {
	case c.start < firstLine:
		return fmt.Sprintf("line %d is not a line", c.start)
	case c.end > lines:
		return fmt.Sprintf("line %d is past the end (%d lines)", c.end, lines)
	}
	return ""
}

// firstLine is the number of a file's first line: citations are 1-based, as
// every editor and every `file:line` in a compiler's output is.
const firstLine = 1

// lineCount is how many lines data holds: one per newline, plus a last
// unterminated line when the file does not end in one. An empty file has no
// lines, so no citation into it can be in range.
func lineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}
