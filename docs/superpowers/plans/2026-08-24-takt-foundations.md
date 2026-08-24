# takt Foundations Implementation Plan (plan 1 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the deterministic foundations of `takt` — git wrapper, config, run bundle (state / events / lock), plan index (validation + wave assignment), goals parsing — and the CLI commands that need only those: `takt init`, `takt status`, `takt plan validate`, `takt version`, `takt doctor` (2 checks).

**Architecture:** A single Go binary (`cmd/takt`) over small `internal/` packages with one responsibility each. Every command prints one JSON object on stdout and exits 0, or a JSON error on stderr with exit 1 (usage: 2). All file paths stored anywhere are relative to the repo root. Plans 2 and 3 add the op loop (`decide`, `next`, waves, gates, backends) and the Claude Code plugin on top of these packages without changing their interfaces.

**Tech Stack:** Go 1.26, standard library only (no third-party modules). External programs: `git`. Tests: `go test` with temp git repos. Lint: golangci-lint with the maratori golden config.

**Spec:** `docs/superpowers/specs/2026-08-24-takt-design.md` (sections cited per task as §N).

## Global Constraints

- Module path `github.com/monrad/takt`; `go 1.26` in `go.mod`; **no third-party dependencies** (spec §3.4).
- **Every stored path is relative to the repo root**; absolute paths, `..`, or paths resolving outside the repo are rejected (spec §4.5).
- `state.json` is written atomically (temp file + rename) with stable key order; `events.jsonl` is append-only (spec §13).
- Commands: one JSON object on stdout + exit 0; errors as `{"error": "...", "hint": "..."}` on stderr + exit 1; usage errors exit 2 (spec §5.1).
- takt never creates worktrees and never checks out another branch after `init` (spec §4.7, D8/D9).
- Branch rule: on the default branch create and check out `takt/<slug>`; otherwise adopt the current branch and set `branch_adopted: true` (spec D9).
- Bundle directory precedence: `--dir` › `TAKT_DIR` › `.takt.json` `dir` › `docs/takt` (spec §4.1).
- Commit messages from takt: `takt(<slug>): <what>` (spec §4.7).
- Lint: golden config from `github.com/maratori/golangci-lint-config`, `local-prefixes: github.com/monrad/takt` (spec §14).
- **Never `git push`, never add a remote, never create the GitHub repository.** The user creates `github.com/monrad/takt` and pushes when they decide to; every commit in this plan is local.
- Test helpers create git repos with `git init -b main` and a local `user.name`/`user.email`; tests never touch the user's global git config.

---

## File Structure

```
go.mod                                   module github.com/monrad/takt, go 1.26
.golangci.yml                            golden config (Task 1)
.gitignore
cmd/takt/main.go                         os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, cwd))
internal/cli/cli.go                      Main(): subcommand dispatch, JSON output/error helpers, exit codes
internal/cli/cmd_version.go              `takt version [--expect v]`
internal/cli/cmd_init.go                 `takt init`
internal/cli/cmd_status.go               `takt status [--json]`
internal/cli/cmd_plan.go                 `takt plan validate [path]`
internal/cli/cmd_doctor.go               `takt doctor`
internal/version/version.go              Version string (ldflags)
internal/gitx/git.go                     Repo: Open, Run, CurrentBranch, DefaultBranch, HeadSHA, MergeBase, Porcelain, HasStaged, CreateBranch, Commit
internal/gitx/porcelain.go               ParsePorcelainZ
internal/testutil/gitrepo.go             NewRepo(t) — temp repo with one commit on main
internal/config/config.go                Config struct, Defaults(), Load(repoRoot, home, getenv)
internal/bundle/dir.go                   ResolveDir, Dir{RepoRoot, Base, InRepo}, Dir.Bundle(slug)
internal/bundle/state.go                 State struct + Load/Save (atomic)
internal/bundle/events.go                Event, AppendEvent
internal/bundle/lock.go                  Acquire (advisory session lock)
internal/bundle/paths.go                 RelPath validation helper
internal/plan/index.go                   Index, Task, ParseIndex
internal/plan/validate.go                Validate, Problem, ValidateOpts
internal/plan/waves.go                   AssignWaves (Kahn)
internal/goals/goals.go                  Goals, Goal, Parse, Hash
internal/doctor/doctor.go                Finding, Check, Run
internal/doctor/state_schema.go          check: state-schema
internal/doctor/plan_disjoint.go         check: plan-disjoint
```

Each package has `_test.go` files beside it. Packages depend downward only: `cli` → `bundle`/`plan`/`goals`/`doctor`/`config`/`gitx`; `bundle` → `gitx`, `config`; `doctor` → `bundle`, `plan`; nothing imports `cli`.

---

### Task 1: Repository scaffold, version command, lint config

**Files:**
- Create: `go.mod`, `.gitignore`, `.golangci.yml`, `cmd/takt/main.go`, `internal/version/version.go`, `internal/cli/cli.go`, `internal/cli/cmd_version.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Produces: `cli.Main(args []string, stdout, stderr io.Writer, getenv func(string) string, cwd string) int` — every later CLI task registers a subcommand in the `commands` map in `cli.go`.
- Produces: `cli.writeJSON(w io.Writer, v any) error` and `cli.fail(stderr io.Writer, code int, msg, hint string) int` — the output contract every command uses.
- Produces: `version.Version` (string, default `"0.0.0-dev"`, overridden with `-ldflags "-X github.com/monrad/takt/internal/version.Version=1.2.3"`).

- [ ] **Step 1: Initialise the module and write the failing test**

```bash
cd ~/code/misc/takt
go mod init github.com/monrad/takt
sed -i 's/^go .*/go 1.26/' go.mod
printf '/takt\n/dist/\n*.test\n/docs/takt/*/logs/\n' > .gitignore
mkdir -p cmd/takt internal/cli internal/version
```

`internal/cli/cli_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/version"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Main(args, &out, &errb, func(string) string { return "" }, t.TempDir())
	return code, out.String(), errb.String()
}

func TestVersionPrintsJSON(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	if got["version"] != version.Version {
		t.Fatalf("version = %q, want %q", got["version"], version.Version)
	}
}

func TestVersionExpectMismatchExits1(t *testing.T) {
	code, _, errb := run(t, "version", "--expect", "9.9.9")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb, `"error"`) || !strings.Contains(errb, "9.9.9") {
		t.Fatalf("stderr = %q", errb)
	}
}

func TestUnknownCommandExits2(t *testing.T) {
	code, _, errb := run(t, "bogus")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "unknown command") {
		t.Fatalf("stderr = %q", errb)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/`
Expected: FAIL — `undefined: Main`.

- [ ] **Step 3: Write the minimal implementation**

`internal/version/version.go`:

```go
// Package version holds the build-time version string.
package version

// Version is stamped at build time with
// -ldflags "-X github.com/monrad/takt/internal/version.Version=<tag>".
var Version = "0.0.0-dev"
```

`internal/cli/cli.go`:

```go
// Package cli implements the takt command-line surface: one JSON object on
// stdout and exit 0 on success; {"error","hint"} on stderr with exit 1; usage
// errors exit 2 (spec §5.1).
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Env is what a command may read from its environment. Injected so tests
// never depend on the real process.
type Env struct {
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	Cwd    string
}

type command func(Env) int

var commands = map[string]command{
	"version": cmdVersion,
}

// Main dispatches args[0] to a subcommand and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer, getenv func(string) string, cwd string) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return fail(stderr, 2, "unknown command: "+args[0], "run `takt help`")
	}
	return cmd(Env{Args: args[1:], Stdout: stdout, Stderr: stderr, Getenv: getenv, Cwd: cwd})
}

func usage(w io.Writer) int {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return fail(w, 2, "usage: takt <command> [flags]", "commands: "+strings.Join(names, ", "))
}

// writeJSON prints v as a single pretty JSON object followed by a newline.
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// fail prints a structured error to stderr and returns code.
func fail(w io.Writer, code int, msg, hint string) int {
	_ = writeJSON(w, map[string]string{"error": msg, "hint": hint})
	return code
}
```

`internal/cli/cmd_version.go`:

```go
package cli

import (
	"flag"

	"github.com/monrad/takt/internal/version"
)

func cmdVersion(env Env) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	expect := fs.String("expect", "", "fail unless the version equals this value")
	if err := fs.Parse(env.Args); err != nil {
		return 2
	}
	if *expect != "" && *expect != version.Version {
		return fail(env.Stderr, 1,
			"takt version "+version.Version+" does not match expected "+*expect,
			"install the takt binary matching the plugin version")
	}
	if err := writeJSON(env.Stdout, map[string]string{"version": version.Version}); err != nil {
		return 1
	}
	return 0
}
```

`cmd/takt/main.go`:

```go
// Command takt is the CLI entry point; all logic lives in internal/cli.
package main

