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
	"maps"
	"slices"
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

// exitError is the process exit code for a runtime failure: the command was
// understood but could not be carried out (spec §5.1).
const exitError = 1

// JSON document and event-data keys shared by more than one command —
// named so goconst does not flag the repeated literals across the command
// files. The values are the wire format and must not change.
const (
	keySlug          = "slug"
	keyBranch        = "branch"
	keyBranchAdopted = "branch_adopted"
	keyBase          = "base"
	keyBaseSHA       = "base_sha"
	keyTasks         = "tasks"
	keyGate          = "gate"
	keyChoice        = "choice"
	keyReason        = "reason"
	keyHash          = "hash"
	keyCount         = "count"
	keyGoals         = "goals"
	keySession       = "session"
	keyProblems      = "problems"
	keyMode          = "mode"
	keyValid         = "valid"
	keyFindings      = "findings"
	keyWaves         = "waves"
	keyVerdict       = "verdict"
	keyTask          = "task"
	keyAttempt       = "attempt"
	keyWave          = "wave"
	keySlice         = "slice"
	keyStatus        = "status"
	keyCommitted     = "committed"
	keySHA           = "sha"
	keyPassed        = "passed"
	keyFailed        = "failed"
	keyNoCommands    = "no_commands"
	keyIgnored       = "ignored"
	keyHealed        = "healed"
	keyVersion       = "version"
	keyStep          = "step"
	keyProvider      = "provider"
)

// The non-task agents `takt next` dispatches and `takt record --agent`
// records; the same names identify the capped agent in the agent_invalid
// gate's context (spec §5.3 rows 8, 10, 11, 21).
const (
	agentPlanner  = "planner"
	agentAuditor  = "alignment-auditor"
	agentAssessor = "goal-assessor"
)

// state.gates values: spec §4.3's pending | ok | skipped, plus `disabled`
// for a review this run switched off at init — a gate that will never take a
// receipt, which none of the three existing words describes. Named so
// goconst sees one definition instead of repeated literals; the values are
// the wire format `takt status` prints and must not change.
const (
	gateOK       = "ok"
	gateSkipped  = "skipped"
	gateDisabled = "disabled"
)

// gatePending is a gate's value before any receipt exists (state.gates at
// init, and a live gate.Compute with no verdict yet) — named so goconst
// sees one definition instead of repeated literals across cmd_init.go and
// cmd_status.go.
const gatePending = "pending"

// Task statuses a close record can hold that state.json cannot: a reviewed
// task sent back for rework is still pending, and a task whose review could
// not be run at all is neither passed nor failed (spec §7.4 step 4).
const (
	statusRework      = "rework"
	statusReviewError = "review_error"
)

var commands = map[string]command{
	"version":    cmdVersion,
	"init":       cmdInit,
	"status":     cmdStatus,
	"plan":       cmdPlan,
	"doctor":     cmdDoctor,
	"next":       cmdNext,
	"done":       cmdDone,
	"review":     cmdReview,
	"record":     cmdRecord,
	"answer":     cmdAnswer,
	"goals":      cmdGoals,
	"unlock":     cmdUnlock,
	"close-wave": cmdCloseWave,
	"waive":      cmdWaive,
	"verify":     cmdVerify,
}

// Commands is every subcommand name, sorted: the list `takt` accepts, the
// one usage prints, and the one the prompt parity test holds
// commands/takt.md to — a `takt <name>` the prompt mentions must be one of
// these (spec §6).
func Commands() []string {
	return slices.Sorted(maps.Keys(commands))
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
	return fail(w, exitUsage, "usage: takt <command> [flags]", "commands: "+strings.Join(Commands(), ", "))
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

// defaultGitTimeout bounds every command's git work (spec §13). Plan 3 wires
// it to config; TAKT_GIT_TIMEOUT overrides it (tests shorten it).
const defaultGitTimeout = 2 * time.Minute

// commandContext returns the deadline every command runs its git under.
// Callers must defer the returned [context.CancelFunc].
func commandContext(env Env) (context.Context, context.CancelFunc) {
	d := defaultGitTimeout
	if env.Getenv != nil {
		if v := env.Getenv("TAKT_GIT_TIMEOUT"); v != "" {
			if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
				d = parsed
			}
		}
	}
	return context.WithTimeout(context.Background(), d)
}
