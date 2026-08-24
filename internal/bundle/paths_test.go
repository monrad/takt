package bundle_test

import (
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
