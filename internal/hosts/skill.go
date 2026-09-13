package hosts

import (
	"fmt"
	"strings"
)

// substitution is one region commands/takt.md and the Copilot skill word
// differently: from is the Claude text, to the Copilot text, and count how
// many times from must occur in the document at the moment this
// substitution runs.
//
// The count is the whole safety property, and it is one contract rather than
// two. A shared sentence — anything no substitution names — propagates to
// the skill silently and correctly, because the render copies through
// everything it does not replace. A host-specific sentence cannot drift the
// same way: edit or move one of the regions below and its from stops
// matching its count, so the generator fails by name instead of quietly
// emitting a skill that has lost the region.
type substitution struct {
	from  string
	to    string
	count int
}

// labelMax is how many runes of a substitution's from text an error quotes:
// enough to point at the region, short enough to stay on one line.
const labelMax = 56

// askUserQuestions is the multiplicity the tool-name swap declares: the
// three AskUserQuestion mentions commands/takt.md carries, less the one the
// ask bullet's own substitution has already taken by the time it runs.
const askUserQuestions = 2

// RenderCopilotSkill converts commands/takt.md into the Copilot CLI skill,
// hosts/copilot/skills/takt/SKILL.md. src is the path the caller actually
// read in from — named in every failure, so an error points at the file this
// process opened rather than at an assumed commands/takt.md that a --root
// may not have used. version is the plugin manifest's version: the Copilot
// host has no plugin root to read a manifest from, so its handshake pins the
// version as text.
//
// The profile is applied in order, and a from found a different number of
// times than it declares stops the render, naming the substitution and both
// counts. Nothing else is rewritten: what the profile does not name is
// copied through byte for byte.
func RenderCopilotSkill(src string, in []byte, version string) ([]byte, error) {
	out := string(in)
	for _, s := range copilotSkillProfile(version) {
		found := strings.Count(out, s.from)
		if found != s.count {
			return nil, fmt.Errorf("%s: substitution %q matched %d times, declared %d"+
				" — commands/takt.md and the copilot profile in internal/hosts/skill.go disagree",
				src, substitutionLabel(s.from), found, s.count)
		}
		out = strings.ReplaceAll(out, s.from, s.to)
	}
	return []byte(out), nil
}

