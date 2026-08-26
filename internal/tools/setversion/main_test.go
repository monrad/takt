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

// skillFixture is the Copilot skill's handshake line — the third place the
// version is written, and the only one that is prose rather than JSON.
const skillFixture = "## Handshake\n\nRun `takt version --expect 0.1.0`. If it exits non-zero, stop.\n"

// skillPath is where setversion looks for that line, relative to the root.
var skillPath = filepath.Join("hosts", "copilot", "skills", "takt", "SKILL.md")

// setup writes the three files setversion rewrites into dir.
func setup(t *testing.T, dir string) {
	t.Helper()
	write(t, dir, filepath.Join(".claude-plugin", "plugin.json"), pluginFixture)
	write(t, dir, filepath.Join(".claude-plugin", "marketplace.json"), marketplaceFixture)
	write(t, dir, skillPath, skillFixture)
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

// TestRunStampsTheCopilotSkill covers the third file: the Copilot CLI host
// has no plugin manifest to read its version out of, so `task version:set`
// has to stamp the skill's handshake line too, or a release would ship a
// skill pinning the previous version (spec §6.1).
func TestRunStampsTheCopilotSkill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setup(t, dir)

	if code := setversion.Run([]string{"0.2.0"}, &strings.Builder{}, dir); code != 0 {
		t.Fatalf("Run exit = %d", code)
	}

	got, _ := os.ReadFile(filepath.Join(dir, skillPath))
	want := "## Handshake\n\nRun `takt version --expect 0.2.0`. If it exits non-zero, stop.\n"
	if string(got) != want {
		t.Fatalf("skill = %q, want %q", got, want)
	}
}

// TestSetVersionRewritesTheSkillHandshake drives the rewriter directly: the
// version inside the code span is replaced and nothing around it — the
// closing backtick and the sentence — is disturbed, and a file with no
// handshake line at all is an error rather than a silent no-op.
func TestSetVersionRewritesTheSkillHandshake(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte("Run `takt version --expect 0.1.0`. If it fails\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setversion.RewriteExpect(p, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "Run `takt version --expect 0.2.0`. If it fails\n" {
		t.Fatalf("%q", b)
	}
	if err := setversion.RewriteExpect(p, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("no handshake here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setversion.RewriteExpect(p, "0.4.0"); err == nil {
		t.Fatal("a skill without the handshake line must be an error")
	}
}
