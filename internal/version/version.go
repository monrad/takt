// Package version holds the build-time version string.
package version

// Dev is the unstamped default: a local `go build`/`go test` binary that
// was not built with -ldflags. `takt version --expect-manifest` treats it
// as matching any plugin manifest (spec §6).
const Dev = "0.0.0-dev"

// Version is stamped at build time with
// -ldflags "-X github.com/monrad/takt/internal/version.Version=<tag>".
var Version = Dev