// copilotSkillProfile is the ordered profile: one entry per region of spec
// §2.3's table — the frontmatter, the H1, the handshake, the verb bullets,
// four op-table clauses, the delegation invariant, and the host's name for
// the question tool.
//
// Order matters. commands/takt.md names AskUserQuestion three times, and one
// of them sits inside the ask bullet's opening clause, which the skill
// rewords for a different reason. That clause is replaced first, so the
// tool-name swap closing the profile sees exactly the two occurrences it
// declares: the slug-ambiguity bullet and the autonomy paragraph.
func copilotSkillProfile(version string) []substitution {
	return []substitution{
		// The frontmatter: a Copilot skill carries its own name, and its
		// description is what the host matches a user's "takt …" against.
		{
			from:  "description: \"Resumable brainstorm → plan → execute → finish for this repository, driven by the takt binary: /takt <topic> starts a run, /takt resumes it, /takt status|doctor|waive|unlock are the verbs.\"",
			to:    "name: takt\ndescription: 'Resumable brainstorm → plan → execute → finish for this repository, driven by the takt binary. Use when the user says \"takt\" — \"takt: <topic>\" starts a run, \"takt\" alone resumes it, and \"takt status\", \"takt doctor\", \"takt waive <N> <reason>\", \"takt unlock\" are the verbs.'",
			count: 1,
		},
		// The H1, which names the host this copy was rendered for.
		{
			from:  "# /takt — the op loop",
			to:    "# takt — the op loop (Copilot CLI host)",
			count: 1,
		},
		// The handshake: no plugin root to point --expect-manifest at, so
		// the manifest's version is injected here as text.
		{
			from: "Run `takt version --expect-manifest \"${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json\"`. If it exits non-zero, print its `hint` and stop — the binary and this plugin must be the same version (a `0.0.0-dev` binary is accepted with `\"dev\": true`).",
			to: "Run `takt version --expect " + version +
				"`. If it exits non-zero, print its `hint` and stop — the binary and this skill must be the same version (a `0.0.0-dev` development build is accepted with `\"dev\": true`, as the Claude Code handshake does).",
			count: 1,
		},
		// The verbs: Copilot has no slash commands, so the three /takt
		// bullets become the phrasings a user types in prose.
		{
			from:  "- `/takt` — resume: go to **The loop**.\n- `/takt <topic…>` — `takt init \"<topic>\"` (quote the topic verbatim; add `--slug <s>` only if the user gave one), print the JSON, then **The loop**.\n- `/takt status` → `takt status`; `/takt doctor` → `takt doctor`; `/takt waive <N> \"<reason>\"` → `takt waive --task <N> --reason \"<reason>\"`; `/takt unlock` → `takt unlock`. Print the output and stop.",
			to:    "- \"takt\" — resume: go to **The loop**.\n- \"takt: <topic…>\" — `takt init \"<topic>\"` (quote the topic verbatim; add `--slug <s>` only if the user gave one), print the JSON, then **The loop**.\n- \"takt status\" → `takt status`; \"takt doctor\" → `takt doctor`; \"takt waive <N> <reason>\" → `takt waive --task <N> --reason \"<reason>\"`; \"takt unlock\" → `takt unlock`. Print the output and stop.",
			count: 1,
		},
		// The dispatch bullet: custom agents instead of the Agent tool, and
		// a model the host treats as advisory.
		{
			from:  "- **`dispatch`** — One message with one `Agent` call per entry of `agents`: `subagent_type: \"takt:<agent>\"`, `model: <model>` (always present — never omit or change it), `run_in_background: true`, prompt = the **contents** of the file at `brief` (read it; pass the text verbatim, nothing added). Every entry's `cwd` is the repository root — the `Agent` tool has no `cwd` parameter, so a subagent inherits",
			to:    "- **`dispatch`** — For every entry of `agents`, delegate to the custom agent named `takt-<agent>` (installed from this repository's `hosts/copilot/agents/`), all entries of one op at once where the host runs subagents in parallel (fleet mode), with prompt = the **contents** of the file at `brief` (read it; pass the text verbatim, nothing added). The op's `model` is advisory on this host — Copilot picks subagent models from its `/subagents` setting; mention in the narration when it differs. Every entry's `cwd` is the repository root — a subagent inherits",
			count: 1,
		},
		// The ask bullet's opening clause. It runs before the tool-name swap
		// below and consumes the third AskUserQuestion.
		{
			from:  "- **`ask`** — `AskUserQuestion` with the op's `question` and its `options`",
			to:    "- **`ask`** — `ask_user` with the op's `question` and its `options` as the choices,",
			count: 1,
		},
		// The run bullet's brainstorm clause: no superpowers plugin on this
		// host, so the design conversation happens in the session.
		{
			from:  "`brainstorm` (invoke `superpowers:brainstorming`,",
			to:    "`brainstorm` (design with the user in this conversation, one question at a time, and",
			count: 1,
		},
		// The exec bullet's opening clause: Copilot's shell tool has no
		// background mode to wait on.
		{
			from:  "- **`exec`** — Run `command` with the Bash tool with `run_in_background: true` and wait for its completion notification",
			to:    "- **`exec`** — Run `command` with the shell tool and wait for it to finish",
			count: 1,
		},
		// The delegation invariant, which names the Agent tool's parameters.
		{
			from:  "- Every `Agent` call carries the `model` from the op and the `takt:<agent>` subagent type.",
			to:    "- Every delegation targets the `takt-<agent>` custom agent the op names, with the brief as its whole prompt.",
			count: 1,
		},
		// The question tool, in the slug-ambiguity bullet and the autonomy
		// paragraph — the two occurrences left once the ask bullet above has
		// taken the third.
		{
			from:  "AskUserQuestion",
			to:    "ask_user",
			count: askUserQuestions,
		},
	}
}

// substitutionLabel names a substitution in an error by the head of its from
// text: the profile gives its entries no other identity, and the text that
// stopped matching is the most useful thing an error can print. A multi-line
// region is quoted by its first line.
func substitutionLabel(from string) string {
	head, _, _ := strings.Cut(from, "\n")
	r := []rune(head)
	if len(r) > labelMax {
		return string(r[:labelMax]) + "…"
	}
	return head
}
