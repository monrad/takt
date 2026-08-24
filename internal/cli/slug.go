package cli

import (
	"regexp"
	"strings"
)

var issueRe = regexp.MustCompile(`/issues/(\d+)`)
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// maxSlugWords is the number of leading words kept when a topic has no
// issue link (spec §18).
const maxSlugWords = 6

// maxSlugLen is the hard cap on a derived slug's length (spec §18).
const maxSlugLen = 48

// deriveSlug implements spec §18: issue-<n> for issue links, else the kebab
// case of the first six words; "run" when nothing usable remains.
func deriveSlug(topic string) string {
	if m := issueRe.FindStringSubmatch(topic); m != nil {
		return "issue-" + m[1]
	}
	words := strings.Fields(strings.ToLower(topic))
	if len(words) > maxSlugWords {
		words = words[:maxSlugWords]
	}
	s := nonSlug.ReplaceAllString(strings.Join(words, "-"), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "run"
	}
	if len(s) > maxSlugLen {
		s = strings.Trim(s[:maxSlugLen], "-")
	}
	return s
}
