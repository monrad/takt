package prompt_test

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

const (
	pluginManifestPath      = "../../.claude-plugin/plugin.json"
	marketplaceManifestPath = "../../.claude-plugin/marketplace.json"
)

// semverPattern is the x.y.z shape both manifests' version fields must take
// (task-4 brief interface).
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

type pluginManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type marketplaceManifest struct {
	Version string `json:"version"`
	Plugins []struct {
		Version string `json:"version"`
	} `json:"plugins"`
}

// TestPluginManifestsAgreeOnVersion pins the task-4 brief's manifest_test.go
// interface: both files parse, plugin.json's name is takt, its version is
// semver, and it agrees with marketplace.json's plugins[0].version.
func TestPluginManifestsAgreeOnVersion(t *testing.T) {
	t.Parallel()
	pb, err := os.ReadFile(pluginManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var p pluginManifest
	if err = json.Unmarshal(pb, &p); err != nil {
		t.Fatalf("plugin.json does not parse: %v", err)
	}
	mb, err := os.ReadFile(marketplaceManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var m marketplaceManifest
	if err = json.Unmarshal(mb, &m); err != nil {
		t.Fatalf("marketplace.json does not parse: %v", err)
	}

	if p.Name != "takt" {
		t.Errorf("plugin.json name = %q, want takt", p.Name)
	}
	if !semverPattern.MatchString(p.Version) {
		t.Errorf("plugin.json version %q is not semver x.y.z", p.Version)
	}
	if len(m.Plugins) == 0 {
		t.Fatal("marketplace.json has no plugins")
	}
	if p.Version != m.Plugins[0].Version {
		t.Errorf("plugin.json version %q != marketplace.json plugins[0].version %q",
			p.Version, m.Plugins[0].Version)
	}
}
