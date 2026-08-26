package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/testutil"
)

// e2eEnvVar gates the one test in this file. It drives real agents and a
// real reviewer over the network: it spends money, takes minutes, and
// depends on two CLIs being logged in — so the hermetic suite (TAKT_E2E
// unset) must never reach it (Task 7 brief).
const e2eEnvVar = "TAKT_E2E"

// e2eLogDirEnvVar names a directory the live agents' prompts and output are
// kept in. Unset, they go to a t.TempDir the test framework deletes on the
// way out — fine while the run passes, useless the moment it does not — so
// a run whose evidence has to outlive it names one:
//
//	TAKT_E2E=1 TAKT_E2E_LOGDIR=/tmp/takt-e2e go test ./internal/cli/ -run TestLiveEndToEnd -v
const e2eLogDirEnvVar = "TAKT_E2E_LOGDIR"

// implementerTimeout bounds one live implementer.
const implementerTimeout = 10 * time.Minute

// implementerKillDelay is how long a killed implementer — and anything
// still holding its stdout — may take to go away, mirroring the backend
// package's own waitDelay.
const implementerKillDelay = 5 * time.Second

// liveModel is the model liveCfg pins the plan's task class to: the model
// the dispatch op names, the model the hook runs, and the model the digest
// records — one fact, asserted in all three places.
const liveModel = "haiku"

// doneReport is the STATUS line a clean implementer run ends with; anything
// else makes the whole final message worth printing.
const doneReport = "STATUS: done"

// outputTailBudget bounds how much of a failed agent's output lands in an
// error message; the whole of it is logged separately.
const outputTailBudget = 800

// goTestTimeout bounds the final `go test ./...` in the finished repository.
const goTestTimeout = 5 * time.Minute

// e2eMaxSteps bounds the live walk. The scripted run takes about twenty ops;
// the slack is for a review that sends a task back for rework.
const e2eMaxSteps = 60

// liveCfg is the throwaway repository's .takt.json: the shipped reviewer
// chain (copilot, then claude) instead of the fake every other test in this
// package uses, a max_parallel honest about a two-task plan, and the plan's
// task class pinned to haiku.
//
// That pin is what makes the run's own record true. The hook runs whatever
// model the dispatch op names, and the digest records the model takt
// resolved for the attempt — so with `bounded` left at its shipped sonnet,
// a haiku hook would have made takt record a model that never saw the task.
// Pinning the class instead keeps the run cheap AND keeps the two facts the
// same one, which is what the assertions below check.
const liveCfg = `{"backends":{"reviewer":["copilot","claude"]},"max_parallel":2,` +
	`"agents":{"implementer":{"by_class":{"bounded":"haiku"}}}}`

// liveGoMod and liveMain are the module the run adds a greeting to: real
// enough that `go build ./...` and `go test ./...` mean something, small
// enough that a haiku implementer has nothing to get lost in.
//
// liveGitignore is the rule any Go repository with a main package carries —
// takt's own has /takt for it. Task 1's verify command is `go build ./...`,
// and for a main package that writes the executable into the module root.
// The wave's scope check cannot catch it: spec §7.4 step 4 runs scope verify
// *before* the verify commands, so what a verify command creates afterwards
// is never in `touched` — the wave commit is path-scoped and never commits
// it, and it is the repository's own ignore rule that keeps it from being
// litter. Without this the run ends clean in every sense except `?? greet`.
const (
	liveGoMod     = "module greet\n\ngo 1.26\n"
	liveMain      = "package main\n\nfunc main() {}\n"
	liveGitignore = "/greet\n"
)

