package prompt_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	flakePath      = "../../flake.nix"
	goreleaserPath = "../../.goreleaser.yaml"
)

// commentTail matches a `#` comment running to end of line, in both YAML and
// Nix: the `#` must open a token, so it is either at the start of the line or
// preceded by whitespace. That distinction matters — .goreleaser.yaml's cask
// hook embeds Ruby's `"#{staged_path}/takt"`, where the `#` follows a quote
// and is not a comment at all.
var commentTail = regexp.MustCompile(`(^|[ \t])#[^\n]*`)

// goosBlock captures the items of the builds' `goos:` list, so the platform
// assertion reads the setting itself rather than the whole file: a comment
// or a description that merely says "windows" is not a windows build.
var goosBlock = regexp.MustCompile(`(?m)^[ \t]*goos:[ \t]*(\[[^\]]*\])?[ \t]*\n((?:[ \t]*-[ \t]*\S+[ \t]*\n)*)`)

// stripComments blanks every comment in src so a test that scans raw text
// cannot be satisfied — or broken — by prose. Without it a commented-out
// `builtins.fromJSON` line still "proves" the flake reads the manifest.
func stripComments(src string) string {
	return commentTail.ReplaceAllString(src, "$1")
}

func readStripped(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return stripComments(string(b))
}

// TestFlakeReadsThePluginVersion pins the single source of the version: the
// nix package must read `.claude-plugin/plugin.json` rather than repeat the
// string, so `task version:set` stays the only writer (task-5 brief Step 1).
func TestFlakeReadsThePluginVersion(t *testing.T) {
	t.Parallel()
	flake := readStripped(t, flakePath)

	for _, want := range []string{"builtins.fromJSON", ".claude-plugin/plugin.json"} {
		if !strings.Contains(flake, want) {
			t.Errorf("flake.nix does not contain %q outside its comments — "+
				"the version must come from the plugin manifest", want)
		}
	}
}

// TestGoreleaserStampsTheVersion pins the release build to the same ldflags
// path the flake and the Taskfile use, and to the two supported platforms:
// no windows target ships (task-5 brief Step 1).
func TestGoreleaserStampsTheVersion(t *testing.T) {
	t.Parallel()
	config := readStripped(t, goreleaserPath)

	const stamp = "internal/version.Version={{.Version}}"
	if !strings.Contains(config, stamp) {
		t.Errorf(".goreleaser.yaml does not stamp the version: want ldflags containing %q", stamp)
	}

	blocks := goosBlock.FindAllStringSubmatch(config, -1)
	if len(blocks) == 0 {
		t.Fatal(".goreleaser.yaml has no goos: block — cannot tell which platforms ship")
	}
	for _, b := range blocks {
		platforms := b[1] + b[2]
		if strings.Contains(strings.ToLower(platforms), "windows") {
			t.Errorf("a goos: block lists windows — takt ships linux and darwin only:\n%s", platforms)
		}
	}
}
