// Command setversion rewrites the "version" field(s) in the takt plugin
// manifests (.claude-plugin/plugin.json, .claude-plugin/marketplace.json) to
// a new semver — `task version:set VERSION=x.y.z` (task-4 brief). It edits
// the files as text: a regexp substitution on each `"version": "…"` line,
// so everything else in the file — key order, indentation, the rest of the
// content — is untouched and the diff stays a single-line change per file.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// manifests is the two files setversion rewrites, relative to the directory
// Run is pointed at (the repository root — the Taskfile always runs `go
// run` from there).
var manifests = []string{
	filepath.Join(".claude-plugin", "plugin.json"),
	filepath.Join(".claude-plugin", "marketplace.json"),
}

// semverPattern is the x.y.z shape `manifest_test.go` expects and the only
// form setversion accepts.
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// versionLine matches one `"version": "…"` line's quoted value, so the
// rewrite is a text substitution rather than a JSON round-trip.
var versionLine = regexp.MustCompile(`("version":\s*")[^"]*(")`)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(Run(os.Args[1:], os.Stderr, dir))
}

// Run is setversion's entry point, exported for its test: args is the
// program's arguments (exactly one semver string), dir is the directory the
// two manifests are resolved relative to. It returns the process exit code.
func Run(args []string, stderr io.Writer, dir string) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: setversion <x.y.z>")
		return 1
	}
	v := args[0]
	if !semverPattern.MatchString(v) {
		fmt.Fprintf(stderr, "setversion: %q is not a semver x.y.z\n", v)
		return 1
	}
	for _, rel := range manifests {
		if err := rewriteVersion(filepath.Join(dir, rel), v); err != nil {
			fmt.Fprintln(stderr, "setversion:", err)
			return 1
		}
	}
	return 0
}

// rewriteVersion replaces every `"version": "…"` line's value in path with
// v, refusing to silently no-op on a file that has no such line.
func rewriteVersion(path, v string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !versionLine.Match(b) {
		return fmt.Errorf("%s: no \"version\" field found", path)
	}
	out := versionLine.ReplaceAll(b, []byte(`${1}`+v+`${2}`))
	//nolint:gosec // G703: path is dir (the Taskfile's cwd, or a test's t.TempDir()) joined with one of the
	// two fixed relative names in manifests; no caller-supplied value ever reaches this write.
	return os.WriteFile(path, out, 0o600)
}