// liveSpec is what the brainstorm step writes. It is a real spec, not the
// one-line placeholder the hermetic walk uses, because a real reviewer
// judges it at the spec gate: testable requirements, an explicit scope, an
// Assumptions & Open Decisions table, and success criteria that match the
// G1 in goalsMD. It carries no backtick spans, so it can live in a raw
// string beside the plan it is planned into.
const liveSpec = `# Design: a greeting for the greet module

## Overview
The greet module is a single-package Go program whose main function does
nothing. This run adds one exported function to it and the test that proves
the function behaves as specified. Nothing else about the module changes.

## Requirements
- R1 — a new file greet.go at the module root declares package main (the
  package main.go beside it already declares) and one exported function,
  Greet(name string) string.
- R2 — Greet returns the string "Hello, " immediately followed by its name
  argument, with no trailing punctuation and no other text. Greet("takt")
  therefore returns exactly "Hello, takt".
- R3 — a new file greet_test.go at the module root holds a table-driven Go
  test over at least two cases — Greet("takt") is exactly "Hello, takt" and
  Greet("") is exactly "Hello, " — failing on any mismatch. One case cannot
  prove R2: an implementation that returned the constant "Hello, takt" would
  satisfy it.
- R4 — go build ./... and go test ./... both succeed at the module root.

## Success criteria
Goal G1 in goals.md ("greet works", evidenced by go test ./...) is met when
R3's test is present and go test ./... passes at HEAD.

## Scope
In scope: greet.go and greet_test.go, both new, both at the module root.
Out of scope: main.go, go.mod, the module path, any dependency, and any
behaviour beyond the single string R2 defines.

## Assumptions & Open Decisions
| Question | Decision | Rationale | Status |
|---|---|---|---|
| Punctuation after the name? | None: "Hello, " + name | R2 pins the exact string R3's test asserts; an exclamation mark would fail it | decided |
| What does Greet("") return? | "Hello, ", with no validation | Nothing in scope reads the result, so a validation rule would be untested code | decided |
| A package of its own? | No: package main, in the module root directory | Go allows one package per directory, and main.go already fixes it | decided |
| Localisation? | Out of scope | Nothing in the request or in goals.md asks for it | decided |
`

// livePlanMD is the human-readable plan the plan gate reviews beside the
// index below.
const livePlanMD = `# Plan — add a greeting to the greet module

Two waves, one task each. Wave 1 depends on wave 0 because the test compiles
against the function wave 0 writes, so the two cannot run at once.

## Wave 0
- Task 1 — Add Greet to package main. Files: greet.go. Class: bounded.
  Serves G1. Verify: go build ./... — the file has to compile as part of the
  module before anything can call it.

## Wave 1
- Task 2 — Test Greet, table-driven over both cases R3 names ("takt" and the
  empty name). Files: greet_test.go. Class: bounded. Depends on task 1.
  Serves G1. Verify: go test ./... — the test is the evidence G1 names.

## Requirement coverage
- R1, R2 → task 1.
- R3 → task 2.
- R4 → task 1's verify command (build) and task 2's (test).
`

// livePlanIndex is the plan the stand-in planner records; %s is stamped with
// the hash of the spec above, exactly as validIndex is.
const livePlanIndex = `{"schema":1,"spec_hash":"%s","tasks":[
 {"id":1,"title":"Add Greet to package main","description":"Create the file greet.go at the module root. It declares package main — main.go beside it already does, and Go allows only one package per directory — and defines one exported function with a doc comment: func Greet(name string) string, returning the string \"Hello, \" immediately followed by name, with no trailing punctuation, so that Greet(\"takt\") returns exactly \"Hello, takt\". Change nothing else: main.go and go.mod are out of scope.","files":["greet.go"],"verify":["go build ./..."],"depends_on":[],"goals":["G1"],"class":"bounded"},
 {"id":2,"title":"Test Greet","description":"Create the file greet_test.go at the module root, in package main, holding one table-driven Go test over at least two cases: Greet(\"takt\") must return exactly \"Hello, takt\", and Greet(\"\") must return exactly \"Hello, \". Report the input and the mismatch on failure. Use the standard testing package only. Do not edit greet.go: if Greet does not behave as specified, report that as a blocker instead of changing it.","files":["greet_test.go"],"verify":["go test ./..."],"depends_on":[1],"goals":["G1"],"class":"bounded"}]}`

// liveProviders are the reviewer names that mean a real backend ran.
var liveProviders = []string{"copilot", "claude"}

// liveSession is what the walk carries besides the driver: the model each
// live implementer was dispatched on, its final message verbatim, and how
// many wave retries have been spent. The messages are kept so that any gate
// the run should not have reached can print what the agents actually said —
// the one thing a failure here always turns on.
type liveSession struct {
	models   []string
	messages []string
	retries  int
}

// transcript is every live implementer's final message, verbatim, for a
// failure message.
func (ls *liveSession) transcript() string {
	if len(ls.messages) == 0 {
		return "(no implementer has reported yet)"
	}
	var b strings.Builder
	for i, m := range ls.messages {
		model := "?"
		if i < len(ls.models) {
			model = ls.models[i]
		}
		fmt.Fprintf(&b, "=== implementer %d on %s said ===\n%s\n", i+1, model, m)
	}
	return b.String()
}

