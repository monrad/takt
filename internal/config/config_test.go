package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	d := config.Defaults()
	if d.Dir != "docs/takt" || d.Autonomy != "auto" || d.MaxParallel != 8 || d.MaxRework != 1 ||
		d.MaxFilesPerTask != 12 {
		t.Fatalf("defaults = %+v", d)
	}
	if !d.Review.Spec || !d.Review.Plan || !d.Review.Tasks || !d.Goals || !d.Alignment {
		t.Fatalf("gates must default on: %+v", d)
	}
	if time.Duration(d.WaveStaleAfter) != 30*time.Minute || time.Duration(d.LockTTL) != 10*time.Minute ||
		time.Duration(d.VerifyTimeout) != 10*time.Minute {
		t.Fatalf("durations = %+v", d)
	}
	if d.Agents.Planner.Model != "fable" || d.Agents.Implementer.Model != "opus" ||
		d.Agents.Implementer.ByClass["mechanical"] != "haiku" {
		t.Fatalf("agent models = %+v", d.Agents)
	}
	if len(d.Backends.Reviewer) != 2 || d.Backends.Reviewer[0] != "copilot" {
		t.Fatalf("reviewer chain = %v", d.Backends.Reviewer)
	}
}

func TestLoadPrecedence(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := t.TempDir()
	write(
		t,
		filepath.Join(home, ".config", "takt", "config.json"),
		`{"max_parallel": 2, "backends": {"copilot": {"model": "gpt-user"}}, "agents": {"implementer": {"by_class": {"docs": "haiku"}}}}`,
	)
	write(t, filepath.Join(repo, ".takt.json"),
		`{"dir": "plans", "max_parallel": 4, "wave_stale_after": "5m"}`)

	cfg, sources, err := config.Load(repo, home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %v", sources)
	}
	if cfg.Dir != "plans" {
		t.Errorf("repo file must win for dir: %q", cfg.Dir)
	}
	if cfg.MaxParallel != 4 {
		t.Errorf("repo file must win for max_parallel: %d", cfg.MaxParallel)
	}
	if cfg.Backends.Copilot.Model != "gpt-user" {
		t.Errorf("user file value must survive: %q", cfg.Backends.Copilot.Model)
	}
	if cfg.Backends.Copilot.Effort != "high" {
		t.Errorf("unset fields keep defaults: %q", cfg.Backends.Copilot.Effort)
	}
	if cfg.Agents.Implementer.ByClass["docs"] != "haiku" || cfg.Agents.Implementer.ByClass["mechanical"] != "haiku" {
		t.Errorf("by_class must merge by key: %v", cfg.Agents.Implementer.ByClass)
	}
	if time.Duration(cfg.WaveStaleAfter) != 5*time.Minute {
		t.Errorf("duration string not parsed: %v", cfg.WaveStaleAfter)
	}
}

func TestLoadTaktConfigEnvReplacesRepoFile(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".takt.json"), `{"max_parallel": 4}`)
	alt := filepath.Join(t.TempDir(), "alt.json")
	write(t, alt, `{"max_parallel": 6}`)
	cfg, _, err := config.Load(repo, t.TempDir(), func(k string) string {
		if k == "TAKT_CONFIG" {
			return alt
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != 6 {
		t.Fatalf("TAKT_CONFIG must replace the repo file: %d", cfg.MaxParallel)
	}
}

func TestLoadRejectsBadJSONAndBadDuration(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".takt.json"), `{"max_parallel": `)
	if _, _, err := config.Load(repo, t.TempDir(), func(string) string { return "" }); err == nil {
		t.Fatal("expected a parse error")
	}
	write(t, filepath.Join(repo, ".takt.json"), `{"lock_ttl": "soon"}`)
	if _, _, err := config.Load(repo, t.TempDir(), func(string) string { return "" }); err == nil {
		t.Fatal("expected a duration error")
	}
}

func TestValidateRejectsUnknownAutonomyAndClass(t *testing.T) {
	t.Parallel()
	c := config.Defaults()
	c.Autonomy = "yolo"
	if err := c.Validate(); err == nil {
		t.Fatal("autonomy must be auto|step")
	}
	c = config.Defaults()
	c.Agents.Implementer.ByClass["weird"] = "haiku"
	if err := c.Validate(); err == nil {
		t.Fatal("unknown task class must be rejected")
	}
}
