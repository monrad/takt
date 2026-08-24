// Package goals parses goals.md (spec §7.2): the verbatim anchor plus the
// frozen list of success criteria the finish-time assessor checks.
package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Goal is one success criterion.
type Goal struct {
	ID       string
	Text     string
	Signal   string
	Evidence string
}

// Goals is the parsed file.
type Goals struct {
	Anchor string
	Items  []Goal
}

// Signals is the closed set of evidence kinds.
var Signals = []string{"test", "command", "artifact", "docs"}

var goalLine = regexp.MustCompile(`^- (G\d+) — (.+?) · signal: (\w+) · evidence: (.+)$`)

// Hash returns "sha256:<hex>" over the raw bytes.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IDs returns the goal ids in file order.
func (g Goals) IDs() []string {
	ids := make([]string, len(g.Items))
	for i, it := range g.Items {
		ids[i] = it.ID
	}
	return ids
}

// Parse reads the anchor block and the goal list, enforcing ids G1..Gn.
func Parse(b []byte) (Goals, error) {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var g Goals
	section := ""
	inFence, anchorSeen := false, false
	var anchor []string
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "## "):
			section = strings.TrimSpace(strings.TrimPrefix(ln, "## "))
			continue
		case section == "Anchor" && strings.HasPrefix(ln, "```"):
			if inFence {
				inFence, anchorSeen = false, true
			} else if !anchorSeen {
				inFence = true
			}
			continue
		case section == "Anchor" && inFence:
			anchor = append(anchor, ln)
		case section == "Goals" && strings.HasPrefix(ln, "- "):
			item, err := parseGoalLine(ln)
			if err != nil {
				return Goals{}, err
			}
			g.Items = append(g.Items, item)
		}
	}
	if !anchorSeen {
		return Goals{}, errors.New("goals.md: missing `## Anchor` with a fenced verbatim block")
	}
	g.Anchor = strings.Join(anchor, "\n")
	if len(g.Items) == 0 {
		return Goals{}, errors.New("goals.md: no goals under `## Goals`")
	}
	if err := validateIDs(g.Items); err != nil {
		return Goals{}, err
	}
	return g, nil
}

// parseGoalLine parses and validates one `- G<n> — text · signal: <s> ·
// evidence: <e>` line.
func parseGoalLine(ln string) (Goal, error) {
	m := goalLine.FindStringSubmatch(ln)
	if m == nil {
		return Goal{}, fmt.Errorf(
			"goals.md: malformed goal line %q (want `- G<n> — text · signal: <s> · evidence: <e>`)",
			ln,
		)
	}
	sig := m[3]
	if !slices.Contains(Signals, sig) {
		return Goal{}, fmt.Errorf("goals.md: %s has unknown signal %q", m[1], sig)
	}
	return Goal{ID: m[1], Text: m[2], Signal: sig, Evidence: m[4]}, nil
}

// validateIDs enforces that goal ids are exactly G1..Gn in order, with no
// gaps or duplicates.
func validateIDs(items []Goal) error {
	for i, it := range items {
		if it.ID != "G"+strconv.Itoa(i+1) {
			return fmt.Errorf("goals.md: ids must be G1..Gn in order; found %s at position %d", it.ID, i+1)
		}
	}
	return nil
}