// TestLiveEndToEnd drives one whole run through cli.Main against real
// agents: the implementers are Claude Code on haiku, editing a throwaway Go
// module for real, and the spec, plan and per-task reviews go to whichever
// of copilot/claude is healthy. The planner, the alignment auditor and the
// goal assessor stay scripted — they are the parts this test is not about —
// and everything else is the shipped code path: the briefs takt renders, the
// final messages `takt record` parses, the verify commands, the reviewer
// verdicts, the wave commits and the archive.
//
// Every `next` is also run twice with a fresh named session in between
// (driver.takeover), so the run proves spec §14's kill/resume at every op
// boundary against a real run rather than a fixture.
//
// Run it with:
//
//	TAKT_E2E=1 go test ./internal/cli/ -run TestLiveEndToEnd -v -count=1 -timeout 45m
//
// Set TAKT_E2E_LOGDIR to a directory of your own to keep each agent's
// prompt, stdout and stderr after the test ends; without it they go to a
// t.TempDir that the framework deletes on the way out. Either way every
// non-happy path — a failed `claude` run, an implementer that did not report
// done, a `record` takt refused, a wave gate — prints the agent's whole
// final message on the test log, so `go test -v` output alone is enough to
// tell what happened.
func TestLiveEndToEnd(t *testing.T) {
	skipUnlessE2E(t)
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("skipping the live end-to-end run: claude is not on PATH (%v)", err)
	}
	// This test binary runs with HOME pointed at a throwaway directory
	// (testutil.RunHermetic) so that git never reads the developer's
	// configuration. The live implementer and the real reviewers are
	// credentialed CLIs that find their login under HOME, so it goes back
	// for this test; git stays hermetic on GIT_CONFIG_GLOBAL and
	// GIT_CONFIG_NOSYSTEM, which are untouched.
	//
	// t.Setenv writes the process environment, not this test's own, so for
	// as long as this test runs every other test in the binary sees the real
	// HOME too. That is acceptable because nothing else here reads it: the
	// hermetic tests reach git through testutil.Git and runIn, which pin
	// HOME themselves on every call. It is also why this test takes the
	// whole binary rather than running in parallel — t.Setenv forbids
	// t.Parallel — and why it is invoked with -run TestLiveEndToEnd.
	t.Setenv("HOME", testutil.RealHome())

	root, bdir := liveRepo(t)
	logDir := liveLogDir(t)
	t.Logf("live run: repo %s, agent logs %s", root, logDir)

	ls := &liveSession{}
	d := &driver{
		t: t, root: root, bdir: bdir,
		env:      map[string]string{"TAKT_SESSION": "live-0"},
		takeover: true,
		implement: func(brief, repo, model string) (string, error) {
			ls.models = append(ls.models, model)
			msg, err := runImplementer(t, logDir, len(ls.models), brief, repo, model)
			ls.messages = append(ls.messages, msg)
			return msg, err
		},
	}

	start := time.Now()
	reason := playLive(t, d, ls)
	elapsed := time.Since(start)
	t.Logf("ops: %s", strings.Join(d.ops, " "))
	t.Logf("wall time: %s, live implementer runs: %v", elapsed.Round(time.Second), ls.models)
	if reason != stopArchived {
		t.Fatalf("the live run must end archived, stopped %q", reason)
	}

	st, err := bundle.LoadState(bdir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != bundle.PhaseArchived || st.ActiveWave != nil || st.Session != nil {
		t.Fatalf("phase=%s active=%+v session=%+v", st.Phase, st.ActiveWave, st.Session)
	}
	if st.VerifiedSHA == nil || st.GoalsCheckedSHA == nil {
		t.Fatalf("the run must be verified and its goals checked: %v %v", st.VerifiedSHA, st.GoalsCheckedSHA)
	}
	if st.Disposition == nil || st.Disposition.Choice != "keep" || !st.Disposition.Applied {
		t.Fatalf("disposition = %+v, want an applied keep", st.Disposition)
	}
	for _, tk := range st.Tasks {
		if tk.Status != bundle.StatusDone {
			t.Fatalf("task %d is %s: %s", tk.ID, tk.Status, tk.LastDigest)
		}
		// The class is pinned to haiku and the hook runs the op's model, so
		// the model the run recorded is the model that really wrote the
		// code. (A task that needed a second attempt escalates one tier —
		// escalate_on_retry — and would fail here, loudly and correctly.)
		if m := recordedModel(t, tk); m != liveModel {
			t.Errorf("task %d recorded model %q, want %q", tk.ID, m, liveModel)
		}
	}
	if want := []string{liveModel, liveModel}; !slices.Equal(ls.models, want) {
		t.Errorf("the live implementers ran on %v, want %v", ls.models, want)
	}

	t.Logf("git log:\n%s", testutil.Git(t, root, "log", "--oneline"))
	assertWaveCommit(t, root, "wave 0 — tasks 1", "greet.go")
	assertWaveCommit(t, root, "wave 1 — tasks 2", "greet_test.go")
	assertRealReviewers(t, bdir)
	assertGoTestPasses(t, root)
	if status := testutil.Git(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("tree not clean: %q", status)
	}
}

