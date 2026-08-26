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
		return nil, ErrNoFrontmatter
	}

	out := make(map[string]string, end-start-1)
	for _, ln := range lines[start+1 : end] {
		key, value, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}
	return out, nil
}

// Body returns the markdown after the frontmatter block, or the whole text
// when there is none — the part of an agent definition that is the same on
// every host, since only the envelope a host reads differs.
func Body(md string) string {
	if !strings.HasPrefix(md, "---\n") {
		return md
	}
	_, body, ok := strings.Cut(md[len("---\n"):], "\n---\n")
	if !ok {
		return md
	}
	return strings.TrimLeft(body, "\n")
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
