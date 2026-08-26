package version

// Resolve exposes resolve to the external test package, so the build-info
// fallback can be driven with a stand-in reader.
var Resolve = resolve