// skipUnlessE2E skips, naming e2eEnvVar, unless it is set to "1".
func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if os.Getenv(e2eEnvVar) != "1" {
		t.Skipf("skipping the live end-to-end run: set %s=1 to run it", e2eEnvVar)
	}
}

// liveRepo builds the throwaway repository the run drives — a real Go module
// with an empty main and the live reviewer chain in .takt.json — and
// initialises the run in it.
func liveRepo(t *testing.T) (string, string) {
	t.Helper()
	root := testutil.NewRepo(t)
	testutil.WriteFile(t, root, "go.mod", liveGoMod)
	testutil.WriteFile(t, root, "main.go", liveMain)
	testutil.WriteFile(t, root, ".gitignore", liveGitignore)
	testutil.WriteFile(t, root, ".takt.json", liveCfg)
	testutil.Commit(t, root, "the module the run adds a greeting to")
	if code, _, errb := runIn(t, root, nil, "init", "--slug", "demo", "Add a greeting"); code != 0 {
		t.Fatalf("takt init: %s", errb)
	}
	return root, filepath.Join(root, "docs", "takt", "demo")
}

// playLive drives the loop the way the session owning this run would: it
// writes the real spec at the brainstorm step, plays the stand-in planner,
// answers each gate the way the brief says to, and hands everything else —
// the wave dispatches, the exec steps, the retro, the goal assessment — to
// the driver's own scripted handling. It returns the stop reason.
func playLive(t *testing.T, d *driver, ls *liveSession) string {
	t.Helper()
	for range e2eMaxSteps {
		o := d.nextOp()
		switch {
		case o["op"] == "run" && o["step"] == "brainstorm":
			in, ok := o["inputs"].(map[string]any)
			if !ok {
				t.Fatalf("run op without inputs: %v", o)
			}
			writeAt(t, in["spec_path"].(string), liveSpec)
			if code, _, errb := d.cmd("done", "--step", "brainstorm", "--slug", "demo"); code != 0 {
				t.Fatalf("done brainstorm: %s", errb)
			}
		case o["op"] == "dispatch" && agentsOf(t, o)[0]["agent"] == "planner":
			livePlanner(t, d)
		case o["op"] == "ask":
			liveAnswer(t, d, o, ls)
		default:
			if reason, stopped := d.step(o); stopped {
				return reason
			}
		}
	}
	t.Fatalf("the live run did not finish in %d ops: %v", e2eMaxSteps, d.ops)
	return ""
}

// livePlanner writes the fixture's two-wave plan and records it, so takt
// validates the index itself. The planner is a stand-in — the implementers
// and the reviewer are what this test is about — but nothing downstream of
// it is: this is the index the plan gate reviews and the one the wave
// dispatch renders its briefs from.
func livePlanner(t *testing.T, d *driver) {
	t.Helper()
	testutil.WriteFile(t, d.root, "docs/takt/demo/plan.md", livePlanMD)
	testutil.WriteFile(t, d.root, "docs/takt/demo/plan.index.json",
		strings.Replace(livePlanIndex, "%s", specHash(t, d.bdir), 1))
	code, out, errb := d.cmd("record", "--agent", "planner",
		"--from", d.message("wrote the plan\n"), "--slug", "demo")
	if code != 0 || out["valid"] != true {
		t.Fatalf("record planner: %d %v %s", code, out, errb)
	}
}

