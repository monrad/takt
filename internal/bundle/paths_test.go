package bundle_test

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
)

func TestCheckRelPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ok := []string{"a.go", "dir/b.go", "docs/x/y.md"}
	for _, p := range ok {
		if err := bundle.CheckRelPath(root, p); err != nil {
			t.Errorf("%q should be accepted: %v", p, err)
		}
	}
	bad := []string{"/abs/a.go", "../a.go", "dir/../../a.go", "", "dir/./../../x"}
	for _, p := range bad {
		if err := bundle.CheckRelPath(root, p); err == nil {
			t.Errorf("%q should be rejected", p)
		}
	}
}

func TestValidSlug(t *testing.T) {
	t.Parallel()
	ok := []string{"demo", "issue-2154", "a-b-c", "run", "x", "9", strings.Repeat("a", bundle.MaxSlugLen)}
	for _, s := range ok {
		if err := bundle.ValidSlug(s); err != nil {
			t.Errorf("%q should be accepted: %v", s, err)
		}
	}
	bad := []string{
		"../../x", "My Feature", "UPPER", "-lead", "trail-", "a--b", "", "a/b", "a_b", ".",
		strings.Repeat("a", bundle.MaxSlugLen+1),
	}
	for _, s := range bad {
		if err := bundle.ValidSlug(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}

func TestValidSlugErrorNamesValueAndRule(t *testing.T) {
	t.Parallel()
	err := bundle.ValidSlug("My Feature")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), `"My Feature"`) || !strings.Contains(err.Error(), "hyphen") {
		t.Fatalf("error must name the value and the rule: %v", err)
	}
	long := bundle.ValidSlug(strings.Repeat("a", bundle.MaxSlugLen+1))
	if long == nil || !strings.Contains(long.Error(), "48") {
		t.Fatalf("length error must name the limit: %v", long)
	}
}
