//nolint:testpackage // tests an unexported helper
package cli

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
)

func TestDeriveSlug(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"full https://github.com/bit-mover/BitMover/issues/2154 — Cedar generator can emit": "issue-2154",
		"Add pluralization rule for UpDownCounter names (issue #29)":                        "add-pluralization-rule-for-updowncounter-names",
		"  Fix   the THING!!  ": "fix-the-thing",
		"":                      "run",
	}
	for in, want := range cases {
		if got := deriveSlug(in); got != want {
			t.Errorf("deriveSlug(%q) = %q, want %q", in, got, want)
		}
	}
	long := deriveSlug("one two three four five six seven eight nine ten")
	if long != "one-two-three-four-five-six" {
		t.Errorf("first six words: %q", long)
	}
}

// TestDeriveSlugAlwaysValid pins the invariant review finding 1's fix rests
// on: --slug is rejected unless it looks like something deriveSlug could
// have produced, so deriveSlug must never produce something ValidSlug
// rejects.
func TestDeriveSlugAlwaysValid(t *testing.T) {
	t.Parallel()
	topics := []string{
		"",
		"   ",
		"---",
		"!!! ??? ***",
		"Add a greeting",
		"full https://github.com/bit-mover/BitMover/issues/2154 — Cedar generator can emit",
		"UPPER CASE TOPIC",
		"-leading dash",
		"trailing dash-",
		"a  b   c",
		"café naïve résumé",
		"one two three four five six seven eight nine ten",
		strings.Repeat("supercalifragilistic ", 6),
		strings.Repeat("x", 200),
		"a" + strings.Repeat("-", 60) + "b",
	}
	for _, topic := range topics {
		got := deriveSlug(topic)
		if err := bundle.ValidSlug(got); err != nil {
			t.Errorf("deriveSlug(%q) = %q, which ValidSlug rejects: %v", topic, got, err)
		}
	}
}
