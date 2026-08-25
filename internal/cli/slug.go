package cli

import (
	"regexp"
	"strings"

	"github.com/monrad/takt/internal/bundle"
)

var issueRe = regexp.MustCompile(`/issues/(\d+)`)
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// maxSlugWords is the number of leading words kept when a topic has no
// issue link (spec §18).
const maxSlugWords = 6

// deriveSlug implements spec §18: issue-<n> for issue links, else the kebab
// case of the first six words; "run" when nothing usable remains. Every
// result satisfies [bundle.ValidSlug] — that invariant is what lets init
// reject a hand-written --slug outside this alphabet (review finding 1).
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
	if len(s) > bundle.MaxSlugLen {
		s = strings.Trim(s[:bundle.MaxSlugLen], "-")
	}
	return s
}
