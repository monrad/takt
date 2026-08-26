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

// TestVersionExpectManifestNoVersion covers a plugin manifest that declares
// no version at all — absent, empty, or blank. That is a broken bundle
// rather than a version mismatch, and it has to be reported as one before
// the comparison: a stamped binary reported "does not match plugin version "
// with nothing after it, and a dev binary — which matches any manifest —
// accepted it outright and let the loop run against a bundle whose version
// nothing knows (task-4 deferred minor).
func TestVersionExpectManifestNoVersion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, body string }{
		{"absent", `{"name":"takt"}`},
		{"empty", `{"name":"takt","version":""}`},
		{"blank", `{"name":"takt","version":"  "}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			manifest := filepath.Join(dir, "plugin.json")
			if err := os.WriteFile(manifest, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errb := runIn(t, dir, nil, "version", "--expect-manifest", manifest)
			if code != 1 {
				t.Fatalf("exit %d, want 1: %s", code, errb)
			}
			if !strings.Contains(errb, "plugin manifest "+manifest+" has no version field") {
				t.Errorf("error does not name the empty version field: %s", errb)
			}
			if !strings.Contains(errb, "hint") {
				t.Errorf("no hint: %s", errb)
			}
		})
	}
}

// TestManifestFailure drives the handshake's whole judgment, which the
// command-level test can only half reach: the test binary is always
// 0.0.0-dev, so a stamped build's branches are unreachable through
// `takt version`. A missing manifest version fails for both kinds of build;
// a mismatch fails only for the stamped one.
func TestManifestFailure(t *testing.T) {
	t.Parallel()
	const path = "/p/.claude-plugin/plugin.json"
	for _, c := range []struct{ binary, manifest, wantErr string }{
		{"0.0.0-dev", "", "plugin manifest " + path + " has no version field"},
		{"1.2.3", "", "plugin manifest " + path + " has no version field"},
		{"1.2.3", "  ", "plugin manifest " + path + " has no version field"},
		{"1.2.3", "1.2.4", "takt version 1.2.3 does not match plugin version 1.2.4"},
		{"1.2.3", "1.2.3", ""},
		{"0.0.0-dev", "1.2.4", ""},
	} {
		gotErr, gotHint := cli.ManifestFailure(c.binary, path, c.manifest)
		if gotErr != c.wantErr {
			t.Errorf("ManifestFailure(%q, %q) error = %q, want %q", c.binary, c.manifest, gotErr, c.wantErr)
		}
		if (gotHint == "") != (c.wantErr == "") {
			t.Errorf("ManifestFailure(%q, %q) hint = %q with error %q — a failure must carry a hint",
				c.binary, c.manifest, gotHint, gotErr)
		}
	}
}
