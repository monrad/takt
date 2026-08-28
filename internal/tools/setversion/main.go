// Command setversion rewrites takt's declared version — the "version"
// field(s) in the plugin manifests (.claude-plugin/plugin.json,
// .claude-plugin/marketplace.json) and the handshake line of the Copilot CLI
// skill, which has no manifest to read — to a new semver: `task version:set
// VERSION=x.y.z` (task-4 brief, spec §6.1). It edits the files as text: a
// regexp substitution on each `"version": "…"` line and on the skill's
// `takt version --expect <x.y.z>`, so everything else in the file — key
// order, indentation, the rest of the content — is untouched and the diff
// stays a single-line change per file.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// pluginManifest is the manifest --print reads: the one file every build
// (task build's -ldflags, the flake, goreleaser) treats as the source of
// truth for takt's declared version.
var pluginManifest = filepath.Join(".claude-plugin", "plugin.json")

// manifests is the two files setversion's rewrite mode touches, relative to
// the directory Run is pointed at (the repository root — the Taskfile
// always runs `go run` from there).
var manifests = []string{
	pluginManifest,
	filepath.Join(".claude-plugin", "marketplace.json"),
}

// skill is the Copilot CLI host's skill, rewritten alongside the manifests:
// that host has no plugin root to read a manifest from, so its handshake
// carries the version as text (spec §6.1).
var skill = filepath.Join("hosts", "copilot", "skills", "takt", "SKILL.md")

// semverPattern is the x.y.z shape `manifest_test.go` expects and the only
// form setversion accepts.
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// versionLine matches one `"version": "…"` line's quoted value, so the
// rewrite is a text substitution rather than a JSON round-trip.
var versionLine = regexp.MustCompile(`("version":\s*")[^"]*(")`)

// expectVersion captures the version in a skill's handshake command. It is
// anchored on the x.y.z shape rather than on "the rest of the token": the
// version is written inside a code span and followed by a full stop, so a
// \S+ would swallow the closing backtick and the punctuation with it.
var expectVersion = regexp.MustCompile(`takt version --expect (\d+\.\d+\.\d+)`)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr, dir))
}

// Run is setversion's entry point, exported for its test: args is the
// program's arguments — either exactly one semver string (the rewrite mode)
// or the single flag --print (the read-only mode `task build` runs to learn
// the version to stamp into -ldflags) — and dir is the directory the
// manifests are resolved relative to. It returns the process exit code.
func Run(args []string, stdout, stderr io.Writer, dir string) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: setversion (<x.y.z>|--print)")
		return 1
	}
	if args[0] == "--print" {
		return runPrint(stdout, stderr, dir)
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
	if err := rewriteExpect(filepath.Join(dir, skill), v); err != nil {
		fmt.Fprintln(stderr, "setversion:", err)
		return 1
	}
	return 0
}

// runPrint implements --print: it reads pluginManifest with the same
// versionLine regexp rewriteVersion writes with, prints the version it finds
// to stdout, and writes nothing — `task build` shells this out into a
// Taskfile var rather than depending on a JSON tool the repo does not
// otherwise need.
func runPrint(stdout, stderr io.Writer, dir string) int {
	v, err := readVersion(filepath.Join(dir, pluginManifest))
	if err != nil {
		fmt.Fprintln(stderr, "setversion:", err)
		return 1
	}
	fmt.Fprintln(stdout, v)
	return 0
}

// readVersion extracts the quoted value of path's first `"version": "…"`
// line using versionLine — the same regexp rewriteVersion substitutes
// through — so --print and the rewrite mode stay one parser reading and
// writing the same shape.
func readVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := versionLine.FindSubmatchIndex(b)
	if m == nil {
		return "", fmt.Errorf("%s: no \"version\" field found", path)
	}
	return string(b[m[3]:m[4]]), nil
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

// rewriteExpect replaces the version in the first `takt version --expect
// <x.y.z>` of path with v, refusing a file that has no such line: the
// handshake is load-bearing — a skill that lost it would run against any
// binary — so a silent no-op there is worse than a failed release step.
func rewriteExpect(path, v string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m := expectVersion.FindSubmatchIndex(b)
	if m == nil {
		return fmt.Errorf("%s: no `takt version --expect <x.y.z>` handshake line found", path)
	}
	out := make([]byte, 0, len(b))
	out = append(out, b[:m[2]]...)
	out = append(out, v...)
	out = append(out, b[m[3]:]...)
	//nolint:gosec // G703: path is dir (the Taskfile's cwd, or a test's t.TempDir()) joined with the fixed
	// relative name in skill, or the test's own temporary file; no caller-supplied value ever reaches this write.
	return os.WriteFile(path, out, 0o600)
}
