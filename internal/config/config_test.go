package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestLoadRejectsMissingTaktConfig covers review finding 7: the implicit
// layers may be absent, but an explicit override may not — silently ignoring
// a TAKT_CONFIG typo means the run is configured by something other than
// what the user pointed at.
func TestLoadRejectsMissingTaktConfig(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "typo.json")
	_, _, err := config.Load(t.TempDir(), t.TempDir(), func(k string) string {
		if k == "TAKT_CONFIG" {
			return missing
		}
		return ""
	})
	if err == nil {
		t.Fatal("a TAKT_CONFIG pointing at a missing file must be an error")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "TAKT_CONFIG") {
		t.Fatalf("error must name the path and the variable: %v", err)
	}
}

// TestLoadAllowsMissingImplicitLayers keeps the check above from turning an
// ordinary repo with no config files into an error.
func TestLoadAllowsMissingImplicitLayers(t *testing.T) {
	t.Parallel()
	cfg, sources, err := config.Load(t.TempDir(), t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 0 || cfg.MaxParallel != 8 {
		t.Fatalf("sources = %v, cfg = %+v", sources, cfg)
	}
}

// TestValidateRejectsNonPositiveDurations covers the three durations a run
// divides by zero on: a lock that expires the instant it is taken, a wave
// stale before it is dispatched, a verify command with no time to run. Each
// is rejected by name, so the user is told which key in which file is wrong.
func TestValidateRejectsNonPositiveDurations(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"lock_ttl", "wave_stale_after", "verify_timeout"} {
		c := config.Defaults()
		js := fmt.Sprintf(`{"%s":"0s"}`, field)
		if err := json.Unmarshal([]byte(js), &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("%s = 0 must be rejected by name: %v", field, err)
		}
	}
}

func TestDefaultsIncludeTheSixLensesAndTheReviewerModel(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	want := []string{"correctness", "intent", "tests", "simplicity", "consistency", "docs"}
	if !slices.Equal(cfg.Review.Lenses, want) {
		t.Fatalf("Review.Lenses = %v, want %v", cfg.Review.Lenses, want)
	}
	if cfg.Agents.Reviewer.Model != "sonnet" {
		t.Fatalf("Agents.Reviewer.Model = %q, want sonnet", cfg.Agents.Reviewer.Model)
	}
}

func TestValidateRejectsUnknownAndDuplicateLenses(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Review.Lenses = []string{"correctness", "nope"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown lens not rejected: %v", err)
	}
	cfg.Review.Lenses = []string{"correctness", "correctness"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "correctness") {
		t.Fatalf("duplicate lens not rejected: %v", err)
	}
	cfg.Review.Lenses = nil // empty means the internal layer is off — valid
	if err := cfg.Validate(); err != nil {
		t.Fatalf("empty lens list must be valid: %v", err)
	}
}

// TestDurationMarshalsShortForm keeps a config takt writes back readable:
// [time.Duration.String] always spells the zero tail out ("30m0s"), which is
// not what anyone types into .takt.json.
func TestDurationMarshalsShortForm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{d: 30 * time.Minute, want: `"30m"`},
		{d: time.Hour, want: `"1h"`},
		{d: 90 * time.Second, want: `"1m30s"`},
		{d: 90 * time.Minute, want: `"1h30m"`},
	} {
		b, err := json.Marshal(config.Duration(tc.d))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Fatalf("%s marshalled as %s, want %s", tc.d, b, tc.want)
		}
	}
}
