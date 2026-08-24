//nolint:testpackage // tests an unexported helper
package cli

import "testing"

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
