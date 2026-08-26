package prompt_test

import (
	"os"
	"strings"
	"testing"
)

const (
	flakePath      = "../../flake.nix"
	goreleaserPath = "../../.goreleaser.yaml"
)

// TestFlakeReadsThePluginVersion pins the single source of the version: the
// nix package must read `.claude-plugin/plugin.json` rather than repeat the
// string, so `task version:set` stays the only writer (task-5 brief Step 1).
func TestFlakeReadsThePluginVersion(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(flakePath)
	if err != nil {
		t.Fatal(err)
	}
	flake := string(b)

	for _, want := range []string{"builtins.fromJSON", ".claude-plugin/plugin.json"} {
		if !strings.Contains(flake, want) {
			t.Errorf("flake.nix does not contain %q — the version must come from the plugin manifest", want)
		}
	}
}

// TestGoreleaserStampsTheVersion pins the release build to the same ldflags
// path the flake and the Taskfile use, and to the two supported platforms:
// no windows target ships (task-5 brief Step 1).
func TestGoreleaserStampsTheVersion(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(goreleaserPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(b)

	const stamp = "internal/version.Version={{.Version}}"
	if !strings.Contains(config, stamp) {
		t.Errorf(".goreleaser.yaml does not stamp the version: want ldflags containing %q", stamp)
	}
	if strings.Contains(strings.ToLower(config), "windows") {
		t.Error(".goreleaser.yaml mentions windows — takt ships linux and darwin only")
	}
}
