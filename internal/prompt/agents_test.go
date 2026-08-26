package prompt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/prompt"
)

var wantAgents = map[string]struct{ model, tools string }{
	"implementer":       {"sonnet", "Read, Edit, Write, Bash, Grep, Glob"},
	"planner":           {"fable", "Read, Grep, Glob, Write"},
	"goal-assessor":     {"sonnet", "Read, Grep, Glob, Bash"},
	"alignment-auditor": {"sonnet", "Read, Grep, Glob"},
}

func TestAgentDefinitionsMatchSpec(t *testing.T) {
	t.Parallel()
	for name, want := range wantAgents {
		b, err := os.ReadFile(filepath.Join("..", "..", "agents", name+".md"))
		if err != nil {
			t.Fatalf("agents/%s.md: %v", name, err)
		}
		fm, err := prompt.Frontmatter(string(b))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fm["name"] != name || fm["model"] != want.model || fm["tools"] != want.tools || fm["description"] == "" {
			t.Errorf("%s frontmatter %v, want model %s tools %q", name, fm, want.model, want.tools)
		}
		body := string(b)
		for _, must := range []string{"brief", "quoted", "never commit"} {
			if !strings.Contains(strings.ToLower(body), must) {
				t.Errorf("%s body lacks %q", name, must)
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join("..", "..", "agents"))
	if len(entries) != len(wantAgents) {
		t.Errorf("agents/ has %d files, want %d", len(entries), len(wantAgents))
	}
}
