package prompt_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestCopilotAgentsAreGeneratedFromTheClaudeCodeAgents(t *testing.T) {
	t.Parallel()
	srcs, gerr := filepath.Glob("../../agents/*.md")
	if gerr != nil || len(srcs) == 0 {
		t.Fatal("no agents found", gerr)
	}
	for _, src := range srcs {
		name := strings.TrimSuffix(filepath.Base(src), ".md")
		in, _ := os.ReadFile(src)
		want, err := hosts.RenderCopilotAgent(name, in)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join("../../hosts/copilot/agents", hosts.CopilotAgentName(name)+".agent.md")
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("%s: %v — run `task hosts:gen`", dst, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is stale — run `task hosts:gen`", dst)
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
// when it is safe. A quoted scalar and a flow sequence carry their own
// delimiters and are taken as they are; anything else is a plain scalar, so
// it may not open with an indicator or contain ": ".
func plainScalarProblem(value string) string {
	if value == "" {
		return ""
	}
	if first, last := value[0], value[len(value)-1]; len(value) >= 2 {
		if (first == '\'' || first == '"') && first == last {
			return ""
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
