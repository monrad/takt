// Package version reports the version of the running takt binary.
package version

import (
	"regexp"
	"runtime/debug"
)

// Dev is the unstamped default: a binary carrying neither an -ldflags stamp
// nor a module release version, which is what a local `go build` or
// `go test` produces. `takt version --expect-manifest` treats it as matching
// any plugin manifest (spec §6).
const Dev = "0.0.0-dev"

// Version is the -ldflags target:
// -ldflags "-X github.com/monrad/takt/internal/version.Version=<x.y.z>".
//
// Read [Current] instead. A `go install …@v0.1.0` binary carries no stamp
// and would read Dev here, though it does know its own version.
var Version = Dev

// releaseVersion matches a module release version, capturing it without the
// leading v so it reads like every other version takt handles. Deliberately
// x.y.z and nothing else: that is the shape internal/tools/setversion,
// manifest_test.go and release.yml's tag gate already agree on, and it is
// what excludes the pseudo-versions described in [resolve].
var releaseVersion = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

// current is resolved once, at package initialisation, from the value the
// linker left in Version.
var current = resolve(Version, debug.ReadBuildInfo)

// Current returns the version this binary should report. Four kinds of build
// reach it by three routes:
//
//   - .goreleaser.yaml stamps Version with the git tag, and release.yml
//     refuses a tag that disagrees with the plugin manifests;
//   - flake.nix stamps Version with the version it reads straight out of
//     .claude-plugin/plugin.json;
//   - `go install github.com/monrad/takt/cmd/takt@v0.1.0` stamps nothing, so
//     the module release version is recovered from the build info;
//   - a local `go build` or `go test` has neither, and reports [Dev].
//
// `task version:set` is the only thing that rewrites the manifests the first
// two routes read.
func Current() string { return current }

// resolve prefers the linker stamp, falls back to a module release version
// from the build info, and otherwise reports [Dev].
//
// The build-info branch accepts only a release version rather than anything
// that merely is not "(devel)". A plain `go build` inside a git checkout
// stamps Main.Version with a VCS pseudo-version — measured here on Go 1.26 as
// "v0.0.0-20260826025802-39a25f9311d0+dirty" — so the looser test would take
// the Dev sentinel away from ordinary local builds and, with it, the
// `--expect-manifest` escape hatch that lets a developer's binary run against
// any plugin bundle. Only `go test` binaries still report "(devel)".
func resolve(stamped string, read func() (*debug.BuildInfo, bool)) string {
	if stamped != Dev {
		return stamped
	}
	info, ok := read()
	if !ok {
		return Dev
	}
	if m := releaseVersion.FindStringSubmatch(info.Main.Version); m != nil {
		return m[1]
	}
	return Dev
}
