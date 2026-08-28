package prompt_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/hosts"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/prompt"
)

const skillPath = "../../hosts/copilot/skills/takt/SKILL.md"

func TestCopilotSkillNamesEverythingTheBinaryCanEmit(t *testing.T) {
	t.Parallel()
	md, err := prompt.Load(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	table := prompt.Section(md, "The op table")
	for _, k := range op.Kinds() {
		mustContain(t, table, "**`"+string(k)+"`**", "op kind "+string(k)+" missing from the op table")
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
		mustContain(t, table, "`takt "+c, "exec command "+c+" missing")
	}
	for _, r := range v.StopReasons {
		mustContain(t, table, "`"+r+"`", "stop reason "+r+" missing")
	}
	for _, name := range taktCommandsNamed(md) {
		if !slices.Contains(cli.Commands(), name) {
			t.Errorf("the skill names `takt %s`, which the binary does not have", name)
		}
	}
	for _, forbidden := range []string{"AskUserQuestion", "subagent_type", "CLAUDE_PLUGIN_ROOT", "superpowers:"} {
		if strings.Contains(md, forbidden) {
			t.Errorf("the Copilot skill must not lean on Claude Code's %q", forbidden)
		}
	}
	for _, want := range []string{"ask_user", "takt-<agent>", "state.json", "events.jsonl", "git add -A", "non-zero exit"} {
		mustContain(t, md, want, want+" missing from the skill")
	}
}

func TestCopilotSkillHandshakeMatchesTheManifest(t *testing.T) {
	t.Parallel()
	md, err := prompt.Load(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("../../.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	mustContain(t, md, "takt version --expect "+m.Version, "the skill's handshake must pin the manifest version")
}

// TestCopilotAgentsAreGeneratedFromTheClaudeCodeAgents checks the two
// directions separately, because they fail differently. Every source must
// have produced its destination byte for byte — that is staleness. And every
// destination must have a source: renaming or deleting an agent leaves the
// old .agent.md in place, and the host goes on loading a definition nothing
// in the repository produces any more, which no content comparison would
// ever visit.
func TestCopilotAgentsAreGeneratedFromTheClaudeCodeAgents(t *testing.T) {
	t.Parallel()
	const dstDir = "../../hosts/copilot/agents"
	srcs, gerr := filepath.Glob("../../agents/*.md")
	if gerr != nil || len(srcs) == 0 {
		t.Fatal("no agents found", gerr)
	}
	generated := make(map[string]bool, len(srcs))
	for _, src := range srcs {
		name := strings.TrimSuffix(filepath.Base(src), ".md")
		in, _ := os.ReadFile(src)
		want, err := hosts.RenderCopilotAgent(src, name, in)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dstDir, hosts.CopilotAgentName(name)+".agent.md")
		generated[dst] = true
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("%s: %v — run `task hosts:gen`", dst, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is stale — run `task hosts:gen`", dst)
		}
	}
	dsts, err := filepath.Glob(filepath.Join(dstDir, "*.agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dst := range dsts {
		if !generated[dst] {
			t.Errorf("%s has no agents/*.md source — run `task hosts:gen` to sweep it", dst)
		}
	}
}

// yamlIndicators are the characters that make a plain YAML scalar something
// other than a string — a flow collection, an alias, an anchor, a tag, a
// block scalar, a directive — so a value opening with one has to be quoted.
// The backtick is reserved by YAML for future use and is refused outright.
const yamlIndicators = "[{*&!|>%@`"

// TestCopilotHostFrontmatterIsParseable is the guard the op-table parity
// tests do not give: a host file whose frontmatter the host's YAML parser
// rejects never reaches the loop at all, however faithful its op table is.
// The skill shipped with `"takt: <topic>"` inside an unquoted description —
// a ": " in a plain scalar is a nested mapping in disguise (YAML 1.2 §7.3.3)
// and made the whole file unloadable. Rather than take a YAML dependency for
// five files, hold every value to what a plain scalar may hold: quote it, or
// keep the indicators and ": " out of it.
func TestCopilotHostFrontmatterIsParseable(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../../hosts/copilot/agents/*.agent.md")
	if err != nil || len(files) == 0 {
		t.Fatal("no generated agents found", err)
	}
	files = append(files, skillPath)
	for _, f := range files {
		md, lerr := prompt.Load(f)
		if lerr != nil {
			t.Fatal(lerr)
		}
		lines := frontmatterLines(md)
		if len(lines) == 0 {
			t.Errorf("%s: no frontmatter block", f)
			continue
		}
		for _, ln := range lines {
			key, value, ok := strings.Cut(ln, ":")
			if !ok {
				t.Errorf("%s: frontmatter line %q is not `key: value`", f, ln)
				continue
			}
			if problem := plainScalarProblem(strings.TrimSpace(value)); problem != "" {
				t.Errorf("%s: %s: %s", f, strings.TrimSpace(key), problem)
			}
		}
	}
}

// frontmatterLines returns the raw lines between md's first two `---` lines.
// prompt.Frontmatter cannot serve here: it hands back parsed values with one
// layer of quotes stripped, and the quoting is what this test judges.
func frontmatterLines(md string) []string {
	var out []string
	seen := 0
	for line := range strings.SplitSeq(md, "\n") {
		if strings.TrimSpace(line) == "---" {
			seen++
			if seen == 2 {
				return out
			}
			continue
		}
		if seen == 1 {
			out = append(out, line)
		}
	}
	return nil
}

// plainScalarProblem reports why value would not survive a YAML parse, or ""
// when it is safe. A flow sequence carries its own delimiters and is taken
// as it is; a quoted scalar carries its own too, but only until one of them
// turns up unescaped in the body (quotedScalarProblem); anything else is a
// plain scalar, so it may not open with an indicator or contain ": ".
func plainScalarProblem(value string) string {
	if value == "" {
		return ""
	}
	if first, last := value[0], value[len(value)-1]; len(value) >= 2 {
		if (first == '\'' || first == '"') && first == last {
			return quotedScalarProblem(value)
		}
		if first == '[' && last == ']' {
			return ""
		}
	}
	if strings.IndexByte(yamlIndicators, value[0]) >= 0 {
		return "value opens with the YAML indicator " + string(value[0]) + " unquoted: " + value
	}
	if strings.Contains(value, ": ") {
		return `value holds ": " unquoted, which YAML reads as a nested mapping: ` + value
	}
	return ""
}

// quotedScalarProblem reports a delimiter inside a quoted scalar's body that
// nothing escapes — the one way a value that carries its own quotes can
// still end early, taking the rest of the line with it as garbage. YAML's
// own doubling escape (two apostrophes inside a single-quoted scalar) is
// legal and refused here all the same: nothing takt generates needs it,
// and a description that wants an apostrophe can be double-quoted.
//
// A double-quoted body escapes its closing quote with a literal backslash,
// but a backslash is itself escaped by a preceding backslash — so what
// makes the quote escaped is the *parity* of the run of backslashes right
// before it, not merely whether the one immediately before it is a
// backslash. An odd run (…\\\") escapes the quote; an even run (…\\\\")
// does not, because every backslash in the run pairs off with its
// neighbour and the quote is unescaped and closes the scalar early.
func quotedScalarProblem(value string) string {
	q := value[0]
	body := value[1 : len(value)-1]
	for i := range len(body) {
		if body[i] != q {
			continue
		}
		if q == '"' {
			run := 0
			for j := i - 1; j >= 0 && body[j] == '\\'; j-- {
				run++
			}
			if run%2 == 1 {
				continue
			}
		}
		return "value closes its " + string(q) + " quote early, at byte " +
			strconv.Itoa(i+1) + ": " + value
	}
	return ""
}

// TestPlainScalarProblem drives the judgment the host files themselves
// cannot: they are all valid, so every rejecting branch is unreachable
// through TestCopilotHostFrontmatterIsParseable.
func TestPlainScalarProblem(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ value, want string }{
		{"", ""},
		{"takt-planner", ""},
		{`"a quoted: value"`, ""},
		{`'a quoted: value'`, ""},
		{`["*"]`, ""},
		{`"an \" escaped quote"`, ""},
		{"takt: a topic", `value holds ": " unquoted`},
		{"[not, closed", "value opens with the YAML indicator ["},
		{`'it's mine'`, "value closes its ' quote early"},
		{`"he said "hi""`, `value closes its " quote early`},
	} {
		got := plainScalarProblem(c.value)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("plainScalarProblem(%q) = %q, want %q", c.value, got, c.want)
		}
	}
}

// TestQuotedScalarProblemBackslashRuns drives quotedScalarProblem directly
// with runs of backslashes in front of a double quote, rather than through
// plainScalarProblem: a one-byte look-back only ever sees "the byte right
// before the quote is a backslash" and cannot tell a single escaping
// backslash from the second half of an escaped backslash, so the case that
// matters is a *run*, not a single byte. Each value below is a raw string
// literal, so every backslash in it is a literal byte in the YAML body —
// none of them are Go escapes.
func TestQuotedScalarProblemBackslashRuns(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, value, want string
	}{
		{"one backslash (odd) escapes the quote", `"a\"b"`, ""},
		{"two backslashes (even) do not escape the quote", `"a\\"b"`, `value closes its " quote early, at byte 4`},
		{"three backslashes (odd) escape the quote", `"a\\\"b"`, ""},
		{"four backslashes (even) do not escape the quote", `"a\\\\"b"`, `value closes its " quote early, at byte 6`},
		{"a run at the very start of the body is even (zero)", `"\\"b"`, `value closes its " quote early, at byte 3`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := quotedScalarProblem(c.value)
			if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
				t.Errorf("quotedScalarProblem(%q) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}
