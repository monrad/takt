// Package cli implements the takt command-line surface: one JSON object on
// stdout and exit 0 on success; {"error","hint"} on stderr with exit 1; usage
// errors exit 2 (spec §5.1).
package cli

import (
	"encoding/json"
	"flag"
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

// exitUsage is the process exit code for usage errors: no command, an
// unknown command, or bad flags (spec §5.1).
const exitUsage = 2

// JSON document keys shared by more than one command's output — named so
// goconst does not flag the repeated literals across cmd_init.go,
// cmd_status.go, and cmd_plan.go.
const (
	keySlug          = "slug"
	keyBranch        = "branch"
	keyBranchAdopted = "branch_adopted"
	keyBase          = "base"
	keyBaseSHA       = "base_sha"
	keyTasks         = "tasks"
)

var commands = map[string]command{
	"version": cmdVersion,
	"init":    cmdInit,
	"status":  cmdStatus,
	"plan":    cmdPlan,
	"doctor":  cmdDoctor,
}

// Main dispatches args[0] to a subcommand and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer, getenv func(string) string, cwd string) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return fail(stderr, exitUsage, "unknown command: "+args[0], "run `takt help`")
	}
	return cmd(Env{Args: args[1:], Stdout: stdout, Stderr: stderr, Getenv: getenv, Cwd: cwd})
}

func usage(w io.Writer) int {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return fail(w, exitUsage, "usage: takt <command> [flags]", "commands: "+strings.Join(names, ", "))
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

// usageError reports a flag-parsing failure through the JSON error contract.
// Every command's [flag.FlagSet] must route fs.Parse errors through this
// instead of returning exitUsage directly, so bad flags never leak the
// flag package's own plain-text output onto stderr.
func usageError(env Env, fs *flag.FlagSet, err error) int {
	return fail(env.Stderr, exitUsage,
		"invalid flags for "+fs.Name()+": "+err.Error(),
		"run `takt "+fs.Name()+" -h` for the accepted flags")
}