import (
	"os"

	"github.com/monrad/takt/internal/cli"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, cwd))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./... && go build -o /dev/null ./cmd/takt`
Expected: `ok github.com/monrad/takt/internal/cli`.

- [ ] **Step 5: Add the golden golangci-lint config**

Find the installed linter version and fetch the config tag that matches it (the config repo's tags track golangci-lint releases — spec §14):

```bash
golangci-lint --version            # e.g. "golangci-lint has version 2.4.0"
V=$(golangci-lint --version | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
curl -fsSL "https://raw.githubusercontent.com/maratori/golangci-lint-config/v${V}/.golangci.yml" -o .golangci.yml \
  || curl -fsSL "https://raw.githubusercontent.com/maratori/golangci-lint-config/main/.golangci.yml" -o .golangci.yml
```

Then set the module prefix: open `.golangci.yml`, find `local-prefixes:` under `formatters.settings.goimports` and set it to `github.com/monrad/takt`. If `golangci-lint` is not installed, run it via Nix for this repo: `nix shell nixpkgs#golangci-lint -c golangci-lint run ./...`.

Run: `golangci-lint run ./...`
Expected: no findings (fix any it reports in the three files above — typically missing package comments or unchecked errors).

- [ ] **Step 6: Commit**

```bash
git add go.mod .gitignore .golangci.yml cmd internal
git commit -m "feat: module scaffold, cli.Main output contract, version command, golden lint config"
```

---

### Task 2: gitx — the git wrapper and the test-repo helper

**Files:**
- Create: `internal/gitx/git.go`, `internal/gitx/porcelain.go`, `internal/testutil/gitrepo.go`
- Test: `internal/gitx/git_test.go`, `internal/gitx/porcelain_test.go`

**Interfaces:**
- Produces (`gitx`):
  - `type Repo struct { Root string }`; `func Open(ctx context.Context, cwd string) (*Repo, error)` — `Root` is `git rev-parse --show-toplevel`; error `ErrNotRepo` when cwd is outside a repo.
  - `func (r *Repo) Run(ctx context.Context, args ...string) (string, error)` — runs `git -C Root args...`, returns trimmed stdout; on non-zero exit the error message includes stderr.
  - `func (r *Repo) CurrentBranch(ctx) (string, error)`; `func (r *Repo) DefaultBranch(ctx, override string) (string, error)`; `func (r *Repo) HeadSHA(ctx) (string, error)`; `func (r *Repo) MergeBase(ctx, a, b string) (string, error)`; `func (r *Repo) BranchExists(ctx, name string) (bool, error)`; `func (r *Repo) CreateAndCheckout(ctx, name string) error`; `func (r *Repo) HasStaged(ctx) (bool, error)`; `func (r *Repo) Porcelain(ctx) ([]Entry, error)`; `func (r *Repo) Add(ctx, paths ...string) error`; `func (r *Repo) Commit(ctx, msg string) (sha string, err error)`.
  - `type Entry struct { X, Y byte; Path string; OrigPath string }` and `func ParsePorcelainZ(b []byte) ([]Entry, error)` — parses `git status --porcelain=v1 -z`.
- Produces (`testutil`): `func NewRepo(t *testing.T) string` — a temp repo on `main` with one commit, local identity set, GPG signing off; `func WriteFile(t *testing.T, root, rel, content string)`; `func Commit(t *testing.T, root, msg string) string`.

- [ ] **Step 1: Write the test-repo helper (it is test infrastructure, so no test of its own)**

`internal/testutil/gitrepo.go`:

```go
// Package testutil holds helpers shared by tests: temporary git repos with a
// known shape. Never imported by non-test code.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git runs git in dir and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null", // never read the developer's global config
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+dir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewRepo creates a temp repository on branch main with one commit.
func NewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "config", "user.name", "takt test")
	Git(t, dir, "config", "user.email", "takt@example.invalid")
	Git(t, dir, "config", "commit.gpgsign", "false")
	WriteFile(t, dir, "README.md", "# fixture\n")
	Git(t, dir, "add", "README.md")
	Git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

// WriteFile writes content to root/rel, creating parent directories.
func WriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit stages everything and commits; returns the new HEAD sha.
func Commit(t *testing.T, root, msg string) string {
	t.Helper()
	Git(t, root, "add", "-A")
	Git(t, root, "commit", "-q", "-m", msg)
	return Git(t, root, "rev-parse", "HEAD")
}
```

- [ ] **Step 2: Write the failing porcelain parser test**

`internal/gitx/porcelain_test.go`:

```go
package gitx

import "testing"

func TestParsePorcelainZ(t *testing.T) {
	// " M a.go\0?? new.txt\0R  new-name.go\0old-name.go\0"
	in := []byte(" M a.go\x00?? new.txt\x00R  new-name.go\x00old-name.go\x00")
	got, err := ParsePorcelainZ(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{X: ' ', Y: 'M', Path: "a.go"},
		{X: '?', Y: '?', Path: "new.txt"},
		{X: 'R', Y: ' ', Path: "new-name.go", OrigPath: "old-name.go"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParsePorcelainZEmpty(t *testing.T) {
	got, err := ParsePorcelainZ(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParsePorcelainZTruncated(t *testing.T) {
	if _, err := ParsePorcelainZ([]byte("M")); err == nil {
		t.Fatal("expected an error for a truncated record")
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/gitx/`
Expected: FAIL — `undefined: ParsePorcelainZ`.

- [ ] **Step 4: Implement the parser**

`internal/gitx/porcelain.go`:

```go
package gitx

import (
	"bytes"
	"errors"
)

// Entry is one record of `git status --porcelain=v1 -z`.
// X is the index status, Y the worktree status ("??" for untracked).
// OrigPath is set for renames/copies (the second NUL-terminated field).
type Entry struct {
	X, Y     byte
	Path     string
	OrigPath string
}

// ParsePorcelainZ parses NUL-separated porcelain v1 output.
func ParsePorcelainZ(b []byte) ([]Entry, error) {
	var out []Entry
	fields := bytes.Split(b, []byte{0})
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) == 0 {
			continue // trailing NUL
		}
		if len(f) < 4 || f[2] != ' ' {
			return nil, errors.New("gitx: malformed porcelain record: " + string(f))
		}
		e := Entry{X: f[0], Y: f[1], Path: string(f[3:])}
		if e.X == 'R' || e.X == 'C' {
			i++
			if i >= len(fields) {
				return nil, errors.New("gitx: rename record without original path")
			}
			e.OrigPath = string(fields[i])
		}
		out = append(out, e)
	}
	return out, nil
}
```

Run: `go test ./internal/gitx/` — Expected: PASS.

- [ ] **Step 5: Write the failing Repo tests**

`internal/gitx/git_test.go`:

```go
package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

func TestOpenFindsToplevelFromSubdir(t *testing.T) {
	root := testutil.NewRepo(t)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Open(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root != root {
		t.Fatalf("Root = %q, want %q", r.Root, root)
	}
}

func TestOpenOutsideRepo(t *testing.T) {
	_, err := Open(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v, want ErrNotRepo", err)
	}
}

func TestBranchesAndCommits(t *testing.T) {
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, _ := Open(ctx, root)

	if b, _ := r.CurrentBranch(ctx); b != "main" {
		t.Fatalf("CurrentBranch = %q", b)
	}
	if d, _ := r.DefaultBranch(ctx, ""); d != "main" {
		t.Fatalf("DefaultBranch = %q", d)
	}
	if d, _ := r.DefaultBranch(ctx, "trunk"); d != "trunk" {
		t.Fatalf("DefaultBranch override = %q", d)
	}
	base, _ := r.HeadSHA(ctx)

	if err := r.CreateAndCheckout(ctx, "takt/demo"); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.CurrentBranch(ctx); b != "takt/demo" {
		t.Fatalf("after checkout CurrentBranch = %q", b)
	}
	if ok, _ := r.BranchExists(ctx, "takt/demo"); !ok {
		t.Fatal("BranchExists false after create")
	}
	if ok, _ := r.BranchExists(ctx, "nope"); ok {
		t.Fatal("BranchExists true for missing branch")
	}

	testutil.WriteFile(t, root, "x.txt", "x\n")
	if staged, _ := r.HasStaged(ctx); staged {
		t.Fatal("HasStaged true with only an untracked file")
	}
	if err := r.Add(ctx, "x.txt"); err != nil {
		t.Fatal(err)
	}
	if staged, _ := r.HasStaged(ctx); !staged {
		t.Fatal("HasStaged false after add")
	}
	sha, err := r.Commit(ctx, "takt(demo): wave 0")
	if err != nil || len(sha) != 40 {
		t.Fatalf("Commit = %q, %v", sha, err)
	}
	if mb, _ := r.MergeBase(ctx, "main", "takt/demo"); mb != base {
		t.Fatalf("MergeBase = %q, want %q", mb, base)
	}
}

func TestPorcelainRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := testutil.NewRepo(t)
	r, _ := Open(ctx, root)
	testutil.WriteFile(t, root, "README.md", "changed\n")
	testutil.WriteFile(t, root, "dir/new.go", "package dir\n")
	entries, err := r.Porcelain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Path] = e
	}
	if e := got["README.md"]; e.Y != 'M' {
		t.Fatalf("README.md = %+v", e)
	}
	if e := got["dir/new.go"]; e.X != '?' {
		t.Fatalf("dir/new.go = %+v", e)
	}
}

func TestRunErrorIncludesStderr(t *testing.T) {
	ctx := context.Background()
	r, _ := Open(ctx, testutil.NewRepo(t))
	_, err := r.Run(ctx, "rev-parse", "--verify", "definitely-not-a-ref")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); len(got) < 10 {
		t.Fatalf("error too terse: %q", got)
	}
}
```

- [ ] **Step 6: Run to verify they fail**

Run: `go test ./internal/gitx/`
Expected: FAIL — `undefined: Open`, `ErrNotRepo`, …

- [ ] **Step 7: Implement Repo**

`internal/gitx/git.go`:

```go
// Package gitx is a thin wrapper over the git CLI. Every call is
// -C-qualified to the repository root, so callers never depend on the
// process cwd (spec §4.5). No network operations live here (spec §13).
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrNotRepo is returned by Open when cwd is not inside a git work tree.
var ErrNotRepo = errors.New("gitx: not inside a git repository")

// Repo is a handle on one work tree (linked or primary).
type Repo struct {
	Root string
}

// Open resolves the work-tree root for cwd.
func Open(ctx context.Context, cwd string) (*Repo, error) {
	out, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRepo, cwd)
	}
	return &Repo{Root: out}, nil
}

// Run executes git with args in the repo root and returns trimmed stdout.
func (r *Repo) Run(ctx context.Context, args ...string) (string, error) {
	return runGit(ctx, r.Root, args...)
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// CurrentBranch returns the checked-out branch name ("HEAD" when detached).
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBranch returns override if non-empty, else origin/HEAD's branch,
// else "main" if it exists, else "master".
func (r *Repo) DefaultBranch(ctx context.Context, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if ref, err := r.Run(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(ref, "origin/"), nil
	}
	for _, cand := range []string{"main", "master"} {
		if ok, _ := r.BranchExists(ctx, cand); ok {
			return cand, nil
		}
	}
	return "", errors.New("gitx: cannot determine the default branch; set default_branch in .takt.json")
}

// HeadSHA returns the full sha of HEAD.
func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "HEAD")
}

// MergeBase returns the merge base of two refs.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	return r.Run(ctx, "merge-base", a, b)
}

// BranchExists reports whether a local branch exists.
func (r *Repo) BranchExists(ctx context.Context, name string) (bool, error) {
	_, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}

// CreateAndCheckout creates a branch at HEAD and checks it out.
func (r *Repo) CreateAndCheckout(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "checkout", "-q", "-b", name)
	return err
}

// HasStaged reports whether the index differs from HEAD.
func (r *Repo) HasStaged(ctx context.Context) (bool, error) {
	_, err := r.Run(ctx, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	if strings.Contains(err.Error(), "exit status 1") {
		return true, nil
	}
	return false, err
}

// Porcelain returns the parsed `git status --porcelain=v1 -z` entries.
func (r *Repo) Porcelain(ctx context.Context) ([]Entry, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return ParsePorcelainZ(out)
}

// Add stages exactly the given paths (never -A).
func (r *Repo) Add(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := r.Run(ctx, append([]string{"add", "--"}, paths...)...)
	return err
}

// Commit commits the index with msg and returns the new HEAD sha.
func (r *Repo) Commit(ctx context.Context, msg string) (string, error) {
	if _, err := r.Run(ctx, "commit", "-q", "-m", msg); err != nil {
		return "", err
	}
	return r.HeadSHA(ctx)
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/gitx/ -v`
Expected: all PASS. If `TestBranchesAndCommits` fails at `Commit` with an identity error, the helper's `user.name` config did not apply — check `testutil.NewRepo` ran `git config` before the first commit.

- [ ] **Step 9: Lint and commit**

```bash
golangci-lint run ./...
git add internal/gitx internal/testutil
git commit -m "feat(gitx): git wrapper with porcelain parser and temp-repo test helper"
```

---

### Task 3: config — defaults and layered loading

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config` (struct below), `config.Defaults() Config`, `config.Load(repoRoot, home string, getenv func(string) string) (Config, []string, error)` — returns the merged config and the list of files that were read, in precedence order low→high. Precedence (spec §12): defaults ‹ `~/.config/takt/config.json` ‹ `<repo>/.takt.json` ‹ `TAKT_CONFIG=<path>` (replaces the repo file). `TAKT_DIR` is *not* applied here — `bundle.ResolveDir` handles it (Task 4).
- Produces: `config.Duration` — a `time.Duration` that (un)marshals as a string like `"30m"`.

- [ ] **Step 1: Write the failing tests**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Dir != "docs/takt" || d.Autonomy != "auto" || d.MaxParallel != 8 || d.MaxRework != 1 || d.MaxFilesPerTask != 12 {
		t.Fatalf("defaults = %+v", d)
	}
	if !d.Review.Spec || !d.Review.Plan || !d.Review.Tasks || !d.Goals || !d.Alignment {
		t.Fatalf("gates must default on: %+v", d)
	}
	if time.Duration(d.WaveStaleAfter) != 30*time.Minute || time.Duration(d.LockTTL) != 10*time.Minute || time.Duration(d.VerifyTimeout) != 10*time.Minute {
		t.Fatalf("durations = %+v", d)
	}
	if d.Agents.Planner.Model != "fable" || d.Agents.Implementer.Model != "opus" || d.Agents.Implementer.ByClass["mechanical"] != "haiku" {
		t.Fatalf("agent models = %+v", d.Agents)
	}
	if len(d.Backends.Reviewer) != 2 || d.Backends.Reviewer[0] != "copilot" {
		t.Fatalf("reviewer chain = %v", d.Backends.Reviewer)
	}
}

func TestLoadPrecedence(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	write(t, filepath.Join(home, ".config", "takt", "config.json"),
		`{"max_parallel": 2, "backends": {"copilot": {"model": "gpt-user"}}, "agents": {"implementer": {"by_class": {"docs": "haiku"}}}}`)
	write(t, filepath.Join(repo, ".takt.json"),
		`{"dir": "plans", "max_parallel": 4, "wave_stale_after": "5m"}`)

	cfg, sources, err := Load(repo, home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %v", sources)
	}
	if cfg.Dir != "plans" {
		t.Errorf("repo file must win for dir: %q", cfg.Dir)
	}
	if cfg.MaxParallel != 4 {
		t.Errorf("repo file must win for max_parallel: %d", cfg.MaxParallel)
	}
	if cfg.Backends.Copilot.Model != "gpt-user" {
		t.Errorf("user file value must survive: %q", cfg.Backends.Copilot.Model)
	}
	if cfg.Backends.Copilot.Effort != "high" {
		t.Errorf("unset fields keep defaults: %q", cfg.Backends.Copilot.Effort)
	}
	if cfg.Agents.Implementer.ByClass["docs"] != "haiku" || cfg.Agents.Implementer.ByClass["mechanical"] != "haiku" {
		t.Errorf("by_class must merge by key: %v", cfg.Agents.Implementer.ByClass)
	}
	if time.Duration(cfg.WaveStaleAfter) != 5*time.Minute {
		t.Errorf("duration string not parsed: %v", cfg.WaveStaleAfter)
	}
}

func TestLoadTaktConfigEnvReplacesRepoFile(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".takt.json"), `{"max_parallel": 4}`)
	alt := filepath.Join(t.TempDir(), "alt.json")
	write(t, alt, `{"max_parallel": 6}`)
	cfg, _, err := Load(repo, t.TempDir(), func(k string) string {
		if k == "TAKT_CONFIG" {
			return alt
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != 6 {
		t.Fatalf("TAKT_CONFIG must replace the repo file: %d", cfg.MaxParallel)
	}
}

func TestLoadRejectsBadJSONAndBadDuration(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".takt.json"), `{"max_parallel": `)
	if _, _, err := Load(repo, t.TempDir(), func(string) string { return "" }); err == nil {
		t.Fatal("expected a parse error")
	}
	write(t, filepath.Join(repo, ".takt.json"), `{"lock_ttl": "soon"}`)
	if _, _, err := Load(repo, t.TempDir(), func(string) string { return "" }); err == nil {
		t.Fatal("expected a duration error")
	}
}

func TestValidateRejectsUnknownAutonomyAndClass(t *testing.T) {
	c := Defaults()
	c.Autonomy = "yolo"
	if err := c.Validate(); err == nil {
		t.Fatal("autonomy must be auto|step")
	}
	c = Defaults()
	c.Agents.Implementer.ByClass["weird"] = "haiku"
	if err := c.Validate(); err == nil {
		t.Fatal("unknown task class must be rejected")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: Defaults`.

- [ ] **Step 3: Implement**

`internal/config/config.go`:

```go
// Package config loads takt's layered configuration (spec §12):
// defaults ‹ ~/.config/takt/config.json ‹ <repo>/.takt.json ‹ $TAKT_CONFIG.
// Per-run values are frozen into state.json at init (spec §12), so a config
// change mid-run never changes a running bundle's behaviour.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Duration is a time.Duration that (un)marshals as a Go duration string.
type Duration time.Duration

// UnmarshalJSON accepts "30m", "1h30m", etc.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"30m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("bad duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON renders the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Review toggles the three review gates.
type Review struct {
	Spec  bool `json:"spec"`
	Plan  bool `json:"plan"`
	Tasks bool `json:"tasks"`
}

// Backend configures one headless CLI backend.
type Backend struct {
	Model   string   `json:"model"`
	Effort  string   `json:"effort"`
	Timeout Duration `json:"timeout"`
}

// Backends lists the reviewer chain and per-backend settings.
type Backends struct {
	Reviewer []string `json:"reviewer"`
	Copilot  Backend  `json:"copilot"`
	Claude   Backend  `json:"claude"`
}

// Implementer maps task classes to models (spec D22).
type Implementer struct {
	Model           string            `json:"model"`
	ByClass         map[string]string `json:"by_class"`
	EscalateOnRetry bool              `json:"escalate_on_retry"`
}

// Agent pins the model for a single-purpose agent.
type Agent struct {
	Model string `json:"model"`
}

// Agents holds per-agent model settings.
type Agents struct {
	Implementer      Implementer `json:"implementer"`
	Planner          Agent       `json:"planner"`
	GoalAssessor     Agent       `json:"goal-assessor"`
	AlignmentAuditor Agent       `json:"alignment-auditor"`
}

// Config is the merged configuration.
type Config struct {
	Dir             string   `json:"dir"`
	Autonomy        string   `json:"autonomy"`
	Review          Review   `json:"review"`
	Goals           bool     `json:"goals"`
	Alignment       bool     `json:"alignment"`
	MaxParallel     int      `json:"max_parallel"`
	MaxRework       int      `json:"max_rework"`
	MaxFilesPerTask int      `json:"max_files_per_task"`
	WaveStaleAfter  Duration `json:"wave_stale_after"`
	LockTTL         Duration `json:"lock_ttl"`
	VerifyTimeout   Duration `json:"verify_timeout"`
	DefaultBranch   string   `json:"default_branch"`
	Backends        Backends `json:"backends"`
	Agents          Agents   `json:"agents"`
}

// TaskClasses is the closed set of plan task classes (spec §7.3).
var TaskClasses = []string{"mechanical", "bounded", "implement", "test", "docs"}

// Defaults returns the shipped defaults (spec §12).
func Defaults() Config {
	return Config{
		Dir:             "docs/takt",
		Autonomy:        "auto",
		Review:          Review{Spec: true, Plan: true, Tasks: true},
		Goals:           true,
		Alignment:       true,
		MaxParallel:     8,
		MaxRework:       1,
		MaxFilesPerTask: 12,
		WaveStaleAfter:  Duration(30 * time.Minute),
		LockTTL:         Duration(10 * time.Minute),
		VerifyTimeout:   Duration(10 * time.Minute),
		Backends: Backends{
			Reviewer: []string{"copilot", "claude"},
			Copilot:  Backend{Model: "gpt-5.6-sol", Effort: "high", Timeout: Duration(5 * time.Minute)},
			Claude:   Backend{Model: "opus", Effort: "high", Timeout: Duration(5 * time.Minute)},
		},
		Agents: Agents{
			Implementer: Implementer{
				Model:           "opus",
				ByClass:         map[string]string{"mechanical": "haiku", "bounded": "sonnet", "test": "sonnet", "docs": "sonnet"},
				EscalateOnRetry: true,
			},
			Planner:          Agent{Model: "fable"},
			GoalAssessor:     Agent{Model: "sonnet"},
			AlignmentAuditor: Agent{Model: "sonnet"},
		},
	}
}

// Load merges the config layers. sources lists files read, low→high.
func Load(repoRoot, home string, getenv func(string) string) (Config, []string, error) {
	cfg := Defaults()
	var sources []string
	layers := []string{filepath.Join(home, ".config", "takt", "config.json")}
	if alt := getenv("TAKT_CONFIG"); alt != "" {
		layers = append(layers, alt)
	} else {
		layers = append(layers, filepath.Join(repoRoot, ".takt.json"))
	}
	for _, p := range layers {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return cfg, sources, err
		}
		// Unmarshal into the existing value: only keys present override,
		// and maps merge by key — this is the whole layering mechanism.
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, sources, fmt.Errorf("%s: %w", p, err)
		}
		sources = append(sources, p)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, sources, err
	}
	return cfg, sources, nil
}

// Validate rejects values outside their closed sets.
func (c Config) Validate() error {
	if c.Autonomy != "auto" && c.Autonomy != "step" {
		return fmt.Errorf("autonomy must be auto or step, got %q", c.Autonomy)
	}
	for class := range c.Agents.Implementer.ByClass {
		if !IsTaskClass(class) {
			return fmt.Errorf("agents.implementer.by_class: unknown task class %q", class)
		}
	}
	if c.MaxParallel < 1 || c.MaxRework < 0 || c.MaxFilesPerTask < 1 {
		return errors.New("max_parallel and max_files_per_task must be ≥ 1, max_rework ≥ 0")
	}
	return nil
}

// IsTaskClass reports whether s is one of TaskClasses.
func IsTaskClass(s string) bool {
	for _, c := range TaskClasses {
		if c == s {
			return true
		}
	}
	return false
}

// ImplementerModel resolves the model for a task class (spec D22).
func (c Config) ImplementerModel(class string) string {
	if m, ok := c.Agents.Implementer.ByClass[class]; ok && m != "" {
		return m
	}
	return c.Agents.Implementer.Model
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
golangci-lint run ./...
git add internal/config
git commit -m "feat(config): defaults, layered loading (user < repo < TAKT_CONFIG), duration strings, task classes"
```

---

### Task 4: bundle directory resolution and path rules

**Files:**
- Create: `internal/bundle/dir.go`, `internal/bundle/paths.go`
- Test: `internal/bundle/dir_test.go`, `internal/bundle/paths_test.go`

**Interfaces:**
- Produces: `bundle.Dir{RepoRoot, Base string; InRepo bool; RepoName string}`; `bundle.ResolveDir(repoRoot, home, flag, env, cfgDir string) (Dir, error)` — precedence `flag › env › cfgDir › "docs/takt"` (spec §4.1). `Dir.Bundle(slug) string` is the absolute bundle path; `Dir.RelToRepo(abs) (string, error)` converts an absolute path under the repo to the repo-relative form used in state; `Dir.ListSlugs() ([]string, error)` lists bundle directories that contain a `state.json`.
- Produces: `bundle.CheckRelPath(repoRoot, p string) error` — rejects absolute paths, `..` segments, and paths resolving outside the repo (spec §4.5); used by plan validation (Task 6) and by every state write.

- [ ] **Step 1: Write the failing tests**

`internal/bundle/dir_test.go`:

```go
package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDirPrecedence(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	cases := []struct {
		name, flag, env, cfg, wantBase string
		wantInRepo               bool
	}{
		{"default", "", "", "", filepath.Join(repo, "docs", "takt"), true},
		{"cfg", "", "", "plans", filepath.Join(repo, "plans"), true},
		{"env beats cfg", "", "/var/takt", "plans", "/var/takt", false},
		{"flag beats env", "x", "/var/takt", "plans", filepath.Join(repo, "x"), true},
		{"tilde", "", "~/runs", "", filepath.Join(home, "runs"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := ResolveDir(repo, home, c.flag, c.env, c.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if d.Base != c.wantBase || d.InRepo != c.wantInRepo {
				t.Fatalf("got %+v, want base=%q inRepo=%v", d, c.wantBase, c.wantInRepo)
			}
		})
	}
}

func TestResolveDirRejectsEscapingRelative(t *testing.T) {
	if _, err := ResolveDir(t.TempDir(), t.TempDir(), "../elsewhere", "", ""); err == nil {
		t.Fatal("relative dir outside the repo must be rejected")
	}
}

func TestBundlePathInRepoAndExternal(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	in, _ := ResolveDir(repo, t.TempDir(), "", "", "")
	if got := in.Bundle("demo"); got != filepath.Join(repo, "docs", "takt", "demo") {
		t.Fatalf("in-repo bundle = %q", got)
	}
	ext, _ := ResolveDir(repo, t.TempDir(), "", "/srv/takt", "")
	if got := ext.Bundle("demo"); got != filepath.Join("/srv/takt", "myrepo", "demo") {
		t.Fatalf("external bundle = %q (external dirs are namespaced by repo name)", got)
	}
}

func TestListSlugs(t *testing.T) {
	repo := t.TempDir()
	d, _ := ResolveDir(repo, t.TempDir(), "", "", "")
	for _, s := range []string{"b", "a"} {
		if err := os.MkdirAll(d.Bundle(s), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d.Bundle(s), "state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(d.Bundle("no-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := d.ListSlugs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("ListSlugs = %v (sorted, state.json required)", got)
	}
}

func TestRelToRepo(t *testing.T) {
	repo := t.TempDir()
	d, _ := ResolveDir(repo, t.TempDir(), "", "", "")
	rel, err := d.RelToRepo(filepath.Join(repo, "docs", "takt", "x", "spec.md"))
	if err != nil || rel != "docs/takt/x/spec.md" {
		t.Fatalf("RelToRepo = %q, %v", rel, err)
	}
	if _, err := d.RelToRepo("/elsewhere/spec.md"); err == nil {
		t.Fatal("paths outside the repo must error")
	}
}
```

`internal/bundle/paths_test.go`:

```go
package bundle

import "testing"

func TestCheckRelPath(t *testing.T) {
	root := t.TempDir()
	ok := []string{"a.go", "dir/b.go", "docs/x/y.md"}
	for _, p := range ok {
		if err := CheckRelPath(root, p); err != nil {
			t.Errorf("%q should be accepted: %v", p, err)
		}
	}
	bad := []string{"/abs/a.go", "../a.go", "dir/../../a.go", "", "dir/./../../x"}
	for _, p := range bad {
		if err := CheckRelPath(root, p); err == nil {
			t.Errorf("%q should be rejected", p)
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/bundle/`
Expected: FAIL — `undefined: ResolveDir`, `CheckRelPath`.

- [ ] **Step 3: Implement**

`internal/bundle/paths.go`:

```go
// Package bundle owns the run bundle on disk: directory resolution, the
// single-writer state.json, the append-only events.jsonl, and the advisory
// session lock (spec §4).
package bundle

import (
	"errors"
	"path/filepath"
	"strings"
)

// CheckRelPath enforces spec §4.5: p must be a clean, relative path that
// stays inside root. It never touches the filesystem.
func CheckRelPath(root, p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if filepath.IsAbs(p) {
		return errors.New("absolute path not allowed: " + p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("path escapes the repository: " + p)
	}
	abs := filepath.Join(root, clean)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return errors.New("path resolves outside the repository: " + p)
	}
	return nil
}
```

`internal/bundle/dir.go`:

```go
package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDir is the bundle directory when nothing else is configured.
const DefaultDir = "docs/takt"

// Dir is the resolved bundle location for one repository.
type Dir struct {
	RepoRoot string // absolute work-tree root
	Base     string // absolute directory holding bundles
	InRepo   bool   // Base is inside RepoRoot → bundles are committed
	RepoName string // filepath.Base(RepoRoot); namespaces external dirs
}

// ResolveDir applies the precedence flag › env › cfgDir › DefaultDir (spec §4.1).
// A relative value is inside the repo; an absolute or ~-prefixed one is external.
func ResolveDir(repoRoot, home, flag, env, cfgDir string) (Dir, error) {
	raw := DefaultDir
	switch {
	case flag != "":
		raw = flag
	case env != "":
		raw = env
	case cfgDir != "":
		raw = cfgDir
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		raw = filepath.Join(home, strings.TrimPrefix(raw, "~"))
	}
	d := Dir{RepoRoot: repoRoot, RepoName: filepath.Base(repoRoot)}
	if filepath.IsAbs(raw) {
		d.Base = filepath.Clean(raw)
		d.InRepo = strings.HasPrefix(d.Base, repoRoot+string(filepath.Separator))
		return d, nil
	}
	if err := CheckRelPath(repoRoot, raw); err != nil {
		return Dir{}, errors.New("bundle dir: " + err.Error())
	}
	d.Base = filepath.Join(repoRoot, raw)
	d.InRepo = true
	return d, nil
}

// Bundle returns the absolute directory of one run.
func (d Dir) Bundle(slug string) string {
	if d.InRepo {
		return filepath.Join(d.Base, slug)
	}
	return filepath.Join(d.Base, d.RepoName, slug)
}

// root returns the directory that holds this repo's bundles.
func (d Dir) root() string {
	if d.InRepo {
		return d.Base
	}
	return filepath.Join(d.Base, d.RepoName)
}

// ListSlugs returns, sorted, every subdirectory containing a state.json.
func (d Dir) ListSlugs() ([]string, error) {
	entries, err := os.ReadDir(d.root())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var slugs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(d.root(), e.Name(), "state.json")); err == nil {
			slugs = append(slugs, e.Name())
		}
	}
	sort.Strings(slugs)
	return slugs, nil
}

// RelToRepo converts an absolute path under RepoRoot to the slash-separated
// repo-relative form stored in state (spec §4.5).
func (d Dir) RelToRepo(abs string) (string, error) {
	rel, err := filepath.Rel(d.RepoRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("path is outside the repository: " + abs)
	}
	return filepath.ToSlash(rel), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bundle/ -v`
Expected: all PASS. On macOS `t.TempDir()` may live under a symlinked `/var` → `/private/var`; if `TestRelToRepo` fails only there, resolve both sides with `filepath.EvalSymlinks` in the test, not in the code.

- [ ] **Step 5: Commit**

```bash
golangci-lint run ./...
git add internal/bundle
git commit -m "feat(bundle): directory resolution (flag > env > config > default, in-repo or external) and repo-relative path rule"
```

---

### Task 5: bundle state, events, and the session lock

**Files:**
- Create: `internal/bundle/state.go`, `internal/bundle/events.go`, `internal/bundle/lock.go`
- Test: `internal/bundle/state_test.go`, `internal/bundle/events_test.go`, `internal/bundle/lock_test.go`

**Interfaces:**
- Produces (`bundle`):
  - `const SchemaVersion = 1`; phase constants `PhaseBrainstorm, PhasePlan, PhaseExecute, PhaseFinish, PhaseArchived`; task-status constants `StatusPending, StatusDone, StatusFailed, StatusBlocked, StatusWaived`.
  - `type State struct` (fields below, JSON names exactly as spec §4.3), `type Task`, `type ActiveWave`, `type BaselineEntry`, `type PendingGate`, `type Session`, `type RunConfig`.
  - `func LoadState(bundleDir string) (*State, error)`; `func SaveState(bundleDir string, s *State) error` (atomic temp+rename, `MarshalIndent`, trailing newline); `func (s *State) Validate() error`; `func (s *State) Task(id int) *Task`.
  - `type Event struct { TS time.Time; Type string; Data map[string]any }`; `func AppendEvent(bundleDir, typ string, data map[string]any) error`; `func ReadEvents(bundleDir string) ([]Event, error)`.
  - `type LockOutcome string` with `LockAcquired, LockHeldBySelf, LockStolen, LockForced, LockBlocked`; `func Acquire(s *State, id, host string, now time.Time, ttl time.Duration, force bool) LockOutcome` (mutates `s.Session` except on `LockBlocked`); `func Release(s *State)`.
  - `var renameFile = os.Rename` — package-level seam so a test can make the rename fail.

- [ ] **Step 1: Write the failing state tests**

`internal/bundle/state_test.go`:

```go
package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *State {
	return &State{
		Schema: SchemaVersion, TaktVersion: "0.0.0-dev",
		Slug: "demo", Topic: "add a thing", Phase: PhaseBrainstorm,
		CreatedAt: time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC),
		Branch: "takt/demo", Base: "main", BaseSHA: "abc123",
		Config: RunConfig{Autonomy: "auto", Review: ReviewConfig{Spec: true, Plan: true, Tasks: true}, Goals: true, Alignment: true, MaxParallel: 8, MaxRework: 1},
		Gates:  map[string]string{"spec": "pending", "plan": "pending"},
		Tasks:  []Task{},
	}
}

func TestSaveLoadRoundTripAndKeyOrder(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, sample()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	text := string(raw)
	for _, pair := range [][2]string{{`"schema"`, `"slug"`}, {`"slug"`, `"phase"`}, {`"phase"`, `"branch"`}, {`"tasks"`, `"session"`}} {
		if strings.Index(text, pair[0]) > strings.Index(text, pair[1]) {
			t.Errorf("key order: %s must precede %s", pair[0], pair[1])
		}
	}
	if !strings.HasSuffix(text, "}\n") {
		t.Error("state.json must end with a newline")
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "demo" || got.Phase != PhaseBrainstorm || got.Config.MaxParallel != 8 || got.Gates["plan"] != "pending" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Tasks == nil {
		t.Fatal("empty tasks must round-trip as [] not null")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, sample()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))

	orig := renameFile
	renameFile = func(string, string) error { return errors.New("disk on fire") }
	t.Cleanup(func() { renameFile = orig })

	s := sample()
	s.Phase = PhasePlan
	if err := SaveState(dir, s); err == nil {
		t.Fatal("expected the injected rename error")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(after) != string(before) {
		t.Fatal("a failed save must leave the previous state.json byte-identical")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestLoadRejectsNewerSchemaAndBadPhase(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schema": 99, "slug": "x", "phase": "brainstorm"}`), 0o644)
	if _, err := LoadState(dir); err == nil {
		t.Fatal("newer schema must be refused")
	}
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schema": 1, "slug": "x", "phase": "flying"}`), 0o644)
	if _, err := LoadState(dir); err == nil {
		t.Fatal("unknown phase must be refused")
	}
}