// liveAnswer answers one gate. Four are expected on a healthy run and each
// is answered the way this run's owner would: the auditor's clauses are
// confirmed, a reviewer that asks for rework on the fixture is overridden
// with a reason (its judgment is about the fixture, not about takt, and the
// override is exactly what the gate offers for it), a failed wave is retried
// once, and the branch is kept rather than pushed. Anything else is a real
// integration failure between takt and a live agent, and fails the test with
// what the gate knows.
func liveAnswer(t *testing.T, d *driver, o map[string]any, ls *liveSession) {
	t.Helper()
	switch o["gate"] {
	case "alignment_confirm":
		d.answer(o) // the recommended option: confirm
	case "gate_review":
		t.Logf("the live reviewer asked for rework, overriding: %v", o["context"])
		if code, _, errb := d.cmd("answer", "--gate", "gate_review", "--choice", "accept",
			"--reason", "live end-to-end fixture: proceeding on the reviewer's findings",
			"--slug", "demo"); code != 0 {
			t.Fatalf("answer gate_review=accept: %s", errb)
		}
	case "wave_failures":
		ls.retries++
		if ls.retries > 1 {
			t.Fatalf("wave %v failed twice: %v\n%s\n%s",
				o["context"], o["question"], ls.transcript(), waveEvidence(t, d.bdir))
		}
		t.Logf("retrying a failed wave once: %v\n%s", o["question"], ls.transcript())
		d.answer(o) // the recommended option: retry
	case "branch_finish":
		if code, _, errb := d.cmd("answer", "--gate", "branch_finish",
			"--choice", "keep", "--slug", "demo"); code != 0 {
			t.Fatalf("answer branch_finish=keep: %s", errb)
		}
	default:
		t.Fatalf("unexpected gate %v: %v\n%s\n%s",
			o["gate"], o, ls.transcript(), waveEvidence(t, d.bdir))
	}
}

// waveEvidence is every close record and digest the run has written, for the
// failure message of a gate that should not have been reached.
func waveEvidence(t *testing.T, bdir string) string {
	t.Helper()
	var b strings.Builder
	waves := filepath.Join(bdir, "waves")
	err := filepath.WalkDir(waves, func(p string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(bdir, p)
		fmt.Fprintf(&b, "=== %s ===\n%s\n", rel, body)
		return nil
	})
	if err != nil {
		fmt.Fprintf(&b, "(reading %s: %v)\n", waves, err)
	}
	return b.String()
}

// runImplementer is the live hook: Claude Code, headless, on the model the
// dispatch op picked for this task, working in the repository the run is
// driving. The brief takt rendered is the whole
// prompt and already ends with the STATUS / SUMMARY / BLOCKERS trailer
// `takt record` parses, so stdout goes back as the agent's final message
// with nothing added — but only when the run exited 0. Prompt, stdout and
// stderr are written to logDir (see liveLogDir: a directory named by
// e2eLogDirEnvVar outlives the test, a t.TempDir does not) and the whole
// message is on the test log at every non-happy path, so a failure is
// diagnosable from `go test -v` output alone.
func runImplementer(t *testing.T, logDir string, n int, brief, repo, model string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), implementerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", brief,
		"--model", model,
		"--permission-mode", "acceptEdits",
		"--allowedTools", "Read,Edit,Write,Bash,Grep,Glob",
		"--no-session-persistence",
		"--output-format", "text")
	cmd.Dir = repo
	cmd.WaitDelay = implementerKillDelay
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	for ext, body := range map[string]string{"prompt": brief, "stdout": out.String(), "stderr": errb.String()} {
		writeAt(t, filepath.Join(logDir, fmt.Sprintf("implementer-%d.%s", n, ext)), body)
	}
	t.Logf("implementer %d on %s: %s, exit %v, %d bytes of stdout, %s",
		n, model, elapsed.Round(time.Second), err, out.Len(), reportLine(out.String()))

	// Any non-zero exit — a deadline the context killed, a crash, a refusal —
	// means what came back is not an agent's final message but whatever it
	// had written when it died. Recording that would put the failure off
	// until parseReport found no STATUS line and blame the wrong thing, so
	// it is reported here, with the reason and the output that came with it.
	if err != nil {
		// Whether the deadline is what killed it is the first thing a reader
		// needs, and cmd.Run's error alone does not say: a context kill and
		// a plain non-zero exit both arrive as an *exec.ExitError.
		why := "no deadline fired"
		if cerr := ctx.Err(); cerr != nil {
			why = cerr.Error()
		}
		return "", fmt.Errorf("claude -p on %s: %w (%s; stderr: %s; stdout: %s)",
			model, err, why, strings.TrimSpace(errb.String()), tailOfOutput(out.String()))
	}
	if out.Len() == 0 {
		return "", errors.New("claude -p exited 0 and wrote nothing; stderr: " + strings.TrimSpace(errb.String()))
	}
	// A clean run reports done. Anything else — failed, blocked, or no
	// trailer at all — is what the rest of the run will be about, so the
	// whole message goes on the log now rather than being summarised into a
	// line nobody can act on.
	if reportLine(out.String()) != doneReport {
		t.Logf("implementer %d did not report done; its final message verbatim:\n%s", n, out.String())
	}
	return out.String(), nil
}

