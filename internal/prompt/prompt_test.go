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

// promptCmd is how the prompt writes a takt invocation: a code span opening
// with the binary's name. Splitting a line on it yields one segment per
// command the line names.
const promptCmd = "`takt "

// handshakeLine is the exact command the prompt's Handshake section must
// run: the plugin/binary version check (spec §6).
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
	md, err := prompt.Load(promptPath)
	if err != nil {
		t.Fatal(err)
	}
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

// crossHostInvariants are the sentences that must read identically in the
// Claude prompt and the Copilot skill: the two are separate files describing
// one contract, and nothing enforces that an edit to one lands in the other.
// Anchoring on the phrases the two files share today means an edit that
// drifts either copy — even a rewording that keeps the same meaning — fails
// this test, which is the point: the wording itself is the shared contract.
var crossHostInvariants = []string{
	// the owner-gate exception (op table, `ask` bullet)
	"The `owner` gate is the exception: its `answer` clears nothing and only prints a `hint`, so act on the choice yourself",
	// the `kept: true` rule (op table, `ask` bullet)
	"An `answer` that prints `\"kept\": true` leaves the gate open — end the turn (the user chose to stop or abort).",
	// the `git add -A` prohibition (Invariants)
	"never run `git add -A`",
	// the push-and-branch invariant's exception (Invariants, #66): the
	// clause that attaches the exception to the two ops that carry it. This
	// is the sentence that drifted — the prompts forbade the very push an
	// `archived` stop's `cleanup` hands the session — and it was anchored
	// nowhere, so the two copies could have disagreed unnoticed.
	"the `push_pr` run op, and an `archived` stop's `cleanup` commands once the user has confirmed them",
	// the push_pr command (op table, `run` bullet, #36)
	"gh pr create --base <base> --title '<title>' --body-file <path>",
	// the never-cd-into-the-bundle invariant (Invariants, #37)
	"Inspect bundle files by absolute path — never `cd` into the bundle",
	// the retro run step's skeleton-copy clause (op table, `run` bullet,
	// lets-work-on-63 #63): pins the full sentence, not just its opening
	// fragment, so the two prompts cannot drift on leaving the rendered
	// sections alone or on where the numbers live.
	"copy `inputs.skeleton_path` to `inputs.retro_path`, fill every `<!-- prose: … -->` slot, and leave the rendered sections as they are; the numbers live at `inputs.inputs_path`",
}

// TestPromptInvariantsReadTheSameOnEveryHost pins #15's third gap:
// TestPromptHandshakeVerbsAndInvariants only ever loaded the Claude prompt,
// so a sentence edited in commands/takt.md and left stale in
// hosts/copilot/skills/takt/SKILL.md (or the reverse) passed every existing
// test. Both files are loaded here and checked against the same anchors.
func TestPromptInvariantsReadTheSameOnEveryHost(t *testing.T) {
	t.Parallel()
	claude, err := prompt.Load(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	copilot, err := prompt.Load(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range crossHostInvariants {
		mustContain(t, claude, inv, "invariant missing from "+promptPath)
		mustContain(t, copilot, inv, "invariant missing from "+skillPath)
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

// TestBody covers the two shapes [prompt.Body] cannot split — a file with no
// frontmatter at all and one whose block is never closed — because both must
// come back whole rather than half-eaten: the body is what every host copies
// verbatim, so losing a line of it silently would ship a truncated agent. The
// CRLF and leading-blank-line cases pin the other half of the contract: Body
// finds a frontmatter block wherever [prompt.Frontmatter] finds one, since a
// file the parser reads as having a header and the renderer reads as having
// none would ship that header into the body as prose.
func TestBody(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, md, want string }{
		{"frontmatter", "---\nname: x\n---\n\nbody\nmore\n", "body\nmore\n"},
		{"none", "body only\n", "body only\n"},
		{"unterminated", "---\nname: x\nbody\n", "---\nname: x\nbody\n"},
		{"crlf", "---\r\nname: x\r\n---\r\n\r\nbody\r\n", "body\r\n"},
		{"leading blank line", "\n---\nname: x\n---\n\nbody\n", "body\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := prompt.Body(c.md); got != c.want {
				t.Errorf("Body(%q) = %q, want %q", c.md, got, c.want)
			}
		})
	}
}
