package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	setversion "github.com/monrad/takt/internal/tools/setversion"
)

// write is a small test helper: create dir/name with contents.
func write(t *testing.T, dir, name, contents string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

const pluginFixture = `{
  "name": "takt",
  "version": "0.1.0",
  "description": "x"
}
`

const marketplaceFixture = `{
  "name": "monrad-takt",
  "version": "0.1.0",
  "plugins": [
    {
      "name": "takt",
      "version": "0.1.0"
    }
  ]
}
`

// setup writes the two manifests setversion rewrites into dir/.claude-plugin.
func setup(t *testing.T, dir string) {
	t.Helper()
	write(t, dir, filepath.Join(".claude-plugin", "plugin.json"), pluginFixture)
	write(t, dir, filepath.Join(".claude-plugin", "marketplace.json"), marketplaceFixture)
}

// TestRewritesBothVersionFieldsInBothFiles covers the brief's central case:
// every "version": "…" line in both manifests is rewritten, including
// marketplace.json's two occurrences (its own version and plugins[0].version).
func TestRewritesBothVersionFieldsInBothFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setup(t, dir)

	if code := setversion.Run([]string{"0.2.0"}, &strings.Builder{}, dir); code != 0 {
		t.Fatalf("Run exit = %d", code)
	}

	plugin, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if strings.Count(string(plugin), `"version": "0.2.0"`) != 1 {
		t.Fatalf("plugin.json = %s", plugin)
	}
	marketplace, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"))
	if strings.Count(string(marketplace), `"version": "0.2.0"`) != 2 {
		t.Fatalf("marketplace.json = %s", marketplace)
	}
}

// TestRewriteIsANoopWhenTheVersionAlreadyMatches pins `task version:set
// VERSION=0.1.0` being a no-diff rewrite: replacing a value with itself must
// not disturb any other byte of the file.
func TestRewriteIsANoopWhenTheVersionAlreadyMatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setup(t, dir)
	before, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))

	if code := setversion.Run([]string{"0.1.0"}, &strings.Builder{}, dir); code != 0 {
		t.Fatal("Run failed")
	}

	after, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if string(before) != string(after) {
		t.Fatalf("rewriting the same version changed the file:\nbefore: %q\nafter:  %q", before, after)
	}
}

// TestRefusesANonSemverArgument covers the brief's refusal case: setversion
// must reject anything that is not x.y.z before touching either file.
func TestRefusesANonSemverArgument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setup(t, dir)
	before, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))

	var errb strings.Builder
	for _, bad := range []string{"v0.1.0", "0.1", "0.1.0-rc1", "latest", ""} {
		errb.Reset()
		if code := setversion.Run([]string{bad}, &errb, dir); code == 0 {
			t.Fatalf("%q: must be refused", bad)
		}
		if errb.Len() == 0 {
			t.Fatalf("%q: must explain the refusal", bad)
		}
	}

	after, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if string(before) != string(after) {
		t.Fatal("a refused argument must not touch the manifests")
	}
}
