// Package version holds the build-time version string.
package version

// Version is stamped at build time with
// -ldflags "-X github.com/monrad/takt/internal/version.Version=<tag>".
var Version = "0.0.0-dev"
