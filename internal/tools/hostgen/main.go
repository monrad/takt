// Command hostgen writes the Copilot CLI host's files from the Claude Code
// ones: hosts/copilot/agents/*.agent.md from agents/*.md, and
// hosts/copilot/skills/takt/SKILL.md from commands/takt.md and the version
// in .claude-plugin/plugin.json. With --check it writes nothing and exits 1
// listing the files that are stale, which is what `task hosts:check` and the
// prompt parity tests enforce.
//
// "Stale" covers both directions. A generated file whose content no longer
// matches its source is stale, and so is one whose source is gone: renaming
// or deleting an agent left the old .agent.md behind, where the host went on
// loading it — a definition nothing in the repository produces any more, and
// one no content comparison could ever notice.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monrad/takt/internal/hosts"
)

// exitFailure is hostgen's own error code. Exit 1 is reserved for --check
// finding a stale file, so a run that broke cannot be read as a clean
// "everything is up to date" or as a mere staleness report.
const exitFailure = 2

func main() {
	check := flag.Bool("check", false, "report stale files instead of writing them")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	os.Exit(run(*root, *check, os.Stdout, os.Stderr))
}

// run renders every agents/*.md under root, sweeps the generated files no
// source claims any more, renders the skill from commands/takt.md, and
// returns the process exit code.
//
// The two streams are parameters rather than [os.Stdout] and [os.Stderr]
// because what a failure prints is part of hostgen's contract: a render
// error names the source path this run actually read, resolved under root,
// and the only way to pin that is to read back what the run wrote.
func run(root string, check bool, stdout, stderr io.Writer) int {
	srcs, err := filepath.Glob(filepath.Join(root, "agents", "*.md"))
	if err != nil || len(srcs) == 0 {
		fmt.Fprintln(stderr, "hostgen: no agents/*.md under", root)
		return exitFailure
	}
	dstDir := filepath.Join(root, "hosts", "copilot", "agents")
	generated := make(map[string]bool, len(srcs))
	stale := 0
	for _, src := range srcs {
		name := strings.TrimSuffix(filepath.Base(src), ".md")
		out, rerr := render(src, name)
		if rerr != nil {
			fmt.Fprintln(stderr, "hostgen:", rerr)
			return exitFailure
		}
		dst := filepath.Join(dstDir, hosts.CopilotAgentName(name)+".agent.md")
		generated[dst] = true
		if cur, _ := os.ReadFile(dst); bytes.Equal(cur, out) {
			continue
		}
		if check {
			fmt.Fprintln(stderr, "stale:", dst)
			stale++
			continue
		}
		if werr := write(dst, out); werr != nil {
			fmt.Fprintln(stderr, "hostgen:", werr)
			return exitFailure
		}
		fmt.Fprintln(stdout, "wrote", dst)
	}
	orphaned, err := sweepOrphans(dstDir, generated, check, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "hostgen:", err)
		return exitFailure
	}
	skillStale, err := generateSkill(root, check, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "hostgen:", err)
		return exitFailure
	}
	if stale+orphaned+skillStale > 0 {
		return 1
	}
	return 0
}

// generateSkill renders hosts/copilot/skills/takt/SKILL.md from
// commands/takt.md under root and returns how many files --check found stale
// (0 or 1). Its inputs are two files a repository either has or is not one:
// a missing prompt or manifest is an error naming the path, never a silent
// skip, because a skip is indistinguishable from "the skill is up to date"
// and would let the file this generator owns drift after all.
func generateSkill(root string, check bool, stdout, stderr io.Writer) (int, error) {
	src := filepath.Join(root, "commands", "takt.md")
	in, err := os.ReadFile(src)
	if err != nil {
		return 0, err
	}
	version, err := manifestVersion(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return 0, err
	}
	out, err := hosts.RenderCopilotSkill(src, in, version)
	if err != nil {
		return 0, err
	}
	dst := filepath.Join(root, "hosts", "copilot", "skills", "takt", "SKILL.md")
	if cur, _ := os.ReadFile(dst); bytes.Equal(cur, out) {
		return 0, nil
	}
	if check {
		fmt.Fprintln(stderr, "stale:", dst)
		return 1, nil
	}
	if werr := write(dst, out); werr != nil {
		return 0, werr
	}
	fmt.Fprintln(stdout, "wrote", dst)
	return 0, nil
}

// manifestVersion reads the "version" field of the plugin manifest at path —
// the one the Copilot handshake pins, since that host has no plugin root to
// read the manifest from at run time. Every failure names path: this is the
// second file a --root must have, and "which root did you point me at" is
// the only question its absence raises.
func manifestVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var m struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if m.Version == "" {
		return "", fmt.Errorf("%s: no \"version\" field", path)
	}
	return m.Version, nil
}

// sweepOrphans reports (--check) or deletes every *.agent.md in dstDir that
// this run did not generate, and returns how many --check found. Deleting is
// safe because the directory holds nothing but generated files: the whole of
// it is rewritten from agents/*.md on every gen, so anything the sweep does
// not recognise came from a source that has since been renamed or removed.
func sweepOrphans(dstDir string, generated map[string]bool, check bool, stdout, stderr io.Writer) (int, error) {
	dsts, err := filepath.Glob(filepath.Join(dstDir, "*.agent.md"))
	if err != nil {
		return 0, err
	}
	orphaned := 0
	for _, dst := range dsts {
		if generated[dst] {
			continue
		}
		if check {
			fmt.Fprintln(stderr, "orphaned:", dst)
			orphaned++
			continue
		}
		if rerr := os.Remove(dst); rerr != nil {
			return 0, rerr
		}
		fmt.Fprintln(stdout, "removed", dst)
	}
	return orphaned, nil
}

// render reads one Claude Code agent definition and returns the Copilot file
// it generates into.
func render(src, name string) ([]byte, error) {
	in, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	return hosts.RenderCopilotAgent(src, name, in)
}

// write puts one generated file at dst, creating the host's agents directory
// the first time.
func write(dst string, out []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	//nolint:gosec // G703: dst is --root joined with names this program builds — an agent's own file name, or
	// the skill's fixed relative path; no caller-supplied value ever reaches this write.
	return os.WriteFile(dst, out, 0o600)
}
