// Command hostgen writes hosts/copilot/agents/*.agent.md from agents/*.md.
// With --check it writes nothing and exits 1 listing the files that are
// stale, which is what `task hosts:check` and the prompt parity test
// enforce.
//
// "Stale" covers both directions. A generated file whose content no longer
// matches its source is stale, and so is one whose source is gone: renaming
// or deleting an agent left the old .agent.md behind, where the host went on
// loading it — a definition nothing in the repository produces any more, and
// one no content comparison could ever notice.
package main

import (
	"bytes"
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
// source claims any more, and returns the process exit code.
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
	if stale+orphaned > 0 {
		return 1
	}
	return 0
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
	return os.WriteFile(dst, out, 0o600)
}
