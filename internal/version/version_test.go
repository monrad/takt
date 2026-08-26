package version_test

import (
	"runtime/debug"
	"testing"

	"github.com/monrad/takt/internal/version"
)

// buildInfo returns a [debug.ReadBuildInfo] stand-in reporting mainVersion.
func buildInfo(mainVersion string, ok bool) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		if !ok {
			return nil, false
		}
		return &debug.BuildInfo{Main: debug.Module{
			Path:    "github.com/monrad/takt",
			Version: mainVersion,
		}}, true
	}
}

func TestResolvePrefersTheLinkerStamp(t *testing.T) {
	t.Parallel()
	// A stamped binary reports its stamp even where the build info
	// disagrees: goreleaser and nix both stamp, and their value is
	// authoritative.
	if got := version.Resolve("0.1.0", buildInfo("v9.9.9", true)); got != "0.1.0" {
		t.Errorf("Resolve = %q, want the stamp 0.1.0", got)
	}
}

func TestResolveFallsBackToTheModuleVersion(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		mainVersion string
		ok          bool
		want        string
	}{
		// `go install …/cmd/takt@v0.1.0` — the case this fallback exists for.
		"release version":     {mainVersion: "v0.1.0", ok: true, want: "0.1.0"},
		"multi digit release": {mainVersion: "v10.20.30", ok: true, want: "10.20.30"},

		// Everything below must keep the Dev sentinel.
		"go test binary": {mainVersion: "(devel)", ok: true, want: version.Dev},
		"no build info":  {mainVersion: "", ok: false, want: version.Dev},
		"empty version":  {mainVersion: "", ok: true, want: version.Dev},

		// A plain `go build` in a git checkout: Go stamps a VCS
		// pseudo-version, which must NOT be mistaken for a release.
		"pseudo version": {
			mainVersion: "v0.0.0-20260826025802-39a25f9311d0", ok: true, want: version.Dev,
		},
		"dirty pseudo version": {
			mainVersion: "v0.0.0-20260826025802-39a25f9311d0+dirty", ok: true, want: version.Dev,
		},

		// takt ships stable tags only (see .goreleaser.yaml).
		"prerelease":  {mainVersion: "v0.2.0-rc1", ok: true, want: version.Dev},
		"build meta":  {mainVersion: "v0.1.0+dirty", ok: true, want: version.Dev},
		"no v prefix": {mainVersion: "0.1.0", ok: true, want: version.Dev},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := version.Resolve(version.Dev, buildInfo(tc.mainVersion, tc.ok))
			if got != tc.want {
				t.Errorf("Resolve(Dev, %q) = %q, want %q", tc.mainVersion, got, tc.want)
			}
		})
	}
}

// TestCurrentUnderGoTest pins the sentinel for the build every developer
// runs: the test binary itself reports "(devel)", so Current is Dev.
func TestCurrentUnderGoTest(t *testing.T) {
	t.Parallel()
	if got := version.Current(); got != version.Dev {
		t.Errorf("Current() = %q under `go test`, want %q", got, version.Dev)
	}
}