func TestValidateTasks(t *testing.T) {
	s := sample()
	s.Tasks = []Task{{ID: 1, Wave: 0, Status: "sleeping", Files: []string{"a.go"}, Class: "implement"}}
	if err := s.Validate(); err == nil {
		t.Fatal("unknown task status must be rejected")
	}
	s.Tasks[0].Status = StatusPending
	s.Tasks[0].Files = []string{"/abs.go"}
	if err := s.Validate(); err == nil {
		t.Fatal("absolute task file must be rejected")
	}
	s.Tasks[0].Files = []string{"a.go"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Task(1) == nil || s.Task(2) != nil {
		t.Fatal("Task(id) lookup")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/bundle/ -run 'TestSave|TestLoad|TestValidate'`
Expected: FAIL — `undefined: State`, `SaveState`, …

- [ ] **Step 3: Implement state.go**

```go
package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the state.json schema this binary writes and reads.
const SchemaVersion = 1

// Phases (spec §4.3): the only progress enum.
const (
	PhaseBrainstorm = "brainstorm"
	PhasePlan       = "plan"
	PhaseExecute    = "execute"
	PhaseFinish     = "finish"
	PhaseArchived   = "archived"
)

// Task statuses (spec §4.3).
const (
	StatusPending = "pending"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusBlocked = "blocked"
	StatusWaived  = "waived"
)

var phases = map[string]bool{PhaseBrainstorm: true, PhasePlan: true, PhaseExecute: true, PhaseFinish: true, PhaseArchived: true}
var statuses = map[string]bool{StatusPending: true, StatusDone: true, StatusFailed: true, StatusBlocked: true, StatusWaived: true}

// ReviewConfig mirrors config.Review; duplicated here so bundle does not
// import config (state is the frozen copy, spec §12).
type ReviewConfig struct {
	Spec  bool `json:"spec"`
	Plan  bool `json:"plan"`
	Tasks bool `json:"tasks"`
}

// RunConfig is the per-run configuration frozen at init.
type RunConfig struct {
	Autonomy    string       `json:"autonomy"`
	Review      ReviewConfig `json:"review"`
	Goals       bool         `json:"goals"`
	Alignment   bool         `json:"alignment"`
	MaxParallel int          `json:"max_parallel"`
	MaxRework   int          `json:"max_rework"`
}

// Task is one plan task as tracked in state.
type Task struct {
	ID         int             `json:"id"`
	Wave       int             `json:"wave"`
	Status     string          `json:"status"`
	Files      []string        `json:"files"`
	Class      string          `json:"class"`
	Attempt    int             `json:"attempt"`
	LastDigest json.RawMessage `json:"last_digest,omitempty"`
}

// BaselineEntry records a dirty/untracked path and its content hash before a wave launches.
type BaselineEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// ActiveWave marks a wave that has been dispatched and not yet closed.
type ActiveWave struct {
	N         int             `json:"n"`
	Slice     int             `json:"slice"`
	Attempt   int             `json:"attempt"`
	StartedAt time.Time       `json:"started_at"`
	SessionID string          `json:"session_id"`
	Baseline  []BaselineEntry `json:"baseline"`
}

// PendingGate is a durable question awaiting the user.
type PendingGate struct {
	ID       string          `json:"id"`
	OpenedAt time.Time       `json:"opened_at"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Session is the advisory lock holder (spec §4.6).
type Session struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Heartbeat time.Time `json:"heartbeat"`
}

// State is state.json. Field order is the on-disk key order.
type State struct {
	Schema          int               `json:"schema"`
	TaktVersion     string            `json:"takt_version"`
	Slug            string            `json:"slug"`
	Topic           string            `json:"topic"`
	Phase           string            `json:"phase"`
	CreatedAt       time.Time         `json:"created_at"`
	Branch          string            `json:"branch"`
	BranchAdopted   bool              `json:"branch_adopted"`
	Base            string            `json:"base"`
	BaseSHA         string            `json:"base_sha"`
	Config          RunConfig         `json:"config"`
	GoalsHash       *string           `json:"goals_hash"`
	Gates           map[string]string `json:"gates"`
	Tasks           []Task            `json:"tasks"`
	ActiveWave      *ActiveWave       `json:"active_wave"`
	PendingGate     *PendingGate      `json:"pending_gate"`
	VerifiedSHA     *string           `json:"verified_sha"`
	GoalsCheckedSHA *string           `json:"goals_checked_sha"`
	Session         *Session          `json:"session"`
}

// StatePath returns bundleDir/state.json.
func StatePath(bundleDir string) string { return filepath.Join(bundleDir, "state.json") }

// LoadState reads and validates state.json.
func LoadState(bundleDir string) (*State, error) {
	b, err := os.ReadFile(StatePath(bundleDir))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("state.json: %w", err)
	}
	if s.Schema > SchemaVersion {
		return nil, fmt.Errorf("state.json schema %d is newer than this takt (%d); upgrade takt", s.Schema, SchemaVersion)
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("state.json: %w", err)
	}
	return &s, nil
}

// renameFile is a seam for the atomicity test.
var renameFile = os.Rename

// SaveState writes state.json atomically: temp file in the same directory,
// fsync, rename (spec §13). Nil slices are normalised so JSON shows [] not null.
func SaveState(bundleDir string, s *State) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Tasks == nil {
		s.Tasks = []Task{}
	}
	if s.Gates == nil {
		s.Gates = map[string]string{}
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(bundleDir, "state.json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := renameFile(tmpName, StatePath(bundleDir)); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Validate checks closed sets and path rules; it does not touch the filesystem.
func (s *State) Validate() error {
	if s.Slug == "" {
		return errors.New("slug is empty")
	}
	if !phases[s.Phase] {
		return fmt.Errorf("unknown phase %q", s.Phase)
	}
	seen := map[int]bool{}
	for _, t := range s.Tasks {
		if seen[t.ID] {
			return fmt.Errorf("duplicate task id %d", t.ID)
		}
		seen[t.ID] = true
		if !statuses[t.Status] {
			return fmt.Errorf("task %d: unknown status %q", t.ID, t.Status)
		}
		if t.Wave < 0 {
			return fmt.Errorf("task %d: negative wave", t.ID)
		}
		for _, f := range t.Files {
			if err := CheckRelPath("/", f); err != nil { // root irrelevant for the syntactic checks
				return fmt.Errorf("task %d: %w", t.ID, err)
			}
		}
	}
	return nil
}

// Task returns the task with id, or nil.
func (s *State) Task(id int) *Task {
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			return &s.Tasks[i]
		}
	}
	return nil
}
```

Run: `go test ./internal/bundle/ -run 'TestSave|TestLoad|TestValidate' -v` — Expected: PASS.

- [ ] **Step 4: Write the failing events and lock tests**

`internal/bundle/events_test.go`:

```go
package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAndReadEvents(t *testing.T) {
	dir := t.TempDir()
	if err := AppendEvent(dir, "init", map[string]any{"slug": "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(dir, "phase", map[string]any{"to": "plan"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], `{"ts":`) {
		t.Fatalf("events.jsonl = %q", raw)
	}
	evs, err := ReadEvents(dir)
	if err != nil || len(evs) != 2 || evs[1].Type != "phase" || evs[1].Data["to"] != "plan" || evs[0].TS.IsZero() {
		t.Fatalf("ReadEvents = %+v, %v", evs, err)
	}
}

func TestReadEventsMissingFileIsEmpty(t *testing.T) {
	evs, err := ReadEvents(t.TempDir())
	if err != nil || len(evs) != 0 {
		t.Fatalf("got %v, %v", evs, err)
	}
}
```

`internal/bundle/lock_test.go`:

```go
package bundle

import (
	"testing"
	"time"
)

func TestAcquireLifecycle(t *testing.T) {
	ttl := 10 * time.Minute
	t0 := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	s := sample()

	if got := Acquire(s, "A", "host1", t0, ttl, false); got != LockAcquired {
		t.Fatalf("fresh acquire = %s", got)
	}
	if s.Session == nil || s.Session.ID != "A" || !s.Session.Heartbeat.Equal(t0) {
		t.Fatalf("session not recorded: %+v", s.Session)
	}
	t1 := t0.Add(time.Minute)
	if got := Acquire(s, "A", "host1", t1, ttl, false); got != LockHeldBySelf || !s.Session.Heartbeat.Equal(t1) {
		t.Fatalf("re-acquire by self = %s, heartbeat %v", got, s.Session.Heartbeat)
	}
	if got := Acquire(s, "B", "host2", t1.Add(time.Minute), ttl, false); got != LockBlocked || s.Session.ID != "A" {
		t.Fatalf("live other session must block: %s, holder %s", got, s.Session.ID)
	}
	if got := Acquire(s, "B", "host2", t1.Add(time.Minute), ttl, true); got != LockForced || s.Session.ID != "B" {
		t.Fatalf("force must take over: %s, holder %s", got, s.Session.ID)
	}
	stale := t1.Add(time.Minute).Add(ttl).Add(time.Second)
	if got := Acquire(s, "C", "host3", stale, ttl, false); got != LockStolen || s.Session.ID != "C" {
		t.Fatalf("stale lock must be stolen: %s, holder %s", got, s.Session.ID)
	}
	Release(s)
	if s.Session != nil {
		t.Fatal("Release must clear the session")
	}
}
```

- [ ] **Step 5: Run to verify they fail**

Run: `go test ./internal/bundle/ -run 'TestAppend|TestRead|TestAcquire'`
Expected: FAIL — `undefined: AppendEvent`, `Acquire`.

- [ ] **Step 6: Implement events.go and lock.go**

`internal/bundle/events.go`:

```go
package bundle

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Event is one line of events.jsonl (spec §4.4).
type Event struct {
	TS   time.Time      `json:"ts"`
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

// EventsPath returns bundleDir/events.jsonl.
func EventsPath(bundleDir string) string { return filepath.Join(bundleDir, "events.jsonl") }

// nowFunc is a seam for deterministic timestamps in tests.
var nowFunc = func() time.Time { return time.Now().UTC() }

// AppendEvent appends one event with O_APPEND (spec §13).
func AppendEvent(bundleDir, typ string, data map[string]any) error {
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(Event{TS: nowFunc(), Type: typ, Data: data})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(EventsPath(bundleDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// ReadEvents returns every event in order; a missing file is an empty log.
func ReadEvents(bundleDir string) ([]Event, error) {
	f, err := os.Open(EventsPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
```

`internal/bundle/lock.go`:

```go
package bundle

import "time"

// LockOutcome is the result of Acquire.
type LockOutcome string

// Lock outcomes (spec §4.6).
const (
	LockAcquired   LockOutcome = "acquired"     // no holder
	LockHeldBySelf LockOutcome = "held-by-self" // same session; heartbeat refreshed
	LockStolen     LockOutcome = "stolen"       // holder's heartbeat older than ttl
	LockForced     LockOutcome = "forced"       // live holder overridden with force
	LockBlocked    LockOutcome = "blocked"      // live holder; nothing changed
)

// Acquire implements the advisory session lock. It mutates s.Session on
// every outcome except LockBlocked; the caller persists the state.
func Acquire(s *State, id, host string, now time.Time, ttl time.Duration, force bool) LockOutcome {
	take := func(o LockOutcome) LockOutcome {
		s.Session = &Session{ID: id, Host: host, Heartbeat: now}
		return o
	}
	switch {
	case s.Session == nil || s.Session.ID == "":
		return take(LockAcquired)
	case s.Session.ID == id:
		return take(LockHeldBySelf)
	case now.Sub(s.Session.Heartbeat) > ttl:
		return take(LockStolen)
	case force:
		return take(LockForced)
	default:
		return LockBlocked
	}
}

// Release clears the session lock (archive, spec §7.5).
func Release(s *State) { s.Session = nil }
```

- [ ] **Step 7: Run all bundle tests to verify they pass**

Run: `go test ./internal/bundle/ -v`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
golangci-lint run ./...
git add internal/bundle
git commit -m "feat(bundle): state.json schema with atomic save, events.jsonl append/read, advisory session lock"
```

---

### Task 6: plan index — parse, validate, assign waves

**Files:**
- Create: `internal/plan/index.go`, `internal/plan/validate.go`, `internal/plan/waves.go`
- Test: `internal/plan/index_test.go`, `internal/plan/validate_test.go`, `internal/plan/waves_test.go`, `internal/plan/testdata/cedar-like.json`

**Interfaces:**
- Produces (`plan`):
  - `type Task struct { ID int; Title, Description string; Files, Verify []string; DependsOn []int; Goals []string; Class string; Wave *int }` (JSON names `id, title, description, files, verify, depends_on, goals, class, wave`).
  - `type Index struct { Schema int; SpecHash string; Tasks []Task }` (JSON `schema, spec_hash, tasks`); `func ParseIndex(b []byte) (Index, error)` — strict JSON, normalises empty `class` to `"implement"`, sorts tasks by id.
  - `type Problem struct { TaskID int; Field, Message string }` with `String()`; `type ValidateOpts struct { RepoRoot string; MaxFilesPerTask int; GoalIDs []string; SpecHash string; LookPath func(string) bool }`; `func Validate(idx Index, o ValidateOpts) []Problem` — empty slice means valid. `GoalIDs == nil` skips goal checks; `SpecHash == ""` skips the hash check; `LookPath == nil` skips the executable check.
  - `func AssignWaves(idx Index) (map[int]int, error)` — task id → wave; error on a dependency cycle.
  - `func Canonical(idx Index) ([]byte, error)` — deterministic bytes with `wave` stripped, for gate hashing (spec §9); `func (idx Index) Task(id int) *Task`.
  - `const MechanicalMaxFiles = 3`.

- [ ] **Step 1: Write the fixture and the failing parse/wave tests**

`internal/plan/testdata/cedar-like.json` (a realistic 8-task index in the shape of the BitMover cedar plan; `spec_hash` is arbitrary):

```json
{
  "schema": 1,
  "spec_hash": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
  "tasks": [
    { "id": 1, "title": "applicability helper", "description": "Add lib/go/cedar/schema/applicability.go exporting Applicable(action, principal, resource) with tests.",
      "files": ["lib/go/cedar/schema/applicability.go", "lib/go/cedar/schema/applicability_test.go"],
      "verify": ["go test ./lib/go/cedar/schema/..."], "depends_on": [], "goals": ["G1"], "class": "implement" },
    { "id": 2, "title": "schema principals", "description": "Remove CustomerUser from the three IAM actions in policies.cedarschema.",
      "files": ["lib/go/cedar/schema/policies.cedarschema"],
      "verify": ["go test ./lib/go/cedar/schema/..."], "depends_on": [], "goals": ["G3"], "class": "mechanical" },
    { "id": 3, "title": "metrics counters", "description": "Add iam.cedar.policy.skipped and iam.cedar.action.dropped counters.",
      "files": ["services/iam/internal/cedar/metrics.go", "services/iam/internal/cedar/metrics_test.go"],
      "verify": ["go test ./services/iam/internal/cedar/..."], "depends_on": [], "goals": ["G5"], "class": "bounded" },
    { "id": 4, "title": "schema invariants test", "description": "Assert every customer-applicable action has a Customer-descendant resource.",
      "files": ["lib/go/cedar/schema/invariants_test.go"],
      "verify": ["go test ./lib/go/cedar/schema/..."], "depends_on": [1, 2], "goals": ["G4"], "class": "test" },
    { "id": 5, "title": "generator filtering", "description": "Filter granted slugs through the applicability helper; emit no policy when nothing survives.",
      "files": ["services/iam/internal/cedar/generator.go", "services/iam/internal/cedar/generator_test.go"],
      "verify": ["go test ./services/iam/internal/cedar/..."], "depends_on": [1, 3], "goals": ["G1", "G5"], "class": "implement" },
    { "id": 6, "title": "publisher partition", "description": "validatePolicySet partitions; invalid generated permits are skipped, invalid forbids abort.",
      "files": ["services/iam/internal/cedar/policy_publisher.go", "services/iam/internal/cedar/policy_publisher_test.go"],
      "verify": ["go test ./services/iam/internal/cedar/..."], "depends_on": [3, 5], "goals": ["G2"], "class": "implement" },
    { "id": 7, "title": "dashboard panels", "description": "Add panels for the new counters and regenerate the JSON.",
      "files": ["tools/dashboards/cedar.go", "tools/development-docker/grafana/provisioning/dashboards/cedar-authorization.json"],
      "verify": ["go run ./tools/dashboards -check"], "depends_on": [3], "goals": ["G5"], "class": "bounded" },
    { "id": 8, "title": "ADR", "description": "Record the two-sided applicability rule and the permit/forbid asymmetry.",
      "files": ["documentation/decisions/2026-08-23-cedar-policy-applicability.md"],
      "verify": ["true"], "depends_on": [], "goals": ["G6"], "class": "docs" }
  ]
}
```

`internal/plan/index_test.go`:

```go
package plan

import (
	"os"
	"testing"
)

func loadFixture(t *testing.T) Index {
	t.Helper()
	b, err := os.ReadFile("testdata/cedar-like.json")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ParseIndex(b)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestParseIndexFixture(t *testing.T) {
	idx := loadFixture(t)
	if idx.Schema != 1 || len(idx.Tasks) != 8 || idx.Tasks[0].ID != 1 || idx.Tasks[7].Class != "docs" {
		t.Fatalf("parsed = %+v", idx)
	}
	if idx.Task(5) == nil || idx.Task(5).DependsOn[1] != 3 {
		t.Fatal("Task(5)")
	}
}

func TestParseIndexNormalisesClassAndSorts(t *testing.T) {
	idx, err := ParseIndex([]byte(`{"schema":1,"spec_hash":"x","tasks":[
	  {"id":2,"title":"b","description":"d","files":["b.go"],"verify":["true"]},
	  {"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Tasks[0].ID != 1 || idx.Tasks[0].Class != "implement" || idx.Tasks[1].Class != "implement" {
		t.Fatalf("%+v", idx.Tasks)
	}
}

func TestParseIndexRejectsGarbage(t *testing.T) {
	if _, err := ParseIndex([]byte(`{"schema":1,"tasks":[{"id":"one"}]}`)); err == nil {
		t.Fatal("string id must fail")
	}
	if _, err := ParseIndex([]byte(`not json`)); err == nil {
		t.Fatal("non-JSON must fail")
	}
}

func TestCanonicalStripsWaveAndIsStable(t *testing.T) {
	idx := loadFixture(t)
	a, _ := Canonical(idx)
	w := 3
	idx.Tasks[0].Wave = &w
	b, _ := Canonical(idx)
	if string(a) != string(b) {
		t.Fatal("wave must not affect the canonical bytes")
	}
}
```

`internal/plan/waves_test.go`:

```go
package plan

import "testing"

func TestAssignWavesFixture(t *testing.T) {
	waves, err := AssignWaves(loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]int{1: 0, 2: 0, 3: 0, 8: 0, 4: 1, 5: 1, 7: 1, 6: 2}
	for id, w := range want {
		if waves[id] != w {
			t.Errorf("task %d wave = %d, want %d", id, waves[id], w)
		}
	}
}

func TestAssignWavesCycle(t *testing.T) {
	idx := Index{Schema: 1, Tasks: []Task{
		{ID: 1, DependsOn: []int{2}}, {ID: 2, DependsOn: []int{3}}, {ID: 3, DependsOn: []int{1}}, {ID: 4},
	}}
	if _, err := AssignWaves(idx); err == nil {
		t.Fatal("cycle must be reported")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/plan/`
Expected: FAIL — `undefined: ParseIndex`, `AssignWaves`.

- [ ] **Step 3: Implement index.go and waves.go**

`internal/plan/index.go`:

```go
// Package plan defines plan.index.json (spec §7.3): the task schema the
// planner writes, its validation rules, and deterministic wave assignment.
package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Task is one planned unit of work.
type Task struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
	Verify      []string `json:"verify"`
	DependsOn   []int    `json:"depends_on"`
	Goals       []string `json:"goals"`
	Class       string   `json:"class"`
	Wave        *int     `json:"wave,omitempty"` // display only; takt assigns it
}

// Index is the whole plan.index.json.
type Index struct {
	Schema   int    `json:"schema"`
	SpecHash string `json:"spec_hash"`
	Tasks    []Task `json:"tasks"`
}

// DefaultClass is assumed when a task omits class.
const DefaultClass = "implement"

// ParseIndex decodes strictly, defaults class, and sorts tasks by id.
func ParseIndex(b []byte) (Index, error) {
	var idx Index
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&idx); err != nil {
		return Index{}, fmt.Errorf("plan.index.json: %w", err)
	}
	for i := range idx.Tasks {
		if idx.Tasks[i].Class == "" {
			idx.Tasks[i].Class = DefaultClass
		}
		if idx.Tasks[i].DependsOn == nil {
			idx.Tasks[i].DependsOn = []int{}
		}
		if idx.Tasks[i].Goals == nil {
			idx.Tasks[i].Goals = []string{}
		}
	}
	sort.SliceStable(idx.Tasks, func(i, j int) bool { return idx.Tasks[i].ID < idx.Tasks[j].ID })
	return idx, nil
}

// Task returns the task with id, or nil.
func (idx Index) Task(id int) *Task {
	for i := range idx.Tasks {
		if idx.Tasks[i].ID == id {
			return &idx.Tasks[i]
		}
	}
	return nil
}

// Canonical returns stable bytes with the display-only wave removed (spec §9).
func Canonical(idx Index) ([]byte, error) {
	c := Index{Schema: idx.Schema, SpecHash: idx.SpecHash, Tasks: make([]Task, len(idx.Tasks))}
	copy(c.Tasks, idx.Tasks)
	sort.SliceStable(c.Tasks, func(i, j int) bool { return c.Tasks[i].ID < c.Tasks[j].ID })
	for i := range c.Tasks {
		c.Tasks[i].Wave = nil
	}
	return json.Marshal(c)
}
```

`internal/plan/waves.go`:

```go
package plan

import (
	"fmt"
	"sort"
)

// AssignWaves computes wave(t) = 0 for tasks without dependencies, else
// 1 + max(wave(dep)) — Kahn's algorithm over depends_on (spec §7.3).
// Returns an error naming the tasks left in a cycle.
func AssignWaves(idx Index) (map[int]int, error) {
	indeg := map[int]int{}
	children := map[int][]int{}
	for _, t := range idx.Tasks {
		indeg[t.ID] += 0
		for _, d := range t.DependsOn {
			indeg[t.ID]++
			children[d] = append(children[d], t.ID)
		}
	}
	waves := map[int]int{}
	var queue []int
	for _, t := range idx.Tasks {
		if indeg[t.ID] == 0 {
			queue = append(queue, t.ID)
			waves[t.ID] = 0
		}
	}
	sort.Ints(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, c := range children[id] {
			if waves[id]+1 > waves[c] {
				waves[c] = waves[id] + 1
			}
			indeg[c]--
			if indeg[c] == 0 {
				queue = append(queue, c)
			}
		}
	}
	if len(waves) != len(idx.Tasks) {
		var stuck []int
		for _, t := range idx.Tasks {
			if _, ok := waves[t.ID]; !ok {
				stuck = append(stuck, t.ID)
			}
		}
		sort.Ints(stuck)
		return nil, fmt.Errorf("dependency cycle among tasks %v", stuck)
	}
	return waves, nil
}
```

Run: `go test ./internal/plan/ -run 'TestParse|TestCanonical|TestAssign' -v` — Expected: PASS.

- [ ] **Step 4: Write the failing validation tests**

`internal/plan/validate_test.go`:

```go
package plan

import (
	"strings"
	"testing"
)

func opts(t *testing.T) ValidateOpts {
	t.Helper()
	return ValidateOpts{
		RepoRoot:        t.TempDir(),
		MaxFilesPerTask: 12,
		GoalIDs:         []string{"G1", "G2", "G3", "G4", "G5", "G6"},
		SpecHash:        "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		LookPath:        func(tok string) bool { return tok == "go" || tok == "true" },
	}
}

func hasProblem(ps []Problem, taskID int, substr string) bool {
	for _, p := range ps {
		if p.TaskID == taskID && strings.Contains(p.Message, substr) {
			return true
		}
	}
	return false
}

func TestFixtureIsValid(t *testing.T) {
	if ps := Validate(loadFixture(t), opts(t)); len(ps) != 0 {
		t.Fatalf("expected no problems, got %v", ps)
	}
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Index)
		taskID int
		want   string
	}{
		{"schema", func(i *Index) { i.Schema = 2 }, 0, "schema"},
		{"ids not 1..n", func(i *Index) { i.Tasks[7].ID = 42 }, 0, "1..n"},
		{"empty title", func(i *Index) { i.Tasks[0].Title = "" }, 1, "title"},
		{"no files", func(i *Index) { i.Tasks[0].Files = nil }, 1, "files"},
		{"absolute file", func(i *Index) { i.Tasks[0].Files = []string{"/etc/passwd"} }, 1, "absolute"},
		{"escaping file", func(i *Index) { i.Tasks[0].Files = []string{"../x.go"} }, 1, "escapes"},
		{"too many files", func(i *Index) {
			i.Tasks[0].Files = make([]string, 13)
			for k := range i.Tasks[0].Files {
				i.Tasks[0].Files[k] = "f" + string(rune('a'+k)) + ".go"
			}
		}, 1, "at most 12"},
		{"mechanical too big", func(i *Index) { i.Tasks[1].Files = []string{"a", "b", "c", "d"} }, 2, "mechanical"},
		{"no verify", func(i *Index) { i.Tasks[0].Verify = nil }, 1, "verify"},
		{"verify not on PATH", func(i *Index) { i.Tasks[0].Verify = []string{"frobnicate --all"} }, 1, "not found on PATH"},
		{"unknown dep", func(i *Index) { i.Tasks[0].DependsOn = []int{99} }, 1, "unknown task 99"},
		{"self dep", func(i *Index) { i.Tasks[0].DependsOn = []int{1} }, 1, "itself"},
		{"cycle", func(i *Index) { i.Tasks[0].DependsOn = []int{6} }, 0, "cycle"},
		{"overlap without order", func(i *Index) { i.Tasks[7].Files = []string{"lib/go/cedar/schema/applicability.go"} }, 8, "share"},
		{"unknown goal", func(i *Index) { i.Tasks[0].Goals = []string{"G9"} }, 1, "unknown goal"},
		{"goal unserved", func(i *Index) { i.Tasks[7].Goals = []string{"G1"} }, 0, "G6"},
		{"unknown class", func(i *Index) { i.Tasks[0].Class = "magic" }, 1, "class"},
		{"stale spec hash", func(i *Index) { i.SpecHash = "sha256:ffff" }, 0, "spec_hash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx := loadFixture(t)
			c.mutate(&idx)
			ps := Validate(idx, opts(t))
			if !hasProblem(ps, c.taskID, c.want) {
				t.Fatalf("want problem on task %d containing %q, got %v", c.taskID, c.want, ps)
			}
		})
	}
}

func TestOverlapWithTransitiveOrderIsFine(t *testing.T) {
	idx := loadFixture(t)
	// task 6 depends on 5 which depends on 1; sharing a file between 6 and 1 is ordered.
	idx.Tasks[5].Files = append(idx.Tasks[5].Files, "lib/go/cedar/schema/applicability.go")
	if ps := Validate(idx, opts(t)); len(ps) != 0 {
		t.Fatalf("transitively ordered overlap must be accepted: %v", ps)
	}
}

func TestValidateSkipsOptionalChecks(t *testing.T) {
	idx := loadFixture(t)
	idx.SpecHash = "whatever"
	idx.Tasks[0].Goals = []string{"G9"}
	idx.Tasks[0].Verify = []string{"frobnicate"}
	if ps := Validate(idx, ValidateOpts{RepoRoot: t.TempDir(), MaxFilesPerTask: 12}); len(ps) != 0 {
		t.Fatalf("nil GoalIDs/LookPath and empty SpecHash must skip those checks: %v", ps)
	}
}
```

- [ ] **Step 5: Run to verify they fail**

Run: `go test ./internal/plan/ -run TestValidate`
Expected: FAIL — `undefined: Validate`.

- [ ] **Step 6: Implement validate.go**

```go
package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
)

// MechanicalMaxFiles caps a `mechanical` task (spec §7.3).
const MechanicalMaxFiles = 3

// Problem is one validation failure. TaskID 0 means the whole index.
type Problem struct {
	TaskID  int
	Field   string
	Message string
}

func (p Problem) String() string {
	if p.TaskID == 0 {
		return p.Field + ": " + p.Message
	}
	return fmt.Sprintf("task %d %s: %s", p.TaskID, p.Field, p.Message)
}

// ValidateOpts carries the context validation needs; nil/empty members skip
// the corresponding optional check.
type ValidateOpts struct {
	RepoRoot        string
	MaxFilesPerTask int
	GoalIDs         []string          // nil → skip goal checks
	SpecHash        string            // "" → skip
	LookPath        func(string) bool // nil → skip
}

// Validate applies every rule in spec §7.3 and returns all problems found.
func Validate(idx Index, o ValidateOpts) []Problem {
	var ps []Problem
	add := func(id int, field, msg string) { ps = append(ps, Problem{TaskID: id, Field: field, Message: msg}) }

	if idx.Schema != 1 {
		add(0, "schema", fmt.Sprintf("unsupported schema %d (want 1)", idx.Schema))
	}
	if o.SpecHash != "" && idx.SpecHash != o.SpecHash {
		add(0, "spec_hash", "spec_hash does not match the current spec.md — the plan was drafted against an older spec")
	}
	ids := map[int]bool{}
	for i, t := range idx.Tasks {
		if t.ID != i+1 {
			add(0, "tasks", "ids must be exactly 1..n in order")
			break
		}
		ids[t.ID] = true
	}
	for _, t := range idx.Tasks {
		if strings.TrimSpace(t.Title) == "" {
			add(t.ID, "title", "title is empty")
		}
		if strings.TrimSpace(t.Description) == "" {
			add(t.ID, "description", "description is empty")
		}
		if !config.IsTaskClass(t.Class) {
			add(t.ID, "class", fmt.Sprintf("unknown class %q (want one of %s)", t.Class, strings.Join(config.TaskClasses, "|")))
		}
		if len(t.Files) == 0 {
			add(t.ID, "files", "files is empty — every task declares the files it may change")
		}
		for _, f := range t.Files {
			if err := bundle.CheckRelPath(o.RepoRoot, f); err != nil {
				add(t.ID, "files", err.Error())
			}
		}
		if o.MaxFilesPerTask > 0 && len(t.Files) > o.MaxFilesPerTask {
			add(t.ID, "files", fmt.Sprintf("%d files; at most %d per task — split the task", len(t.Files), o.MaxFilesPerTask))
		}
		if t.Class == "mechanical" && len(t.Files) > MechanicalMaxFiles {
			add(t.ID, "files", fmt.Sprintf("a mechanical task may touch at most %d files", MechanicalMaxFiles))
		}
		if len(t.Verify) == 0 {
			add(t.ID, "verify", "verify is empty — every task must prove itself")
		}
		for _, v := range t.Verify {
			tok := strings.Fields(v)
			if len(tok) == 0 {
				add(t.ID, "verify", "blank command")
				continue
			}
			if o.LookPath != nil && !o.LookPath(tok[0]) {
				add(t.ID, "verify", fmt.Sprintf("%q not found on PATH", tok[0]))
			}
		}
		for _, d := range t.DependsOn {
			if d == t.ID {
				add(t.ID, "depends_on", "a task cannot depend on itself")
			} else if !ids[d] {
				add(t.ID, "depends_on", fmt.Sprintf("unknown task %d", d))
			}
		}
		if o.GoalIDs != nil {
			for _, g := range t.Goals {
				if !contains(o.GoalIDs, g) {
					add(t.ID, "goals", "unknown goal "+g)
				}
			}
		}
	}
	if _, err := AssignWaves(idx); err != nil {
		add(0, "depends_on", err.Error())
		return ps // reachability below assumes a DAG
	}
	// Shared files must be ordered (transitively) by depends_on.
	reach := reachability(idx)
	byFile := map[string][]int{}
	for _, t := range idx.Tasks {
		for _, f := range t.Files {
			byFile[f] = append(byFile[f], t.ID)
		}
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		owners := byFile[f]
		for i := 0; i < len(owners); i++ {
			for j := i + 1; j < len(owners); j++ {
				a, b := owners[i], owners[j]
				if !reach[a][b] && !reach[b][a] {
					add(b, "files", fmt.Sprintf("tasks %d and %d share %s but neither depends on the other — add depends_on", a, b, f))
				}
			}
		}
	}
	if o.GoalIDs != nil {
		served := map[string]bool{}
		for _, t := range idx.Tasks {
			for _, g := range t.Goals {
				served[g] = true
			}
		}
		for _, g := range o.GoalIDs {
			if !served[g] {
				add(0, "goals", "goal "+g+" is served by no task")
			}
		}
	}
	return ps
}

// reachability returns reach[a][b] == true when b transitively depends on a.
func reachability(idx Index) map[int]map[int]bool {
	children := map[int][]int{}
	for _, t := range idx.Tasks {
		for _, d := range t.DependsOn {
			children[d] = append(children[d], t.ID)
		}
	}
	reach := map[int]map[int]bool{}
	for _, t := range idx.Tasks {
		reach[t.ID] = map[int]bool{}
		stack := append([]int{}, children[t.ID]...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if reach[t.ID][n] {
				continue
			}
			reach[t.ID][n] = true
			stack = append(stack, children[n]...)
		}
	}
	return reach
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 7: Run all plan tests to verify they pass**

Run: `go test ./internal/plan/ -v`
Expected: all PASS, including every sub-test of `TestValidateRules`.

- [ ] **Step 8: Commit**

```bash
golangci-lint run ./...
git add internal/plan
git commit -m "feat(plan): index schema, full validation (paths, classes, verify, deps, shared-file ordering, goals), Kahn wave assignment"
```

---

### Task 7: goals.md — parse and hash

**Files:**
- Create: `internal/goals/goals.go`
- Test: `internal/goals/goals_test.go`

**Interfaces:**
- Produces (`goals`): `type Goal struct { ID, Text, Signal, Evidence string }`; `type Goals struct { Anchor string; Items []Goal }`; `func Parse(b []byte) (Goals, error)`; `func Hash(b []byte) string` (`"sha256:" + hex`); `func (g Goals) IDs() []string`; `var Signals = []string{"test", "command", "artifact", "docs"}`.
- Format parsed (spec §7.2): a `## Anchor` section holding one fenced block whose body is the verbatim topic, then a `## Goals` section of lines `- G<n> — <text> · signal: <signal> · evidence: <text>`.

- [ ] **Step 1: Write the failing tests**

`internal/goals/goals_test.go`:

```go
package goals

import (
	"strings"
	"testing"
)

const sample = "# Goals — demo\n\n## Anchor\n```text\nfull https://github.com/x/y/issues/7 — make it\nwork across two lines\n```\n\n## Goals\n- G1 — Policy generation yields a schema-valid set · signal: test · evidence: go test ./lib/... passes\n- G2 — The decision is recorded · signal: docs · evidence: an ADR under documentation/decisions/\n"

func TestParseSample(t *testing.T) {
	g, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if g.Anchor != "full https://github.com/x/y/issues/7 — make it\nwork across two lines" {
		t.Fatalf("anchor = %q", g.Anchor)
	}
	if len(g.Items) != 2 || g.Items[0].ID != "G1" || g.Items[0].Signal != "test" || !strings.HasPrefix(g.Items[1].Evidence, "an ADR") {
		t.Fatalf("items = %+v", g.Items)
	}
	if ids := g.IDs(); len(ids) != 2 || ids[1] != "G2" {
		t.Fatalf("IDs = %v", ids)
	}
}

func TestParseRejects(t *testing.T) {
	bad := map[string]string{
		"no anchor":     strings.Replace(sample, "## Anchor", "## Intro", 1),
		"no goals":      sample[:strings.Index(sample, "## Goals")],
		"bad signal":    strings.Replace(sample, "signal: test", "signal: vibes", 1),
		"duplicate id":  strings.Replace(sample, "- G2 —", "- G1 —", 1),
		"missing evid.": strings.Replace(sample, " · evidence: go test ./lib/... passes", "", 1),
		"gap in ids":    strings.Replace(sample, "- G2 —", "- G3 —", 1),
	}
	for name, in := range bad {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestHashIsStableAndPrefixed(t *testing.T) {
	a, b := Hash([]byte(sample)), Hash([]byte(sample))
	if a != b || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("hash = %q", a)
	}
	if Hash([]byte(sample+"x")) == a {
		t.Fatal("different bytes must hash differently")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/goals/`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 3: Implement**

`internal/goals/goals.go`:

```go
// Package goals parses goals.md (spec §7.2): the verbatim anchor plus the
// frozen list of success criteria the finish-time assessor checks.
package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Goal is one success criterion.
type Goal struct {
	ID       string
	Text     string
	Signal   string
	Evidence string
}

// Goals is the parsed file.
type Goals struct {
	Anchor string
	Items  []Goal
}

// Signals is the closed set of evidence kinds.
var Signals = []string{"test", "command", "artifact", "docs"}

var goalLine = regexp.MustCompile(`^- (G\d+) — (.+?) · signal: (\w+) · evidence: (.+)$`)

// Hash returns "sha256:<hex>" over the raw bytes.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IDs returns the goal ids in file order.
func (g Goals) IDs() []string {
	ids := make([]string, len(g.Items))
	for i, it := range g.Items {
		ids[i] = it.ID
	}
	return ids
}

// Parse reads the anchor block and the goal list, enforcing ids G1..Gn.
func Parse(b []byte) (Goals, error) {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var g Goals
	section := ""
	inFence, anchorSeen := false, false
	var anchor []string
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "## "):
			section = strings.TrimSpace(strings.TrimPrefix(ln, "## "))
			continue
		case section == "Anchor" && strings.HasPrefix(ln, "```"):
			if inFence {
				inFence, anchorSeen = false, true
			} else if !anchorSeen {
				inFence = true
			}
			continue
		case section == "Anchor" && inFence:
			anchor = append(anchor, ln)
		case section == "Goals" && strings.HasPrefix(ln, "- "):
			m := goalLine.FindStringSubmatch(ln)
			if m == nil {
				return Goals{}, fmt.Errorf("goals.md: malformed goal line %q (want `- G<n> — text · signal: <s> · evidence: <e>`)", ln)
			}
			sig := m[3]
			if !contains(Signals, sig) {
				return Goals{}, fmt.Errorf("goals.md: %s has unknown signal %q", m[1], sig)
			}
			g.Items = append(g.Items, Goal{ID: m[1], Text: m[2], Signal: sig, Evidence: m[4]})
		}
	}
	if !anchorSeen {
		return Goals{}, errors.New("goals.md: missing `## Anchor` with a fenced verbatim block")
	}
	g.Anchor = strings.Join(anchor, "\n")
	if len(g.Items) == 0 {
		return Goals{}, errors.New("goals.md: no goals under `## Goals`")
	}
	for i, it := range g.Items {
		if it.ID != "G"+strconv.Itoa(i+1) {
			return Goals{}, fmt.Errorf("goals.md: ids must be G1..Gn in order; found %s at position %d", it.ID, i+1)
		}
	}
	return g, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/goals/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
golangci-lint run ./...
git add internal/goals
git commit -m "feat(goals): parse goals.md (verbatim anchor + G1..Gn list) and hash it"
```

---

### Task 8: `takt init` and the shared workspace resolver

**Files:**
- Create: `internal/cli/workspace.go`, `internal/cli/slug.go`, `internal/cli/cmd_init.go`
- Modify: `internal/cli/cli.go` (register `init`)
- Test: `internal/cli/slug_test.go`, `internal/cli/cmd_init_test.go`

**Interfaces:**
- Produces (`cli`, unexported, used by every later command):
  - `type workspace struct { Repo *gitx.Repo; Cfg config.Config; Dir bundle.Dir; Home string }`; `func openWorkspace(ctx context.Context, env Env, dirFlag string) (*workspace, error)` — `gitx.Open(cwd)` → `config.Load` → `bundle.ResolveDir(repoRoot, home, dirFlag, getenv("TAKT_DIR"), cfg.Dir)`. `Home` comes from `getenv("HOME")`.
  - `func sessionID(getenv func(string) string) string` — `CLAUDE_CODE_SESSION_ID`, else `TAKT_SESSION`, else a fresh random hex string.
  - `func deriveSlug(topic string) string`.
- Produces: the `init` command (spec §7.1) and its stdout shape `{"slug","bundle","branch","branch_adopted","base","base_sha","committed"}`.

- [ ] **Step 1: Write the failing slug tests**

`internal/cli/slug_test.go`:

```go
package cli

import "testing"

func TestDeriveSlug(t *testing.T) {
	cases := map[string]string{
		"full https://github.com/bit-mover/BitMover/issues/2154 — Cedar generator can emit": "issue-2154",
		"Add pluralization rule for UpDownCounter names (issue #29)":                          "add-pluralization-rule-for-updowncounter-names",
		"  Fix   the THING!!  ":                                                              "fix-the-thing",
		"":                                                                                   "run",
	}
	for in, want := range cases {
		if got := deriveSlug(in); got != want {
			t.Errorf("deriveSlug(%q) = %q, want %q", in, got, want)
		}
	}
	long := deriveSlug("one two three four five six seven eight nine ten")
	if long != "one-two-three-four-five-six" {
		t.Errorf("first six words: %q", long)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cli/ -run TestDeriveSlug`
Expected: FAIL — `undefined: deriveSlug`.

- [ ] **Step 3: Implement slug.go and workspace.go**

`internal/cli/slug.go`:

```go
package cli

import (
	"regexp"
	"strings"
)

var issueRe = regexp.MustCompile(`/issues/(\d+)`)
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// deriveSlug implements spec §18: issue-<n> for issue links, else the kebab
// case of the first six words; "run" when nothing usable remains.
func deriveSlug(topic string) string {
	if m := issueRe.FindStringSubmatch(topic); m != nil {
		return "issue-" + m[1]
	}
	words := strings.Fields(strings.ToLower(topic))
	if len(words) > 6 {
		words = words[:6]
	}
	s := nonSlug.ReplaceAllString(strings.Join(words, "-"), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "run"
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}
```

`internal/cli/workspace.go`:

```go
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/gitx"
)

// workspace is everything a command needs about where it runs.
type workspace struct {
	Repo *gitx.Repo
	Cfg  config.Config
	Dir  bundle.Dir
	Home string
}

// openWorkspace resolves repo, config and bundle dir from the cwd (spec §4.1).
func openWorkspace(ctx context.Context, env Env, dirFlag string) (*workspace, error) {
	repo, err := gitx.Open(ctx, env.Cwd)
	if err != nil {
		return nil, err
	}
	home := env.Getenv("HOME")
	cfg, _, err := config.Load(repo.Root, home, env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	dir, err := bundle.ResolveDir(repo.Root, home, dirFlag, env.Getenv("TAKT_DIR"), cfg.Dir)
	if err != nil {
		return nil, err
	}
	return &workspace{Repo: repo, Cfg: cfg, Dir: dir, Home: home}, nil
}

// addDirFlag registers the --dir flag every command accepts.
func addDirFlag(fs *flag.FlagSet) *string {
	return fs.String("dir", "", "bundle directory (overrides TAKT_DIR and .takt.json)")
}

// sessionID identifies the driving session for the advisory lock (spec §4.6).
func sessionID(getenv func(string) string) string {
	if s := getenv("CLAUDE_CODE_SESSION_ID"); s != "" {
		return s
	}
	if s := getenv("TAKT_SESSION"); s != "" {
		return s
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "takt-" + hex.EncodeToString(b[:])
}
```

Run: `go test ./internal/cli/ -run TestDeriveSlug` — Expected: PASS.

- [ ] **Step 4: Write the failing init tests**

`internal/cli/cmd_init_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/testutil"
)

// runIn runs takt with cwd=dir and a controlled environment.
func runIn(t *testing.T, dir string, env map[string]string, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		if k == "HOME" {
			return filepath.Join(dir, ".home")
		}
		return env[k]
	}
	code := Main(args, &out, &errb, getenv, dir)
	var got map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON: %q", out.String())
		}
	}
	return code, got, errb.String()
}

func TestInitOnDefaultBranchCreatesRunBranch(t *testing.T) {
	root := testutil.NewRepo(t)
	code, got, errb := runIn(t, root, nil, "init", "Add", "a", "greeting")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["slug"] != "add-a-greeting" || got["branch"] != "takt/add-a-greeting" || got["branch_adopted"] != false || got["base"] != "main" {
		t.Fatalf("out = %v", got)
	}
	if b := testutil.Git(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "takt/add-a-greeting" {
		t.Fatalf("branch = %s", b)
	}
	st, err := bundle.LoadState(filepath.Join(root, "docs", "takt", "add-a-greeting"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseBrainstorm || st.Topic != "Add a greeting" || st.Config.MaxParallel != 8 || st.Session == nil {
		t.Fatalf("state = %+v", st)
	}
	if got["committed"] != true {
		t.Fatal("in-repo bundle must be committed")
	}
	if msg := testutil.Git(t, root, "log", "-1", "--format=%s"); msg != "takt(add-a-greeting): init" {
		t.Fatalf("commit message = %q", msg)
	}
	if clean := testutil.Git(t, root, "status", "--porcelain"); clean != "" {
		t.Fatalf("worktree not clean after init: %q", clean)
	}
}

func TestInitOnFeatureBranchAdopts(t *testing.T) {
	root := testutil.NewRepo(t)
	testutil.Git(t, root, "checkout", "-q", "-b", "monrad/2166")
	base := testutil.Git(t, root, "rev-parse", "HEAD")
	testutil.WriteFile(t, root, "f.txt", "x\n")
	testutil.Commit(t, root, "feature work")
	code, got, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["branch"] != "monrad/2166" || got["branch_adopted"] != true || got["base_sha"] != base {
		t.Fatalf("out = %v (base_sha must be the merge-base with main)", got)
	}
}

func TestInitRefusals(t *testing.T) {
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, "staged.txt", "x\n")
	testutil.Git(t, root, "add", "staged.txt")
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "t"); code != 1 || !strings.Contains(errb, "staged") {
		t.Fatalf("staged changes must refuse: %d %s", code, errb)
	}
	testutil.Git(t, root, "reset", "-q")
	os.Remove(filepath.Join(root, "staged.txt"))
	if code, _, _ := runIn(t, root, nil, "init", "--slug", "demo", "t"); code != 0 {
		t.Fatal("first init should succeed")
	}
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "t"); code != 1 || !strings.Contains(errb, "exists") {
		t.Fatalf("existing slug must refuse: %d %s", code, errb)
	}
	if code, _, errb := runIn(t, t.TempDir(), nil, "init", "--slug", "x", "t"); code != 1 || !strings.Contains(errb, "git repository") {
		t.Fatalf("outside a repo must refuse: %d %s", code, errb)
	}
}

func TestInitExternalDirIsNotCommitted(t *testing.T) {
	root := testutil.NewRepo(t)
	ext := t.TempDir()
	code, got, errb := runIn(t, root, map[string]string{"TAKT_DIR": ext}, "init", "--slug", "demo", "topic")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	want := filepath.Join(ext, filepath.Base(root), "demo")
	if got["bundle"] != want || got["committed"] != false {
		t.Fatalf("out = %v, want bundle %q uncommitted", got, want)
	}
	if n := testutil.Git(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("external bundle must not create a commit; count = %s", n)
	}
}

func TestInitFlagsFreezeConfig(t *testing.T) {
	root := testutil.NewRepo(t)
	code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "--autonomy", "step", "--no-review-tasks", "--no-goals", "topic")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	st, _ := bundle.LoadState(filepath.Join(root, "docs", "takt", "demo"))
	if st.Config.Autonomy != "step" || st.Config.Review.Tasks || st.Config.Goals || !st.Config.Review.Spec {
		t.Fatalf("frozen config = %+v", st.Config)
	}
}
```

- [ ] **Step 5: Run to verify they fail**

Run: `go test ./internal/cli/ -run TestInit`
Expected: FAIL — exit 2 `unknown command: init`.

- [ ] **Step 6: Implement cmd_init.go and register it**

`internal/cli/cmd_init.go`:

```go
package cli

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/version"
)

func cmdInit(env Env) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	dirFlag := addDirFlag(fs)
	slug := fs.String("slug", "", "bundle slug (default: derived from the topic)")
	autonomy := fs.String("autonomy", "", "auto|step (default from config)")
	noSpec := fs.Bool("no-review-spec", false, "disable the spec review gate for this run")
	noPlan := fs.Bool("no-review-plan", false, "disable the plan review gate for this run")
	noTasks := fs.Bool("no-review-tasks", false, "disable per-task review for this run")
	noGoals := fs.Bool("no-goals", false, "disable goals for this run")
	noAlign := fs.Bool("no-alignment", false, "disable the alignment audit for this run")
	if err := fs.Parse(env.Args); err != nil {
		return 2
	}
	topic := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if topic == "" {
		return fail(env.Stderr, 2, "init needs a topic", "takt init \"<what you want built>\"")
	}
	ctx := context.Background()
	ws, err := openWorkspace(ctx, env, *dirFlag)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "run takt inside a git repository")
	}
	if *slug == "" {
		*slug = deriveSlug(topic)
	}
	bdir := ws.Dir.Bundle(*slug)
	if _, err := os.Stat(bundle.StatePath(bdir)); err == nil {
		return fail(env.Stderr, 1, "bundle "+*slug+" already exists at "+bdir, "pick another --slug or resume it with `takt next`")
	}
	if staged, err := ws.Repo.HasStaged(ctx); err != nil || staged {
		return fail(env.Stderr, 1, "the index has staged changes", "commit or unstage them first; takt init must start from a clean index")
	}

	// Branch rule (spec D9).
	cur, err := ws.Repo.CurrentBranch(ctx)
	if err != nil || cur == "HEAD" {
		return fail(env.Stderr, 1, "cannot init on a detached HEAD", "check out a branch first")
	}
	def, err := ws.Repo.DefaultBranch(ctx, ws.Cfg.DefaultBranch)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "set default_branch in .takt.json")
	}
	branch, adopted := cur, true
	if cur == def {
		branch, adopted = "takt/"+*slug, false
		if exists, _ := ws.Repo.BranchExists(ctx, branch); exists {
			return fail(env.Stderr, 1, "branch "+branch+" already exists", "delete it or choose another --slug")
		}
		if err := ws.Repo.CreateAndCheckout(ctx, branch); err != nil {
			return fail(env.Stderr, 1, err.Error(), "")
		}
	}
	head, err := ws.Repo.HeadSHA(ctx)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	baseSHA := head
	if adopted {
		if mb, err := ws.Repo.MergeBase(ctx, def, "HEAD"); err == nil {
			baseSHA = mb
		}
	}

	cfg := ws.Cfg
	if *autonomy != "" {
		cfg.Autonomy = *autonomy
	}
	if err := cfg.Validate(); err != nil {
		return fail(env.Stderr, 2, err.Error(), "")
	}
	now := time.Now().UTC()
	host, _ := os.Hostname()
	st := &bundle.State{
		Schema: bundle.SchemaVersion, TaktVersion: version.Version,
		Slug: *slug, Topic: topic, Phase: bundle.PhaseBrainstorm, CreatedAt: now,
		Branch: branch, BranchAdopted: adopted, Base: def, BaseSHA: baseSHA,
		Config: bundle.RunConfig{
			Autonomy:    cfg.Autonomy,
			Review:      bundle.ReviewConfig{Spec: cfg.Review.Spec && !*noSpec, Plan: cfg.Review.Plan && !*noPlan, Tasks: cfg.Review.Tasks && !*noTasks},
			Goals:       cfg.Goals && !*noGoals,
			Alignment:   cfg.Alignment && !*noAlign,
			MaxParallel: cfg.MaxParallel,
			MaxRework:   cfg.MaxRework,
		},
		Gates:   map[string]string{"spec": "pending", "plan": "pending"},
		Tasks:   []bundle.Task{},
		Session: &bundle.Session{ID: sessionID(env.Getenv), Host: host, Heartbeat: now},
	}
	if err := bundle.SaveState(bdir, st); err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	if err := bundle.AppendEvent(bdir, "init", map[string]any{"slug": *slug, "branch": branch, "branch_adopted": adopted, "base": def, "base_sha": baseSHA}); err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}

	committed := false
	bundleOut := bdir
	if ws.Dir.InRepo {
		rel, err := ws.Dir.RelToRepo(bdir)
		if err != nil {
			return fail(env.Stderr, 1, err.Error(), "")
		}
		bundleOut = rel
		if err := ws.Repo.Add(ctx, rel); err != nil {
			return fail(env.Stderr, 1, err.Error(), "")
		}
		if _, err := ws.Repo.Commit(ctx, "takt("+*slug+"): init"); err != nil {
			return fail(env.Stderr, 1, err.Error(), "")
		}
		committed = true
	}
	if err := writeJSON(env.Stdout, map[string]any{
		"slug": *slug, "bundle": bundleOut, "branch": branch, "branch_adopted": adopted,
		"base": def, "base_sha": baseSHA, "committed": committed,
	}); err != nil {
		return 1
	}
	return 0
}
```

Register it in `internal/cli/cli.go`:

```go
var commands = map[string]command{
	"version": cmdVersion,
	"init":    cmdInit,
}
```


- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -v`
Expected: all PASS. Common failures: `TestInitOnFeatureBranchAdopts` — `base_sha` must be the merge-base (the commit *before* "feature work"), not HEAD; `TestInitOnDefaultBranchCreatesRunBranch` "worktree not clean" — the commit must stage only the bundle directory.

- [ ] **Step 8: Commit**

```bash
golangci-lint run ./...
git add internal/cli
git commit -m "feat(cli): takt init — slug derivation, branch rule, frozen run config, bundle commit"
```

---

### Task 9: `takt status` and `takt plan validate`

**Files:**
- Create: `internal/cli/select.go`, `internal/cli/cmd_status.go`, `internal/cli/cmd_plan.go`
- Modify: `internal/cli/cli.go` (register `status`, `plan`)
- Test: `internal/cli/cmd_status_test.go`, `internal/cli/cmd_plan_test.go`

**Interfaces:**
- Produces (`cli`, unexported): `func selectSlug(ws *workspace, flag string) (string, error)` — the flag, else the single non-archived bundle; none → error "no active run"; several → error listing them (spec §5.1). `func loadBundle(ws *workspace, slug string) (dir string, st *bundle.State, err error)`.
- Produces: `func validateOpts(ws *workspace, bdir string) plan.ValidateOpts` — `RepoRoot`, `MaxFilesPerTask` from config, `GoalIDs` from `goals.md` when present, `SpecHash` from `spec.md` when present, `LookPath` via `exec.LookPath`. Plan 2's `record --agent planner` reuses it.
- Produces: `takt status` (text by default, `--json` document) and `takt plan validate [path]` (JSON `{"valid","problems","waves","tasks"}`; exit 1 when invalid).

- [ ] **Step 1: Write the failing tests**

`internal/cli/cmd_status_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

func TestStatusSingleBundle(t *testing.T) {
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "topic"); code != 0 {
		t.Fatal(errb)
	}
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md",
		"# Goals — demo\n\n## Anchor\n```text\ntopic\n```\n\n## Goals\n- G1 — it works · signal: test · evidence: go test\n")
	code, got, errb := runIn(t, root, nil, "status", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got["slug"] != "demo" || got["phase"] != "brainstorm" || got["branch"] != "takt/demo" {
		t.Fatalf("out = %v", got)
	}
	tasks := got["tasks"].(map[string]any)
	if tasks["total"] != float64(0) {
		t.Fatalf("tasks = %v", tasks)
	}
	goals := got["goals"].([]any)
	if len(goals) != 1 || goals[0].(map[string]any)["id"] != "G1" {
		t.Fatalf("goals = %v", goals)
	}

	var out strings.Builder
	Main([]string{"status"}, &out, &out, func(k string) string {
		if k == "HOME" {
			return root + "/.home"
		}
		return ""
	}, root)
	text := out.String()
	for _, want := range []string{"demo", "brainstorm", "takt/demo", "G1"} {
		if !strings.Contains(text, want) {
			t.Errorf("text status lacks %q:\n%s", want, text)
		}
	}
}

func TestStatusNeedsSlugWhenSeveral(t *testing.T) {
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "one", "t")
	testutil.Git(t, root, "checkout", "-q", "main")
	runIn(t, root, nil, "init", "--slug", "two", "t")
	code, _, errb := runIn(t, root, nil, "status", "--json")
	if code != 1 || !strings.Contains(errb, "one") || !strings.Contains(errb, "two") {
		t.Fatalf("several bundles must ask for --slug: %d %s", code, errb)
	}
	if code, got, _ := runIn(t, root, nil, "status", "--json", "--slug", "one"); code != 0 || got["slug"] != "one" {
		t.Fatalf("--slug must select: %d %v", code, got)
	}
}

func TestStatusNoRun(t *testing.T) {
	root := testutil.NewRepo(t)
	if code, _, errb := runIn(t, root, nil, "status", "--json"); code != 1 || !strings.Contains(errb, "no active run") {
		t.Fatalf("%d %s", code, errb)
	}
}
```

`internal/cli/cmd_plan_test.go`:

```go
package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

func TestPlanValidateFixture(t *testing.T) {
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	src, _ := os.ReadFile("../plan/testdata/cedar-like.json")
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", string(src))
	code, got, errb := runIn(t, root, nil, "plan", "validate")
	if code != 0 {
		t.Fatalf("exit %d: %s / %v", code, errb, got)
	}
	if got["valid"] != true || got["tasks"] != float64(8) {
		t.Fatalf("out = %v", got)
	}
	waves := got["waves"].(map[string]any)
	if waves["6"] != float64(2) {
		t.Fatalf("waves = %v", waves)
	}
}

func TestPlanValidateReportsProblems(t *testing.T) {
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json",
		`{"schema":1,"spec_hash":"x","tasks":[{"id":1,"title":"a","description":"d","files":["/abs.go"],"verify":[]}]}`)
	code, got, _ := runIn(t, root, nil, "plan", "validate")
	if code != 1 || got["valid"] != false {
		t.Fatalf("invalid plan must exit 1 with valid:false: %d %v", code, got)
	}
	problems := got["problems"].([]any)
	joined := ""
	for _, p := range problems {
		joined += p.(string) + "\n"
	}
	if !strings.Contains(joined, "absolute") || !strings.Contains(joined, "verify") {
		t.Fatalf("problems = %s", joined)
	}
}

func TestPlanValidateUsesSpecHashAndGoals(t *testing.T) {
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	testutil.WriteFile(t, root, "docs/takt/demo/spec.md", "# spec\n")
	testutil.WriteFile(t, root, "docs/takt/demo/goals.md",
		"# Goals — demo\n\n## Anchor\n```text\ntopic\n```\n\n## Goals\n- G1 — it works · signal: test · evidence: go test\n")
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json",
		`{"schema":1,"spec_hash":"sha256:stale","tasks":[{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"],"goals":["G7"]}]}`)
	code, got, _ := runIn(t, root, nil, "plan", "validate")
	joined := ""
	for _, p := range got["problems"].([]any) {
		joined += p.(string) + "\n"
	}
	if code != 1 || !strings.Contains(joined, "spec_hash") || !strings.Contains(joined, "unknown goal G7") || !strings.Contains(joined, "G1 is served by no task") {
		t.Fatalf("%d %s", code, joined)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/cli/ -run 'TestStatus|TestPlan'`
Expected: FAIL — `unknown command: status`.

- [ ] **Step 3: Implement select.go**

```go
package cli

import (
	"errors"
	"strings"

	"github.com/monrad/takt/internal/bundle"
)

// selectSlug picks the run a command operates on (spec §5.1).
func selectSlug(ws *workspace, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	slugs, err := ws.Dir.ListSlugs()
	if err != nil {
		return "", err
	}
	var active []string
	for _, s := range slugs {
		st, err := bundle.LoadState(ws.Dir.Bundle(s))
		if err != nil || st.Phase == bundle.PhaseArchived {
			continue
		}
		active = append(active, s)
	}
	switch len(active) {
	case 0:
		return "", errors.New("no active run in " + ws.Dir.Base)
	case 1:
		return active[0], nil
	default:
		return "", errors.New("several active runs, pass --slug: " + strings.Join(active, ", "))
	}
}

// loadBundle resolves the bundle dir and loads its state.
func loadBundle(ws *workspace, slug string) (string, *bundle.State, error) {
	dir := ws.Dir.Bundle(slug)
	st, err := bundle.LoadState(dir)
	if err != nil {
		return dir, nil, err
	}
	return dir, st, nil
}
```

- [ ] **Step 4: Implement cmd_status.go**

```go
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/goals"
)

func cmdStatus(env Env) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	dirFlag := addDirFlag(fs)
	slug := fs.String("slug", "", "run to show")
	asJSON := fs.Bool("json", false, "print a JSON document instead of text")
	if err := fs.Parse(env.Args); err != nil {
		return 2
	}
	ws, err := openWorkspace(context.Background(), env, *dirFlag)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	s, err := selectSlug(ws, *slug)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "use --slug <name>")
	}
	bdir, st, err := loadBundle(ws, s)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	doc := statusDoc(bdir, st)
	if *asJSON {
		if err := writeJSON(env.Stdout, doc); err != nil {
			return 1
		}
		return 0
	}
	fmt.Fprint(env.Stdout, renderStatus(doc))
	return 0
}

// statusDoc builds the machine-readable status (spec §11).
func statusDoc(bdir string, st *bundle.State) map[string]any {
	counts := map[string]int{"pending": 0, "done": 0, "failed": 0, "blocked": 0, "waived": 0}
	for _, t := range st.Tasks {
		counts[t.Status]++
	}
	doc := map[string]any{
		"slug": st.Slug, "phase": st.Phase, "branch": st.Branch, "branch_adopted": st.BranchAdopted,
		"base": st.Base, "base_sha": st.BaseSHA,
		"tasks":  map[string]any{"total": len(st.Tasks), "by_status": counts},
		"gates":  st.Gates,
		"active_wave": st.ActiveWave, "pending_gate": st.PendingGate,
		"goals": []map[string]any{},
	}
	if b, err := os.ReadFile(filepath.Join(bdir, "goals.md")); err == nil {
		if g, err := goals.Parse(b); err == nil {
			list := make([]map[string]any, 0, len(g.Items))
			for _, it := range g.Items {
				list = append(list, map[string]any{"id": it.ID, "text": it.Text, "signal": it.Signal})
			}
			doc["goals"] = list
			doc["goals_frozen"] = st.GoalsHash != nil && *st.GoalsHash == goals.Hash(b)
		}
	}
	return doc
}

// renderStatus is the one-screen human view.
func renderStatus(doc map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  phase=%s  branch=%s (base %s)\n", doc["slug"], doc["phase"], doc["branch"], doc["base"])
	tasks := doc["tasks"].(map[string]any)
	counts := tasks["by_status"].(map[string]int)
	fmt.Fprintf(&b, "tasks: %d total — pending %d, done %d, failed %d, blocked %d, waived %d\n",
		tasks["total"], counts["pending"], counts["done"], counts["failed"], counts["blocked"], counts["waived"])
	if aw, ok := doc["active_wave"].(*bundle.ActiveWave); ok && aw != nil {
		fmt.Fprintf(&b, "active wave: %d (attempt %d, since %s)\n", aw.N, aw.Attempt, aw.StartedAt.Format("15:04:05"))
	}
	if pg, ok := doc["pending_gate"].(*bundle.PendingGate); ok && pg != nil {
		fmt.Fprintf(&b, "open gate: %s\n", pg.ID)
	}
	if gates, ok := doc["gates"].(map[string]string); ok {
		fmt.Fprintf(&b, "gates: spec=%s plan=%s\n", gates["spec"], gates["plan"])
	}
	if gl, ok := doc["goals"].([]map[string]any); ok && len(gl) > 0 {
		b.WriteString("goals:\n")
		for _, g := range gl {
			fmt.Fprintf(&b, "  %s — %s (%s)\n", g["id"], g["text"], g["signal"])
		}
	}
	return b.String()
}
```

- [ ] **Step 5: Implement cmd_plan.go**

```go
package cli

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/plan"
)

func cmdPlan(env Env) int {
	if len(env.Args) == 0 || env.Args[0] != "validate" {
		return fail(env.Stderr, 2, "usage: takt plan validate [path]", "")
	}
	fs := flag.NewFlagSet("plan validate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	dirFlag := addDirFlag(fs)
	slug := fs.String("slug", "", "run whose plan to validate")
	if err := fs.Parse(env.Args[1:]); err != nil {
		return 2
	}
	ws, err := openWorkspace(context.Background(), env, *dirFlag)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	s, err := selectSlug(ws, *slug)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "use --slug <name>")
	}
	bdir := ws.Dir.Bundle(s)
	path := filepath.Join(bdir, "plan.index.json")
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	idx, err := plan.ParseIndex(raw)
	if err != nil {
		_ = writeJSON(env.Stdout, map[string]any{"valid": false, "problems": []string{err.Error()}, "tasks": 0, "waves": map[string]int{}})
		return 1
	}
	problems := plan.Validate(idx, validateOpts(ws, bdir))
	waves := map[string]int{}
	if w, err := plan.AssignWaves(idx); err == nil {
		for id, n := range w {
			waves[strconv.Itoa(id)] = n
		}
	}
	msgs := make([]string, 0, len(problems))
	for _, p := range problems {
		msgs = append(msgs, p.String())
	}
	if err := writeJSON(env.Stdout, map[string]any{"valid": len(problems) == 0, "problems": msgs, "tasks": len(idx.Tasks), "waves": waves}); err != nil {
		return 1
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}

// validateOpts assembles the context plan.Validate needs from the bundle.
func validateOpts(ws *workspace, bdir string) plan.ValidateOpts {
	o := plan.ValidateOpts{
		RepoRoot:        ws.Repo.Root,
		MaxFilesPerTask: ws.Cfg.MaxFilesPerTask,
		LookPath:        func(tok string) bool { _, err := exec.LookPath(tok); return err == nil },
	}
	if b, err := os.ReadFile(filepath.Join(bdir, "spec.md")); err == nil {
		o.SpecHash = goals.Hash(b)
	}
	if b, err := os.ReadFile(filepath.Join(bdir, "goals.md")); err == nil {
		if g, err := goals.Parse(b); err == nil {
			o.GoalIDs = g.IDs()
		}
	}
	return o
}
```

Register both in `cli.go`: `"status": cmdStatus, "plan": cmdPlan`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -v`
Expected: all PASS. If `TestPlanValidateFixture` fails on `verify not found on PATH`, the fixture's `go run ./tools/dashboards -check` needs `go` on PATH — it is in any Go dev environment; `true` is a shell builtin **and** `/usr/bin/true` on Linux/macOS, which is what `exec.LookPath` finds.

- [ ] **Step 7: Commit**

```bash
golangci-lint run ./...
git add internal/cli
git commit -m "feat(cli): takt status (text/json) and takt plan validate with spec-hash and goals context"
```

---

### Task 10: `takt doctor` — state-schema and plan-disjoint checks

**Files:**
- Create: `internal/doctor/doctor.go`, `internal/doctor/state_schema.go`, `internal/doctor/plan_disjoint.go`, `internal/cli/cmd_doctor.go`
- Modify: `internal/cli/cli.go` (register `doctor`)
- Test: `internal/doctor/doctor_test.go`, `internal/cli/cmd_doctor_test.go`

**Interfaces:**
- Produces (`doctor`): `type Finding struct { Level, Check, Slug, Message, Fix string }` with `Level ∈ "PASS"|"WARN"|"ERROR"`; `type Check struct { Name string; Run func(ctx context.Context, in Input) []Finding }`; `type Input struct { Dir bundle.Dir; Slug string; BundleDir string; State *bundle.State; ValidateOpts plan.ValidateOpts }`; `func Run(ctx context.Context, dir bundle.Dir, all bool, checks []Check, opts func(bundleDir string) plan.ValidateOpts) []Finding`; `var Default = []Check{StateSchema, PlanDisjoint}`. Plan 2 appends `StaleWave`, `IndexStaleness`, `Branch` to `Default`.
- Produces: `takt doctor [--all] [--json]` — one line per finding `LEVEL  check: message` (+ indented `fix:`), exit 1 on any ERROR (spec §11).

- [ ] **Step 1: Write the failing doctor tests**

`internal/doctor/doctor_test.go`:

```go
package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/plan"
)

func newDir(t *testing.T) bundle.Dir {
	t.Helper()
	d, err := bundle.ResolveDir(t.TempDir(), t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func healthy(slug string) *bundle.State {
	return &bundle.State{Schema: 1, Slug: slug, Topic: "t", Phase: bundle.PhaseExecute, CreatedAt: time.Now(),
		Branch: "takt/" + slug, Base: "main", Gates: map[string]string{"spec": "ok", "plan": "ok"},
		Tasks: []bundle.Task{{ID: 1, Wave: 0, Status: bundle.StatusPending, Files: []string{"a.go"}, Class: "implement"}}}
}

func noOpts(string) plan.ValidateOpts { return plan.ValidateOpts{RepoRoot: "/", MaxFilesPerTask: 12} }

func levels(fs []Finding, check string) []string {
	var out []string
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f.Level)
		}
	}
	return out
}

func TestHealthyBundlePasses(t *testing.T) {
	d := newDir(t)
	if err := bundle.SaveState(d.Bundle("ok"), healthy("ok")); err != nil {
		t.Fatal(err)
	}
	fs := Run(context.Background(), d, false, Default, noOpts)
	for _, f := range fs {
		if f.Level != "PASS" {
			t.Errorf("unexpected %+v", f)
		}
	}
	if len(levels(fs, "state-schema")) != 1 || len(levels(fs, "plan-disjoint")) != 1 {
		t.Fatalf("each check reports once per bundle: %+v", fs)
	}
}

func TestCorruptStateIsError(t *testing.T) {
	d := newDir(t)
	os.MkdirAll(d.Bundle("bad"), 0o755)
	os.WriteFile(filepath.Join(d.Bundle("bad"), "state.json"), []byte(`{"schema":1,"slug":"bad","phase":"flying"}`), 0o644)
	fs := Run(context.Background(), d, false, Default, noOpts)
	if l := levels(fs, "state-schema"); len(l) != 1 || l[0] != "ERROR" {
		t.Fatalf("state-schema = %v", l)
	}
}

func TestActiveWaveReferencingMissingWaveIsError(t *testing.T) {
	d := newDir(t)
	st := healthy("aw")
	st.ActiveWave = &bundle.ActiveWave{N: 5, Attempt: 1, StartedAt: time.Now()}
	bundle.SaveState(d.Bundle("aw"), st)
	fs := Run(context.Background(), d, false, Default, noOpts)
	if l := levels(fs, "state-schema"); l[0] != "ERROR" {
		t.Fatalf("active_wave pointing at a wave with no tasks must be ERROR: %+v", fs)
	}
}

func TestPlanDisjointFindsUnorderedOverlap(t *testing.T) {
	d := newDir(t)
	bundle.SaveState(d.Bundle("ov"), healthy("ov"))
	os.WriteFile(filepath.Join(d.Bundle("ov"), "plan.index.json"), []byte(`{"schema":1,"spec_hash":"x","tasks":[
	  {"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]},
	  {"id":2,"title":"b","description":"d","files":["a.go"],"verify":["true"]}]}`), 0o644)
	fs := Run(context.Background(), d, false, Default, noOpts)
	if l := levels(fs, "plan-disjoint"); len(l) != 1 || l[0] != "ERROR" {
		t.Fatalf("plan-disjoint = %v", l)
	}
}

func TestArchivedSkippedUnlessAll(t *testing.T) {
	d := newDir(t)
	st := healthy("old")
	st.Phase = bundle.PhaseArchived
	bundle.SaveState(d.Bundle("old"), st)
	if fs := Run(context.Background(), d, false, Default, noOpts); len(fs) != 0 {
		t.Fatalf("archived must be skipped: %+v", fs)
	}
	if fs := Run(context.Background(), d, true, Default, noOpts); len(fs) == 0 {
		t.Fatal("--all must include archived")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/doctor/`
Expected: FAIL — `undefined: Run`, `Default`.

- [ ] **Step 3: Implement the doctor package**

`internal/doctor/doctor.go`:

```go
// Package doctor runs read-only health checks over every bundle (spec §11).
package doctor

import (
	"context"
	"sort"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/plan"
)

// Finding is one line of doctor output.
type Finding struct {
	Level   string `json:"level"` // PASS | WARN | ERROR
	Check   string `json:"check"`
	Slug    string `json:"slug"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Input is what a check sees for one bundle.
type Input struct {
	Dir          bundle.Dir
	Slug         string
	BundleDir    string
	State        *bundle.State
	ValidateOpts plan.ValidateOpts
}

// Check is one named health check.
type Check struct {
	Name string
	Run  func(ctx context.Context, in Input) []Finding
}

// Default is the check set shipped in plan 1; plan 2 appends more.
var Default = []Check{StateSchema, PlanDisjoint}

// Run executes checks over every bundle (archived only with all). A bundle
// whose state cannot load yields one state-schema ERROR and no other checks.
func Run(ctx context.Context, dir bundle.Dir, all bool, checks []Check, opts func(bundleDir string) plan.ValidateOpts) []Finding {
	var out []Finding
	slugs, err := dir.ListSlugs()
	if err != nil {
		return []Finding{{Level: "ERROR", Check: "bundles", Message: err.Error()}}
	}
	for _, slug := range slugs {
		bdir := dir.Bundle(slug)
		st, err := bundle.LoadState(bdir)
		if err != nil {
			out = append(out, Finding{Level: "ERROR", Check: "state-schema", Slug: slug, Message: err.Error(),
				Fix: "restore state.json from git history; takt never repairs state silently"})
			continue
		}
		if st.Phase == bundle.PhaseArchived && !all {
			continue
		}
		in := Input{Dir: dir, Slug: slug, BundleDir: bdir, State: st, ValidateOpts: opts(bdir)}
		for _, c := range checks {
			out = append(out, c.Run(ctx, in)...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Slug != out[j].Slug {
			return out[i].Slug < out[j].Slug
		}
		return out[i].Check < out[j].Check
	})
	return out
}

// HasError reports whether any finding is an ERROR.
func HasError(fs []Finding) bool {
	for _, f := range fs {
		if f.Level == "ERROR" {
			return true
		}
	}
	return false
}
```

`internal/doctor/state_schema.go`:

```go
package doctor

import (
	"context"
	"fmt"
)

// StateSchema re-validates state.json beyond what LoadState enforces:
// the active wave must reference tasks, and an open gate must have an id.
var StateSchema = Check{Name: "state-schema", Run: func(_ context.Context, in Input) []Finding {
	st := in.State
	f := Finding{Level: "PASS", Check: "state-schema", Slug: in.Slug, Message: "state.json is schema-valid"}
	if st.ActiveWave != nil {
		found := false
		for _, t := range st.Tasks {
			if t.Wave == st.ActiveWave.N {
				found = true
				break
			}
		}
		if !found {
			f.Level, f.Message = "ERROR", fmt.Sprintf("active_wave.n=%d but no task has that wave", st.ActiveWave.N)
			f.Fix = "run `takt next --recover` once plan 2 lands; until then inspect state.json"
			return []Finding{f}
		}
	}
	if st.PendingGate != nil && st.PendingGate.ID == "" {
		f.Level, f.Message, f.Fix = "ERROR", "pending_gate has no id", "clear it with `takt answer` once plan 2 lands"
		return []Finding{f}
	}
	if st.Phase == "execute" && len(st.Tasks) == 0 {
		f.Level, f.Message, f.Fix = "ERROR", "phase is execute but tasks is empty", "the plan was never loaded; re-run planning"
	}
	return []Finding{f}
}}
```

`internal/doctor/plan_disjoint.go`:

```go
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/monrad/takt/internal/plan"
)

// PlanDisjoint re-validates plan.index.json when present: shared files must
// be ordered by depends_on (so same-wave tasks are disjoint) and paths obey
// the repo-relative rule. State tasks must match the index ids (WARN).
var PlanDisjoint = Check{Name: "plan-disjoint", Run: func(_ context.Context, in Input) []Finding {
	raw, err := os.ReadFile(filepath.Join(in.BundleDir, "plan.index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []Finding{{Level: "PASS", Check: "plan-disjoint", Slug: in.Slug, Message: "no plan yet"}}
	}
	if err != nil {
		return []Finding{{Level: "ERROR", Check: "plan-disjoint", Slug: in.Slug, Message: err.Error()}}
	}
	idx, err := plan.ParseIndex(raw)
	if err != nil {
		return []Finding{{Level: "ERROR", Check: "plan-disjoint", Slug: in.Slug, Message: err.Error(), Fix: "regenerate the plan index"}}
	}
	o := in.ValidateOpts
	o.LookPath, o.GoalIDs, o.SpecHash = nil, nil, "" // structural checks only here
	if ps := plan.Validate(idx, o); len(ps) > 0 {
		return []Finding{{Level: "ERROR", Check: "plan-disjoint", Slug: in.Slug,
			Message: fmt.Sprintf("%d problem(s); first: %s", len(ps), ps[0]), Fix: "run `takt plan validate` for the full list"}}
	}
	if len(in.State.Tasks) > 0 && len(in.State.Tasks) != len(idx.Tasks) {
		return []Finding{{Level: "WARN", Check: "plan-disjoint", Slug: in.Slug,
			Message: fmt.Sprintf("state has %d tasks, plan.index.json has %d", len(in.State.Tasks), len(idx.Tasks)),
			Fix: "the index changed after load; reload the plan"}}
	}
	return []Finding{{Level: "PASS", Check: "plan-disjoint", Slug: in.Slug, Message: fmt.Sprintf("%d tasks, shared files ordered", len(idx.Tasks))}}
}}
```

Run: `go test ./internal/doctor/ -v` — Expected: PASS.

- [ ] **Step 4: Write the failing CLI test and implement cmd_doctor.go**

`internal/cli/cmd_doctor_test.go`:

```go
package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

func TestDoctorTextAndExitCode(t *testing.T) {
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	var out bytes.Buffer
	getenv := func(k string) string {
		if k == "HOME" {
			return root + "/.home"
		}
		return ""
	}
	if code := Main([]string{"doctor"}, &out, &out, getenv, root); code != 0 {
		t.Fatalf("healthy repo: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "PASS  state-schema") {
		t.Fatalf("output = %s", out.String())
	}
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json",
		`{"schema":1,"spec_hash":"x","tasks":[{"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]},{"id":2,"title":"b","description":"d","files":["a.go"],"verify":["true"]}]}`)
	out.Reset()
	if code := Main([]string{"doctor"}, &out, &out, getenv, root); code != 1 {
		t.Fatalf("overlap: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "ERROR plan-disjoint") || !strings.Contains(out.String(), "fix:") {
		t.Fatalf("output = %s", out.String())
	}
	code, got, _ := runIn(t, root, nil, "doctor", "--json")
	if code != 1 || got["errors"] != float64(1) {
		t.Fatalf("--json: %d %v", code, got)
	}
}
```

`internal/cli/cmd_doctor.go`:

```go
package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/monrad/takt/internal/doctor"
	"github.com/monrad/takt/internal/plan"
)

func cmdDoctor(env Env) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	dirFlag := addDirFlag(fs)
	all := fs.Bool("all", false, "include archived bundles")
	asJSON := fs.Bool("json", false, "print findings as JSON")
	if err := fs.Parse(env.Args); err != nil {
		return 2
	}
	ws, err := openWorkspace(context.Background(), env, *dirFlag)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	findings := doctor.Run(context.Background(), ws.Dir, *all, doctor.Default,
		func(bdir string) plan.ValidateOpts { return validateOpts(ws, bdir) })
	errs := 0
	for _, f := range findings {
		if f.Level == "ERROR" {
			errs++
		}
	}
	if *asJSON {
		_ = writeJSON(env.Stdout, map[string]any{"findings": findings, "errors": errs})
	} else {
		for _, f := range findings {
			fmt.Fprintf(env.Stdout, "%-5s %s: %s\n", f.Level, f.Check+" "+f.Slug, f.Message)
			if f.Fix != "" {
				fmt.Fprintf(env.Stdout, "      fix: %s\n", f.Fix)
			}
		}
		fmt.Fprintf(env.Stdout, "takt doctor: %d finding(s), %d error(s)\n", len(findings), errs)
	}
	if errs > 0 {
		return 1
	}
	return 0
}
```

Register `"doctor": cmdDoctor` in `cli.go`. Note the text format pads the level to 5 characters, so `PASS  state-schema` has two spaces and `ERROR plan-disjoint` one — the test matches both.

- [ ] **Step 5: Run everything**

Run: `go test ./... && golangci-lint run ./... && go build -o takt ./cmd/takt && ./takt version`
Expected: every package PASS, no lint findings, `{"version": "0.0.0-dev"}`.

- [ ] **Step 6: Commit**

```bash
git add internal/doctor internal/cli
git commit -m "feat(doctor): state-schema and plan-disjoint checks with takt doctor"
```

---

## Self-review (run before handoff)

**Spec coverage for this plan's scope.** §3.3 layout → Tasks 1–10 create exactly the listed files (`internal/brief`, `internal/backend`, `internal/gate`, `internal/wave`, `internal/decide` and the agents/prompt are plans 2–3). §4.1 dir resolution → Task 4. §4.3 state schema → Task 5 (field names and order match the spec's JSON). §4.4 events → Task 5. §4.5 path rules → Tasks 4, 5, 6. §4.6 session lock → Task 5 (`Acquire` semantics: acquired / held-by-self / stolen / forced / blocked). §4.7 branch rule and `takt(<slug>): init` commit → Task 8. §5.1 output contract, `version --expect`, `status`, `plan validate`, `doctor` → Tasks 1, 9, 10. §7.1 `init` refusals (slug exists, staged changes, outside a repo, detached HEAD) → Task 8. §7.2 goals.md format and hash → Task 7. §7.3 index schema, every validation bullet, wave assignment → Task 6. §11 doctor `state-schema` + `plan-disjoint` → Task 10 (the other three checks need `active_wave` timing and gate receipts — plan 2). §12 config precedence, durations, task classes, frozen per-run config → Tasks 3, 8. §13 atomic writes, fail loud, no network → Tasks 5, 8 (init never pushes). §18 slug derivation default → Task 8.

**Deliberately not in plan 1:** `takt next/record/answer/done/close-wave/review/verify/waive/unlock/goals amend` (plan 2), `commands/takt.md`, `agents/*.md`, `.claude-plugin/*`, `flake.nix`, the kill/resume e2e (plan 3).

**Type consistency checked:** `bundle.CheckRelPath(root, p)` (Tasks 4→5→6), `bundle.ResolveDir(repoRoot, home, flag, env, cfgDir)` (4→8→10), `bundle.LoadState/SaveState(bundleDir)` (5→8→9→10), `Dir.Bundle/ListSlugs/RelToRepo` (4→8→9→10), `plan.ParseIndex/Validate/AssignWaves/ValidateOpts` (6→9→10), `goals.Parse/Hash/IDs` (7→9), `config.Load/Defaults/IsTaskClass/TaskClasses` (3→6→8), `gitx.Repo` methods (2→8), `cli.Main/writeJSON/fail/Env` (1→8→9→10), `openWorkspace/selectSlug/loadBundle/validateOpts/sessionID/deriveSlug` (8→9→10), `doctor.Run(ctx, dir, all, checks, opts)` (10).

**Acceptance for the whole plan** (run from the repo root after Task 10):

```bash
go test ./... && golangci-lint run ./... && go build -o takt ./cmd/takt
cd "$(mktemp -d)" && git init -q -b main && git commit -q --allow-empty -m init
~/code/misc/takt/takt init "Add a greeting endpoint"          # → JSON, branch takt/add-a-greeting-endpoint
~/code/misc/takt/takt status                                   # → one-screen report
~/code/misc/takt/takt doctor                                   # → PASS lines, exit 0
git log --oneline                                              # → "takt(add-a-greeting-endpoint): init"
```
