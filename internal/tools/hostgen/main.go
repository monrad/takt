// Command hostgen writes hosts/copilot/agents/*.agent.md from agents/*.md.
// With --check it writes nothing and exits 1 listing the files that are
// stale, which is what `task hosts:check` and the prompt parity test
// enforce.
package main

import (
	"bytes"
	"flag"
	"fmt"
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
	os.Exit(run(*root, *check))
}

// run renders every agents/*.md under root and returns the process exit code.
func run(root string, check bool) int {
	srcs, err := filepath.Glob(filepath.Join(root, "agents", "*.md"))
	if err != nil || len(srcs) == 0 {
		fmt.Fprintln(os.Stderr, "hostgen: no agents/*.md under", root)
		return exitFailure
	}
	stale := 0
	for _, src := range srcs {
		name := strings.TrimSuffix(filepath.Base(src), ".md")
		out, rerr := render(src, name)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "hostgen:", rerr)
			return exitFailure
		}
		dst := filepath.Join(root, "hosts", "copilot", "agents", hosts.CopilotAgentName(name)+".agent.md")
		if cur, _ := os.ReadFile(dst); bytes.Equal(cur, out) {
			continue
		}
		if check {
			fmt.Fprintln(os.Stderr, "stale:", dst)
			stale++
			continue
		}
		if werr := write(dst, out); werr != nil {
			fmt.Fprintln(os.Stderr, "hostgen:", werr)
			return exitFailure
		}
		fmt.Fprintln(os.Stdout, "wrote", dst)
	}
	if stale > 0 {
		return 1
	}
	return 0
}

// render reads one Claude Code agent definition and returns the Copilot file
// it generates into.
func render(src, name string) ([]byte, error) {
	in, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	return hosts.RenderCopilotAgent(name, in)
}

// write puts one generated file at dst, creating the host's agents directory
// the first time.
func write(dst string, out []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return os.WriteFile(dst, out, 0o600)
}
