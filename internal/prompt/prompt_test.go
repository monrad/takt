package prompt_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/prompt"
)

const promptPath = "../../commands/takt.md"

// handshakeLine is the exact command the prompt's Handshake section must
// run: the plugin/binary version check (spec §6).
// promptCmd is how the prompt writes a takt invocation: a code span opening
// with the binary's name. Splitting a line on it yields one segment per
// command the line names.
const promptCmd = "`takt "

const handshakeLine = `takt version --expect-manifest "${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json"`

func TestPromptNamesEveryOpGateStepAndReason(t *testing.T) {
	t.Parallel()
	md, err := prompt.Load(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	table := prompt.Section(md, "The op table")
	for _, k := range op.Kinds() {
		mustContain(t, table, "`"+string(k)+"`", "op kind "+string(k)+" missing from the op table")
	}
	v := decide.Vocab()
	gates := prompt.Section(md, "Gates")
	for _, g := range v.Gates {
		mustContain(t, gates, "`"+g+"`", "gate "+g+" missing from the Gates section")
	}
	for _, s := range v.RunSteps {
		mustContain(t, table, "`"+s+"`", "run step "+s+" missing")
	}
	for _, c := range v.ExecCommands {
		mustContain(t, table, "`takt "+c, "exec command takt "+c+" missing")
	}
	for _, r := range v.StopReasons {
		mustContain(t, table, "`"+r+"`", "stop reason "+r+" missing")
	}
}

func TestPromptHandshakeVerbsAndInvariants(t *testing.T) {
	t.Parallel()
	md, _ := prompt.Load(promptPath)
	mustContain(t, prompt.Section(md, "Handshake"), handshakeLine, "handshake line missing or changed")
	verbs := prompt.Section(md, "Verbs")
	for _, cmd := range []string{"init", "status", "doctor", "waive", "unlock"} {
		mustContain(t, verbs, "`takt "+cmd, "verb "+cmd+" missing")
	}
	inv := prompt.Section(md, "Invariants")
	for _, must := range []string{"state.json", "events.jsonl", "git add -A", "model", "non-zero exit"} {
		mustContain(t, inv, must, "invariant about "+must+" missing")
	}
	// every takt subcommand the prompt mentions exists
	for _, name := range taktCommandsNamed(md) {
		if !slices.Contains(cli.Commands(), name) {
			t.Errorf("prompt names unknown command takt %s", name)
		}
	}
}

// taktCommandsNamed returns the subcommand of every `takt <name>` the prompt
// spells out: the first word after the token, minus the closing backtick and
// whatever sentence punctuation follows it. Placeholders (`takt <cmd>`) and
// bare flags are skipped. The prose before a line's first token is not a
// command name, so it is cut away rather than scanned.
func taktCommandsNamed(md string) []string {
	var out []string
	for line := range strings.SplitSeq(md, "\n") {
		_, rest, found := strings.Cut(line, promptCmd)
		if !found {
			continue
		}
		for tok := range strings.SplitSeq(rest, promptCmd) {
			fields := strings.Fields(tok)
			if len(fields) == 0 {
				continue
			}
			name := strings.TrimRight(fields[0], "`.,;:)")
			if name == "" || strings.HasPrefix(name, "<") || strings.HasPrefix(name, "-") {
				continue
			}
			out = append(out, name)
		}
	}
	return out
}

// mustContain reports what unless text holds needle.
func mustContain(t *testing.T, text, needle, what string) {
	t.Helper()
	if !strings.Contains(text, needle) {
		t.Errorf("%s (looked for %q)", what, needle)
	}
}
