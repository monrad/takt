package brief_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/brief"
)

func TestLensesListsTheShippedLensesSorted(t *testing.T) {
	t.Parallel()
	want := []string{"consistency", "correctness", "docs", "intent", "simplicity", "tests"}
	if got := brief.Lenses(); !slices.Equal(got, want) {
		t.Fatalf("Lenses() = %v, want %v", got, want)
	}
}

func TestLensRubricsDeconflict(t *testing.T) {
	t.Parallel()
	// Each rubric names what it excludes, so two lenses cannot silently
	// claim the same ground (design §7.2). The exclusion is asserted by a
	// phrase each file must keep.
	musts := map[string]string{
		"correctness": "intent",       // hands task-match off
		"intent":      "correctness",  // hands generic boundary bugs off
		"tests":       "verify",       // does not run tests; Go ran verify
		"simplicity":  "project-wide", // search before "unused" claims
		"consistency": "slice",        // scope is across the slice's tasks
		"docs":        "already",      // report only what is not already documented
	}
	for lens, must := range musts {
		text, err := brief.LensRubric(lens)
		if err != nil {
			t.Fatalf("LensRubric(%q): %v", lens, err)
		}
		if !strings.Contains(strings.ToLower(text), must) {
			t.Errorf("lenses/%s.md does not mention %q", lens, must)
		}
	}
}

func TestLensRubricUnknown(t *testing.T) {
	t.Parallel()
	if _, err := brief.LensRubric("nope"); err == nil {
		t.Fatal("LensRubric(nope) did not error")
	}
}
