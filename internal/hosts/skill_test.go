package hosts_test

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/hosts"
)

// promptPath is the Claude Code prompt the skill is rendered from, relative
// to this package. The tests below drive the real file rather than a fixture
// on purpose: the profile's whole job is to match commands/takt.md as it is
// committed, and a fixture would keep passing after the prompt moved on.
const promptPath = "../../commands/takt.md"

// h1 is the one-line region the failure tests damage: short enough to be
// quoted whole in an error, and host-specific, so no shared sentence has to
// be touched to make a substitution miss.
const h1 = "# /takt — the op loop"

// TestRenderCopilotSkillRendersTheCommittedPrompt drives the profile over
// commands/takt.md as it stands. It is the case that must not error: the one
// substitution declaring a multiplicity of two finds exactly two
// occurrences, because the ask bullet's clause ran first and took the third.
// The ask_user count is that ordering, asserted — one for the ask bullet,
// one for the slug-ambiguity bullet, one for the autonomy paragraph.
func TestRenderCopilotSkillRendersTheCommittedPrompt(t *testing.T) {
	t.Parallel()
	out, err := hosts.RenderCopilotSkill(promptPath, []byte(loadPrompt(t)), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"---\nname: takt\ndescription: '",
		"# takt — the op loop (Copilot CLI host)",
		"takt version --expect 9.9.9`",
		"delegate to the custom agent named `takt-<agent>`",
		"Run `command` with the shell tool",
		"never run `git add -A`, ever.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in the rendered skill", want)
		}
	}
	for _, forbidden := range []string{"AskUserQuestion", "subagent_type", "CLAUDE_PLUGIN_ROOT", "superpowers:"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("the rendered skill still leans on Claude Code's %q", forbidden)
		}
	}
	if n := strings.Count(s, "ask_user"); n != 3 {
		t.Errorf("ask_user appears %d times, want 3 — the ask bullet plus the two the swap declares", n)
	}
}

// TestRenderCopilotSkillErrorsWhenARegionDoesNotMatchItsCount is the drift
// alarm. A substitution that no longer matches exactly as many times as it
// declares stops the render rather than silently emitting a skill that lost
// the region, and the error has to be actionable: it names the file, the
// substitution, and both counts — what was found and what was declared —
// because "a substitution failed" sends a reader looking through the profile
// by hand.
func TestRenderCopilotSkillErrorsWhenARegionDoesNotMatchItsCount(t *testing.T) {
	t.Parallel()
	md := loadPrompt(t)
	for _, tc := range []struct {
		name   string
		in     string
		counts string
	}{
		{"the region is gone", strings.Replace(md, h1, "", 1), "matched 0 times, declared 1"},
		{"the region occurs twice", md + "\n" + h1 + "\n", "matched 2 times, declared 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := hosts.RenderCopilotSkill(promptPath, []byte(tc.in), "9.9.9")
			if err == nil {
				t.Fatalf("expected an error, got %d bytes of skill", len(out))
			}
			for _, want := range []string{promptPath, strconv.Quote(h1), tc.counts} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error must name %s, got: %v", want, err)
				}
			}
		})
	}
}

// TestRenderCopilotSkillCarriesSharedProseThrough is the other half of the
// one contract: prose no substitution names is copied to the skill as it is,
// so rewording a sentence the two hosts share propagates instead of failing.
// Without this, the count check would read as "every edit to the prompt
// breaks the generator", which is the opposite of what it is for.
func TestRenderCopilotSkillCarriesSharedProseThrough(t *testing.T) {
	t.Parallel()
	const (
		shared   = "One row per op kind:"
		reworded = "One row per op kind, and every op has one:"
	)
	md := loadPrompt(t)
	if !strings.Contains(md, shared) {
		t.Fatalf("%s no longer carries %q; pick another shared sentence", promptPath, shared)
	}
	out, err := hosts.RenderCopilotSkill(promptPath, []byte(strings.Replace(md, shared, reworded, 1)), "9.9.9")
	if err != nil {
		t.Fatal("a reworded shared sentence must propagate, not fail the render:", err)
	}
	if !strings.Contains(string(out), reworded) {
		t.Errorf("the rendered skill must carry the reworded shared sentence %q", reworded)
	}
}

// loadPrompt reads commands/takt.md, the profile's only input.
func loadPrompt(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
