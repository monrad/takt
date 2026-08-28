package cli

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/monrad/takt/internal/version"
)

func cmdVersion(env Env) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	expect := fs.String("expect", "", "fail unless the version equals this value")
	expectManifest := fs.String("expect-manifest", "",
		"fail unless the plugin manifest at this path names a compatible version")
	if err := fs.Parse(env.Args); err != nil {
		return usageError(env, fs, err)
	}
	// A literal-empty --expect ("takt version --expect \"\"") means the flag
	// was given but the host's handshake stamped nothing into it — that must
	// still fail (issue #3), unlike an absent --expect, which prints the
	// plain version. *expect != "" cannot tell those apart, so detect that
	// the flag was actually passed instead of inspecting its value.
	var expectGiven bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expect" {
			expectGiven = true
		}
	})
	if expectGiven && *expectManifest != "" {
		return fail(env.Stderr, exitUsage, "--expect and --expect-manifest are mutually exclusive",
			"pass the version inline with --expect, or the manifest to read it from with --expect-manifest")
	}
	if expectGiven {
		return versionExpect(env, *expect)
	}
	if *expectManifest != "" {
		return versionExpectManifest(env, *expectManifest)
	}
	if err := writeJSON(env.Stdout, map[string]string{keyVersion: version.Current()}); err != nil {
		return 1
	}
	return 0
}

// versionExpect implements `takt version --expect <version>` (spec §6): the
// handshake of a host that carries the version in its own prompt instead of
// reading a manifest — the Copilot CLI skill, whose line `task version:set`
// stamps, since that host has no plugin root to point at. The judgment is
// the manifest one, dev exception included: `task build` is a plain `go
// build` with no ldflags, so every development binary reports [version.Dev]
// and must pass the handshake rather than fail it.
//
// A mismatch reads "does not match skill version <v>" with a hint to
// "update the skill", not "plugin" — this flag is the skill's handshake and
// there is no plugin here to name (issue #11); manifestFailure's subject
// argument below carries that noun.
func versionExpect(env Env, want string) int {
	// The one judgment that is not the manifest one: an expectation with no
	// version in it. manifestFailure would call it a manifest with no
	// version field and send the reader off to reinstall a plugin bundle
	// this flag never reads — `--expect` is for the host that carries the
	// version in its own prompt, so the failure has to point back at that
	// line.
	if strings.TrimSpace(want) == "" {
		return fail(env.Stderr, exitError, "the host's handshake names no version",
			"check the host prompt's takt version --expect line")
	}
	problem, hint, dev := manifestFailure(version.Current(), "--expect", want, "skill")
	if problem != "" {
		return fail(env.Stderr, exitError, problem, hint)
	}
	out := map[string]any{keyVersion: version.Current()}
	if dev {
		out["dev"] = true
	}
	if err := writeJSON(env.Stdout, out); err != nil {
		return exitError
	}
	return 0
}

// manifestVersion is the one field takt reads out of the plugin manifest
// for the handshake below.
type manifestVersion struct {
	Version string `json:"version"`
}

// versionExpectManifest implements `takt version --expect-manifest <path>`
// (spec §6): the handshake commands/takt.md runs before anything else, so a
// binary and a plugin bundle that have drifted apart fail loudly up front
// instead of running an op protocol either side has since changed.
func versionExpectManifest(env Env, path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return fail(env.Stderr, 1, "cannot read plugin manifest "+path+": "+err.Error(),
			"check CLAUDE_PLUGIN_ROOT and the plugin installation")
	}
	var m manifestVersion
	if err = json.Unmarshal(b, &m); err != nil {
		return fail(env.Stderr, 1, "cannot parse plugin manifest "+path+": "+err.Error(),
			"check CLAUDE_PLUGIN_ROOT and the plugin installation")
	}
	problem, hint, dev := manifestFailure(version.Current(), path, m.Version, "plugin")
	if problem != "" {
		return fail(env.Stderr, exitError, problem, hint)
	}
	out := map[string]any{keyVersion: version.Current(), "manifest": m.Version}
	if dev {
		out["dev"] = true
	}
	if err = writeJSON(env.Stdout, out); err != nil {
		return 1
	}
	return 0
}

// manifestFailure judges a plugin manifest's declared version against the
// running binary's, once, and returns the {error, hint} pair to fail with —
// or two empty strings when the handshake passes — together with dev, the
// same dev ManifestMatches computed while judging, so neither caller has to
// call ManifestMatches a second time just to learn it.
//
// subject is the noun the mismatch failure names: "skill" for the --expect
// handshake the Copilot CLI skill runs, "plugin" for --expect-manifest's
// plugin-manifest handshake. The empty-version failure below it always names
// the manifest path instead, regardless of subject.
//
// The empty-version check comes first and applies to every build. A manifest
// with no version is a broken bundle, not a version disagreement: a stamped
// binary reported it as "does not match plugin version " with nothing after
// it, and an unstamped one matched it like any other manifest and let the
// loop start against a bundle whose version nothing knows.
func manifestFailure(binary, path, manifest, subject string) (string, string, bool) {
	if strings.TrimSpace(manifest) == "" {
		return "plugin manifest " + path + " has no version field",
			"reinstall the takt plugin, or set the version in a checkout with `task version:set <x.y.z>`", false
	}
	ok, dev := ManifestMatches(binary, manifest)
	if !ok {
		return "takt version " + binary + " does not match " + subject + " version " + manifest,
			"install takt " + manifest + " (nix/brew/go install) or update the " + subject, false
	}
	return "", "", dev
}

// ManifestMatches reports whether binary — takt's own build version —
// satisfies manifest — the plugin manifest's declared version (spec §6):
// equal versions match; an unstamped [version.Dev] binary (a local build)
// matches any manifest, in dev mode.
//
//nolint:nonamedreturns // ok/dev are two same-typed bools the brief names explicitly, for a self-documenting call site
func ManifestMatches(binary, manifest string) (ok, dev bool) {
	if binary == version.Dev {
		return true, true
	}
	return binary == manifest, false
}
