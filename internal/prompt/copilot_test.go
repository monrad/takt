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
