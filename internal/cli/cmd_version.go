package cli

import (
	"encoding/json"
	"flag"
	"io"
	"os"

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
	if *expect != "" && *expect != version.Current() {
		return fail(env.Stderr, 1,
			"takt version "+version.Current()+" does not match expected "+*expect,
			"install the takt binary matching the plugin version")
	}
	if *expectManifest != "" {
		return versionExpectManifest(env, *expectManifest)
	}
	if err := writeJSON(env.Stdout, map[string]string{keyVersion: version.Current()}); err != nil {
		return 1
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
	ok, dev := ManifestMatches(version.Current(), m.Version)
	if !ok {
		return fail(env.Stderr, 1,
			"takt version "+version.Current()+" does not match plugin version "+m.Version,
			"install takt "+m.Version+" (nix/brew/go install) or update the plugin")
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
