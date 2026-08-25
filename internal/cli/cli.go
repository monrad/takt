// Package cli implements the takt command-line surface: one JSON object on
// stdout and exit 0 on success; {"error","hint"} on stderr with exit 1; usage
// errors exit 2 (spec §5.1).
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
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

// isFlagArg reports whether s would be parsed as a flag by [flag.FlagSet].
// It mirrors the flag package's own rule, so a bare "-" counts as a
// positional argument rather than a flag that never gets consumed.
func isFlagArg(s string) bool {
	return len(s) > 1 && s[0] == '-'
}

// parseInterspersed parses fs's flags when they may appear before, after, or
// between positional arguments, and returns the positionals in order.
// [flag.FlagSet.Parse] stops at the first non-flag argument, but takt's
// documented forms put positionals first — spec §5.1 writes
// `takt init <topic…> [--slug s] [--autonomy …]` — so without this a flag
// after the topic is silently swallowed into the topic (review finding 2).
// It parses, takes the positionals up to the next flag, re-parses the
// remainder, and repeats. A literal "--" ends flag parsing: everything after
// it is positional, whatever it starts with.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string // after a literal "--": positional, never flags
	if i := slices.Index(args, "--"); i >= 0 {
		args, tail = args[:i], args[i+1:]
	}
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		n := 0
		for n < len(rest) && !isFlagArg(rest[n]) {
			n++
		}
		positional = append(positional, rest[:n]...)
		args = rest[n:]
	}
	return append(positional, tail...), nil
}

// gitTimeout bounds one takt command's whole run against git. Every git call
// goes through a context carrying this deadline, so a hung hook, a
// credential prompt, or a wedged index lock fails the command instead of
// hanging takt forever (spec §13). Plan 2 turns this into a config value;
// commandContext's env parameter is the hook for that.
const gitTimeout = 2 * time.Minute

// commandContext returns the deadline-bounded context a command runs under.
// Callers must defer the returned [context.CancelFunc]. The Env parameter is
// unused today and is the seam plan 2 reads a configured timeout from.
func commandContext(_ Env) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), gitTimeout)
}
