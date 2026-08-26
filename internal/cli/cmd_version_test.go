package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/cli"
)

// TestVersionExpectManifest is the task-4 brief's handshake test: the test
// binary is always the unstamped 0.0.0-dev build, so a manifest that names
// any version is accepted with dev:true, and an unreadable manifest fails
// with a hint (spec §6).
func TestVersionExpectManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "plugin.json")
	os.WriteFile(manifest, []byte(`{"name":"takt","version":"0.1.0"}`), 0o600)
	// the test binary is 0.0.0-dev: accepted with dev:true
	code, got, _ := runIn(t, dir, nil, "version", "--expect-manifest", manifest)
	if code != 0 || got["dev"] != true || got["manifest"] != "0.1.0" {
		t.Fatalf("%d %v", code, got)
	}
	missing := filepath.Join(dir, "missing.json")
	missCode, _, errb := runIn(t, dir, nil, "version", "--expect-manifest", missing)
	if missCode != 1 || !strings.Contains(errb, "hint") {
		t.Fatalf("%d %s", missCode, errb)
	}
}

// TestManifestMatches is a unit test on the exported helper the handshake
// runs on: equal versions match; a mismatch does not; an unstamped
// 0.0.0-dev binary matches anything, in dev mode.
func TestManifestMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		binary, manifest string
		ok, dev          bool
	}{
		{"1.2.3", "1.2.3", true, false},
		{"1.2.3", "1.2.4", false, false},
		{"0.0.0-dev", "1.2.4", true, true},
	}
	for _, c := range cases {
		ok, dev := cli.ManifestMatches(c.binary, c.manifest)
		if ok != c.ok || dev != c.dev {
			t.Errorf("ManifestMatches(%q, %q) = (%v, %v), want (%v, %v)",
				c.binary, c.manifest, ok, dev, c.ok, c.dev)
		}
	}
}
