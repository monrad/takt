// Package prompt reads the command prompt so tests can hold it to the Go
// vocabulary (spec §6, §14 "prompt" row). The takt binary never calls it:
// its callers are those tests and internal/hosts, which reuses the
// frontmatter parser to render agents/*.md for other hosts.
package prompt

import (
	"errors"
	"os"
	"strings"
)

// ErrNoFrontmatter is returned by [Frontmatter] when md has no `---` block.
var ErrNoFrontmatter = errors.New("prompt: no frontmatter block")

// Load returns the prompt's markdown.
func Load(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// Section returns the text under `## <heading>` up to the next `## `.
func Section(md, heading string) string {
	lines := strings.Split(md, "\n")
	var out []string
	in := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			if in {
				break
			}
			in = strings.TrimSpace(strings.TrimPrefix(ln, "## ")) == heading
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

// Frontmatter parses the `---`-delimited block at the top of md (an agent
// definition's YAML-ish header) into key/value pairs: each line between the
// first two `---` lines is split on the first `:`, both sides are trimmed,
// and matching surrounding quotes are stripped from the value. It errors if
// md has fewer than two `---` lines.
func Frontmatter(md string) (map[string]string, error) {
	front, _, ok := splitFrontmatter(md)
	if !ok {
		return nil, ErrNoFrontmatter
	}
	lines := strings.Split(front, "\n")
	out := make(map[string]string, len(lines))
	for _, ln := range lines {
		key, value, found := strings.Cut(ln, ":")
		if !found {
			continue
		}
		out[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}
	return out, nil
}

// splitFrontmatter cuts md at its `---`-delimited header: front is the text
// between the first two delimiter lines, body everything after the second,
// and ok reports whether both were found (front and body are empty and md is
// returned as body when they were not).
//
// [Frontmatter] and [Body] both read a document through this one definition
// of "where the header ends" on purpose. When they disagreed — one scanning
// trimmed `---` lines, the other requiring the file to open with "---\n" —
// a CRLF file or one with a blank first line parsed as having frontmatter
// and rendered as having none, which copies the header into the body as
// prose instead of dropping it.
//
//nolint:nonamedreturns // front/body are two same-typed strings; naming them is what tells the call site which is which
func splitFrontmatter(md string) (front, body string, ok bool) {
	lines := strings.Split(md, "\n")
	start, end := -1, -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) != "---" {
			continue
		}
		if start == -1 {
			start = i
			continue
		}
		end = i
		break
	}
	if start == -1 || end == -1 {
		return "", md, false
	}
	return strings.Join(lines[start+1:end], "\n"), strings.Join(lines[end+1:], "\n"), true
}

// Body returns the markdown after the frontmatter block, or the whole text
// when there is none — the part of an agent definition that is the same on
// every host, since only the envelope a host reads differs. The blank line
// that conventionally follows the block is dropped, CRLF included.
func Body(md string) string {
	_, body, ok := splitFrontmatter(md)
	if !ok {
		return md
	}
	return strings.TrimLeft(body, "\r\n")
}

// minQuotedLen is the shortest string that can carry a matching pair of
// leading/trailing quotes: the two quote characters themselves.
const minQuotedLen = 2

// unquote strips one layer of matching leading/trailing quotes ( " or ' )
// from s, if present.
func unquote(s string) string {
	if len(s) < minQuotedLen {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' || first == '\'') && first == last {
		return s[1 : len(s)-1]
	}
	return s
}
