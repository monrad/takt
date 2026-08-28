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

	if code := setversion.Run([]string{"0.2.0"}, &strings.Builder{}, &strings.Builder{}, dir); code != 0 {
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

	if code := setversion.Run([]string{"0.1.0"}, &strings.Builder{}, &strings.Builder{}, dir); code != 0 {
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
		if code := setversion.Run([]string{bad}, &strings.Builder{}, &errb, dir); code == 0 {
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

	if code := setversion.Run([]string{"0.2.0"}, &strings.Builder{}, &strings.Builder{}, dir); code != 0 {
		t.Fatalf("Run exit = %d", code)
	}

	got, _ := os.ReadFile(filepath.Join(dir, skillPath))
	want := "## Handshake\n\nRun `takt version --expect 0.2.0`. If it exits non-zero, stop.\n"
	if string(got) != want {
		t.Fatalf("skill = %q, want %q", got, want)
	}

	// Stamping the same version twice must be a byte-for-byte no-op: `task
	// version:set` is run to confirm a release version as often as to change
	// one, and a rewrite that drifted a byte per run would show up as a dirty
	// tree nobody asked for.
	if code := setversion.Run([]string{"0.2.0"}, &strings.Builder{}, &strings.Builder{}, dir); code != 0 {
		t.Fatalf("second Run exit = %d", code)
	}
	for _, rel := range []string{skillPath, filepath.Join(".claude-plugin", "plugin.json"),
		filepath.Join(".claude-plugin", "marketplace.json")} {
		again, _ := os.ReadFile(filepath.Join(dir, rel))
		if rel == skillPath && string(again) != want {
			t.Fatalf("second stamp changed the skill: %q", again)
		}
		if !strings.Contains(string(again), "0.2.0") {
			t.Fatalf("%s lost its version on the second stamp: %q", rel, again)
		}
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
	b, _ = os.ReadFile(p)
	if string(b) != "Run `takt version --expect 0.3.0`. If it fails\n" {
		t.Fatalf("rewriting an already-rewritten line: %q", b)
	}
	if err := setversion.RewriteExpect(p, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(p)
	if string(again) != string(b) {
		t.Fatalf("stamping the same version twice changed the file:\nfirst:  %q\nsecond: %q", b, again)
	}
	if err := os.WriteFile(p, []byte("no handshake here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setversion.RewriteExpect(p, "0.4.0"); err == nil {
		t.Fatal("a skill without the handshake line must be an error")
	}
}

// TestPrintPrintsTheManifestVersionAndWritesNothing covers `task build`'s use
// of --print: it reads the same "version" field the rewrite mode writes and
// leaves every file — including the manifest it read — untouched, so
// `task build` can shell it out without dirtying the tree it is building.
func TestPrintPrintsTheManifestVersionAndWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	setup(t, dir)
	beforePlugin, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	beforeMarketplace, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"))
	beforeSkill, _ := os.ReadFile(filepath.Join(dir, skillPath))

	var out strings.Builder
	if code := setversion.Run([]string{"--print"}, &out, &strings.Builder{}, dir); code != 0 {
		t.Fatalf("Run(--print) exit = %d, stdout = %q", code, out.String())
	}
	if got := out.String(); got != "0.1.0\n" {
		t.Fatalf("Run(--print) stdout = %q, want %q", got, "0.1.0\n")
	}

	afterPlugin, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	afterMarketplace, _ := os.ReadFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"))
	afterSkill, _ := os.ReadFile(filepath.Join(dir, skillPath))
	if string(beforePlugin) != string(afterPlugin) {
		t.Fatalf("--print rewrote plugin.json:\nbefore: %q\nafter:  %q", beforePlugin, afterPlugin)
	}
	if string(beforeMarketplace) != string(afterMarketplace) {
		t.Fatalf("--print rewrote marketplace.json:\nbefore: %q\nafter:  %q", beforeMarketplace, afterMarketplace)
	}
	if string(beforeSkill) != string(afterSkill) {
		t.Fatalf("--print rewrote the skill:\nbefore: %q\nafter:  %q", beforeSkill, afterSkill)
	}
}

// TestPrintFailsOnAMissingOrVersionlessManifest covers --print's two refusal
// cases: a plugin.json that is not there at all, and one that has no
// "version" field for versionLine to find — the same failure rewriteVersion
// would hit, since --print reads with the identical regexp.
func TestPrintFailsOnAMissingOrVersionlessManifest(t *testing.T) {
	t.Parallel()

	t.Run("missing manifest", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var errb strings.Builder
		if code := setversion.Run([]string{"--print"}, &strings.Builder{}, &errb, dir); code == 0 {
			t.Fatal("Run(--print) must fail when plugin.json does not exist")
		}
		if errb.Len() == 0 {
			t.Fatal("Run(--print) must explain the failure")
		}
	})

	t.Run("versionless manifest", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		write(t, dir, filepath.Join(".claude-plugin", "plugin.json"), `{"name": "takt"}`)
		var errb strings.Builder
		if code := setversion.Run([]string{"--print"}, &strings.Builder{}, &errb, dir); code == 0 {
			t.Fatal("Run(--print) must fail on a manifest with no \"version\" field")
		}
		if errb.Len() == 0 {
			t.Fatal("Run(--print) must explain the failure")
		}
	})
}