// liveLogDir is where the live agents' prompts and output are kept: the
// directory e2eLogDirEnvVar names, created if it is not there, else a
// t.TempDir — which the framework removes when the test ends, so a run that
// has to leave evidence behind names one.
func liveLogDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(e2eLogDirEnvVar)
	if dir == "" {
		return t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("%s=%s: %v", e2eLogDirEnvVar, dir, err)
	}
	return dir
}

// tailOfOutput bounds an agent's output for a failure message; the whole of
// it is on the test log and in the log directory either way.
func tailOfOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= outputTailBudget {
		return s
	}
	return "…" + s[len(s)-outputTailBudget:]
}

// reportLine is the last STATUS line of an agent's final message, for the
// test log; the parse takt itself does is in cmd_record.go.
func reportLine(msg string) string {
	last := "no STATUS line"
	for ln := range strings.SplitSeq(msg, "\n") {
		if s := strings.TrimSpace(ln); strings.HasPrefix(s, "STATUS:") {
			last = s
		}
	}
	return last
}

// assertWaveCommit checks the wave commit whose subject contains want exists
// and touched exactly one file outside takt's own bundle tree: the one the
// plan declared. A live agent that wrote somewhere else has either been
// reverted by the scope check (D6) or is a hole in it, and both are what
// this asserts.
func assertWaveCommit(t *testing.T, root, want, declared string) {
	t.Helper()
	sha := testutil.Git(t, root, "log", "-1", "--format=%H", "-F", "--grep="+want)
	if sha == "" {
		t.Fatalf("no commit for %q in:\n%s", want, testutil.Git(t, root, "log", "--oneline"))
	}
	var code []string
	for f := range strings.SplitSeq(testutil.Git(t, root, "show", "--name-only", "--format=", sha), "\n") {
		f = strings.TrimSpace(f)
		if f == "" || strings.HasPrefix(f, "docs/takt/") {
			continue
		}
		code = append(code, f)
	}
	if len(code) != 1 || code[0] != declared {
		t.Errorf("commit %q touched %v outside the bundle, want exactly [%s]", want, code, declared)
	}
}

// assertRealReviewers checks that every review receipt this run took names a
// backend that really ran — the whole point of the live gate — and that each
// task of each wave has its own findings file naming one too.
func assertRealReviewers(t *testing.T, bdir string) {
	t.Helper()
	for _, g := range []string{gate.Spec, gate.Plan} {
		r, err := gate.ReadReceipt(bdir, g)
		if err != nil || r == nil {
			t.Fatalf("gates/%s.json: %v", g, err)
		}
		if !isLiveProvider(r.Reviewer.Provider) {
			t.Errorf("gates/%s.json provider = %q, want one of %v", g, r.Reviewer.Provider, liveProviders)
		}
		t.Logf("gate %s: %s by %s/%s", g, r.Verdict, r.Reviewer.Provider, r.Reviewer.Model)
	}
	found, err := filepath.Glob(filepath.Join(bdir, "reviews", "wave-*", "task-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Errorf("per-task reviews = %v, want one for each of the two tasks", found)
	}
	for _, p := range found {
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatal(rerr)
		}
		// writeFindings ends a findings file with "_<provider> / <model>_".
		if !hasLiveProvider(string(b)) {
			t.Errorf("%s names no real reviewer:\n%s", p, b)
		}
		t.Logf("%s:\n%s", filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p), b)
	}
}

// isLiveProvider reports whether name is a backend that shells out.
func isLiveProvider(name string) bool {
	return slices.Contains(liveProviders, name)
}

// hasLiveProvider reports whether a findings file's provenance line names a
// backend that shells out.
func hasLiveProvider(findings string) bool {
	for _, p := range liveProviders {
		if strings.Contains(findings, "_"+p+" / ") {
			return true
		}
	}
	return false
}

// assertGoTestPasses runs the module's own tests in the finished repository:
// the run's whole point was a function and a test for it, and this is the
// evidence goals.md names, taken independently of takt.
func assertGoTestPasses(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), goTestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = root
	cmd.WaitDelay = implementerKillDelay
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./... in the finished repo: %v\n%s", err, out)
	}
	t.Logf("go test ./... in the finished repo:\n%s", out)
}
