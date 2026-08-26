// Package prompt reads the command prompt so tests can hold it to the Go
// vocabulary (spec §6, §14 "prompt" row). It has no runtime callers.
package prompt

import (
	"os"
	"strings"
)

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
