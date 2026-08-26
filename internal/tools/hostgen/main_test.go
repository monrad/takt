package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ccAgent is one Claude Code agent definition, the only input hostgen has.
const ccAgent = `---
name: planner
description: Turns the spec into a plan.
model: fable
tools: Read, Grep, Glob, Write
---

You write the plan for one run.
`

// TestRunGeneratesChecksAndSweeps drives hostgen's whole contract against a
// throwaway tree: gen writes the file a source produces, check is then
// silent, and a generated file no source produces any more is reported by
// check and removed by gen. The orphan half is the one hostgen could not see
// before — a content comparison never visits a file it does not render.
func TestRunGeneratesChecksAndSweeps(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "planner.md"), []byte(ccAgent), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "hosts", "copilot", "agents", "takt-planner.agent.md")

	if code := run(root, false); code != 0 {
		t.Fatalf("gen exit %d, want 0", code)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal("gen must write the agent the source produces:", err)
	}
	if code := run(root, true); code != 0 {
		t.Fatalf("check exit %d on a freshly generated tree, want 0", code)
	}

	orphan := filepath.Join(root, "hosts", "copilot", "agents", "takt-gone.agent.md")
	if err := os.WriteFile(orphan, []byte("left behind\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(root, true); code != 1 {
		t.Fatalf("check exit %d with an orphan present, want 1", code)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("check must report without writing:", err)
	}
	if code := run(root, false); code != 0 {
		t.Fatalf("gen exit %d, want 0", code)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("gen must delete the orphan: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal("the sweep must not touch a file a source still produces:", err)
	}
}

// TestRunRefusesATreeWithNoAgents pins hostgen's own error code: exit 1 is
// reserved for a staleness report, so a run that found nothing to render at
// all must not be readable as "one file is stale" either.
func TestRunRefusesATreeWithNoAgents(t *testing.T) {
	t.Parallel()
	if code := run(t.TempDir(), true); code != exitFailure {
		t.Fatalf("exit %d, want %d", code, exitFailure)
	}
}
