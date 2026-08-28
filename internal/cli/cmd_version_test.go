package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/version"
)

// TestVersionExpectManifest is the task-4 brief's handshake test: the test
// binary is always the unstamped 0.0.0-dev build, so a manifest that names
// any version is accepted with dev:true, and an unreadable manifest fails
// with a hint (spec §6).
func TestVersionExpectManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"takt","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	// --expect-manifest's failures are the plugin's, never the skill's
	// (issue #11) — the test binary can only reach this unreadable-manifest
	// branch, not the version-mismatch one TestManifestFailure covers
	// directly, but every failure this flag can produce still names the
	// plugin.
	if !strings.Contains(errb, "plugin") || strings.Contains(errb, "skill") {
		t.Errorf("--expect-manifest failure must name the plugin, not the skill: %s", errb)
	}
}

// TestVersionExpectAcceptsADevBuild pins the dev exception on `--expect`,
// the flag the Copilot CLI skill's handshake runs: `task build` is a plain
// `go build` with no ldflags, so every developer binary — and a `go install
// ./cmd/takt` — reports 0.0.0-dev, and a strict comparison would fail the
// handshake on all of them. It is the same judgment `--expect-manifest`
// makes, so the reply carries `"dev": true` the same way (spec §6).
//
// A stamped binary's mismatch cannot be reached through the command — the
// test binary is always 0.0.0-dev — so TestManifestFailure covers that
// branch of the judgment directly.
func TestVersionExpectAcceptsADevBuild(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, got, errb := runIn(t, dir, nil, "version", "--expect", "0.1.0")
	if code != 0 || got["dev"] != true || got["version"] != version.Dev {
		t.Fatalf("exit %d, got %v, stderr %s", code, got, errb)
	}
	if _, ok := got["manifest"]; ok {
		t.Errorf("--expect names no manifest, so the reply must not report one: %v", got)
	}
}

// TestVersionExpectEmptyFailsTheHandshake pins issue #3: `takt version
// --expect ""` gives the flag a literal-empty value — the shape a host
// prompt takes when its own version-stamp line came out empty — and that
// must fail the handshake exactly like the whitespace-only case already
// does, not fall through to the plain `takt version` print. Dispatching on
// `*expect != ""` could not tell "the flag was never passed" apart from
// "the flag was passed an empty string"; detecting that --expect was given
// at all is what fixes it. Both shapes are checked here so a future
// regression that treats them differently is caught. The other two paths
// that share cmdVersion's flag set — the mutual-exclusion check and the
// plain-print default — are re-checked alongside them, since both sit on
// the same dispatch this fix rewrites.
func TestVersionExpectEmptyFailsTheHandshake(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, want := range []string{"", "   "} {
		code, _, errb := runIn(t, dir, nil, "version", "--expect", want)
		if code == 0 {
			t.Fatalf("--expect %q: exit 0, want the versionless refusal", want)
		}
		if !strings.Contains(errb, "the host's handshake names no version") {
			t.Errorf("--expect %q: error does not name the versionless refusal: %s", want, errb)
		}
		if !strings.Contains(errb, "check the host prompt's takt version --expect line") {
			t.Errorf("--expect %q: hint does not point back at the handshake line: %s", want, errb)
		}
	}

	// The mutual-exclusion check still fires on two real, non-empty values.
	manifest := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"takt","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := runIn(t, dir, nil, "version", "--expect", "0.1.0", "--expect-manifest", manifest); code != 2 {
		t.Fatalf("mutual exclusion: exit %d, want 2: %s", code, errb)
	}

	// The plain-print path — no --expect at all — is unaffected.
	if code, got, errb := runIn(t, dir, nil, "version"); code != 0 || got["version"] != version.Current() {
		t.Fatalf("plain print: exit %d, got %v, stderr %s", code, got, errb)
	}
}

// TestVersionExpectAndManifestAreMutuallyExclusive pins the one combination
// that has no answer: --expect names the version inline, --expect-manifest
// names a file to read it from, and a caller passing both means two
// different things at once. Answering only the first would let a stale
// manifest path sit unread in a release script forever, so it is a usage
// error like any other bad flag combination.
func TestVersionExpectAndManifestAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"takt","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errb := runIn(t, dir, nil, "version", "--expect", "0.1.0", "--expect-manifest", manifest)
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errb)
	}
	if !strings.Contains(errb, "--expect and --expect-manifest are mutually exclusive") {
		t.Errorf("error does not name the conflict: %s", errb)
	}
	if !strings.Contains(errb, "hint") {
		t.Errorf("no hint: %s", errb)
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
// a mismatch fails only for the stamped one, and the mismatch text names the
// subject the caller passes — "skill" for --expect's handshake, "plugin" for
// --expect-manifest's (issue #11) — while the missing-version text always
// names the manifest path regardless of subject. It also pins dev: false on
// every failure, and dev: true on the one case that passes with an unstamped
// binary, now returned alongside the {error, hint} pair instead of requiring
// a second call to [cli.ManifestMatches] (issue #10).
func TestManifestFailure(t *testing.T) {
	t.Parallel()
	const path = "/p/.claude-plugin/plugin.json"
	for _, c := range []struct {
		binary, manifest, subject, wantErr, wantHintContains string
		wantDev                                              bool
	}{
		{"0.0.0-dev", "", "plugin", "plugin manifest " + path + " has no version field", "", false},
		{"1.2.3", "", "plugin", "plugin manifest " + path + " has no version field", "", false},
		{"1.2.3", "  ", "plugin", "plugin manifest " + path + " has no version field", "", false},
		{"1.2.3", "1.2.4", "plugin", "takt version 1.2.3 does not match plugin version 1.2.4", "update the plugin", false},
		{"1.2.3", "1.2.4", "skill", "takt version 1.2.3 does not match skill version 1.2.4", "update the skill", false},
		{"1.2.3", "1.2.3", "plugin", "", "", false},
		{"0.0.0-dev", "1.2.4", "plugin", "", "", true},
	} {
		gotErr, gotHint, gotDev := cli.ManifestFailure(c.binary, path, c.manifest, c.subject)
		if gotErr != c.wantErr {
			t.Errorf("ManifestFailure(%q, %q, %q) error = %q, want %q",
				c.binary, c.manifest, c.subject, gotErr, c.wantErr)
		}
		if (gotHint == "") != (c.wantErr == "") {
			t.Errorf("ManifestFailure(%q, %q, %q) hint = %q with error %q — a failure must carry a hint",
				c.binary, c.manifest, c.subject, gotHint, gotErr)
		}
		if c.wantHintContains != "" && !strings.Contains(gotHint, c.wantHintContains) {
			t.Errorf("ManifestFailure(%q, %q, %q) hint %q does not name the subject: want %q",
				c.binary, c.manifest, c.subject, gotHint, c.wantHintContains)
		}
		if gotDev != c.wantDev {
			t.Errorf("ManifestFailure(%q, %q, %q) dev = %v, want %v",
				c.binary, c.manifest, c.subject, gotDev, c.wantDev)
		}
	}
}
