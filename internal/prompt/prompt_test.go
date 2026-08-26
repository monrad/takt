package prompt_test

import (
	"regexp"
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
		if !opBullet(string(k)).MatchString(table) {
			t.Errorf("op kind %s has no bullet of its own in the op table "+
				"(looked for a line %s)", k, opBulletForm(string(k)))
		}
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

// opBulletForm is the shape the op table writes one kind's row in: a
// top-level list item opening with the kind as bold code. The table is a
// bullet list rather than a markdown table (task-2 ruling), so this is what
// "one row per op kind" means here.
func opBulletForm(kind string) string { return "- **`" + kind + "`**" }

// opBullet matches [opBulletForm] at the start of a line. Matching the row
// rather than the bare word is the whole point of the assertion: `stop` and
// `ask` are named in half the other bullets' prose, so a kind whose own row
// was deleted — or written without its code span, which is how the prompt's
// reader finds it — would still "appear" in the table.
func opBullet(kind string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- \*\*` + "`" + regexp.QuoteMeta(kind) + "`" + `\*\*`)
}

// mustContain reports what unless text holds needle.
func mustContain(t *testing.T, text, needle, what string) {
	t.Helper()
	if !strings.Contains(text, needle) {
		t.Errorf("%s (looked for %q)", what, needle)
	}
}

// TestFrontmatter covers two edge cases the agent parity test never
// exercises: a quoted value, which must come back with its surrounding
// quotes stripped, and a value that itself contains ": ", which must split
// only on the first colon.
func TestFrontmatter(t *testing.T) {
	t.Parallel()
	md := "---\n" +
		"name: \"quoted-name\"\n" +
		"description:  Turns X into Y: a colon inside the value  \n" +
		"---\nbody\n"
	fm, err := prompt.Frontmatter(md)
	if err != nil {
		t.Fatal(err)
	}
	if fm["name"] != "quoted-name" {
		t.Errorf("surrounding quotes not stripped: %q", fm["name"])
	}
	if want := "Turns X into Y: a colon inside the value"; fm["description"] != want {
		t.Errorf("value with an embedded colon split wrong: got %q, want %q", fm["description"], want)
	}
}
