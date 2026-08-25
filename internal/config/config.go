// Package config loads takt's layered configuration (spec §12):
// defaults ‹ ~/.config/takt/config.json ‹ <repo>/.takt.json ‹ $TAKT_CONFIG.
// Per-run values are frozen into state.json at init (spec §12), so a config
// change mid-run never changes a running bundle's behaviour.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Duration is a [time.Duration] that (un)marshals as a Go duration string.
type Duration time.Duration

// UnmarshalJSON accepts "30m", "1h30m", etc.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"30m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("bad duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON renders the duration in the short form a user would have
// typed: [time.Duration.String] always spells its zero tail out, so a config
// takt writes back would read "30m0s" where the file said "30m".
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(shortDuration(time.Duration(d)))
}

// shortDuration trims the zero tail [time.Duration.String] appends: "30m0s"
// → "30m", "1h0m0s" → "1h". A non-zero component is never trimmed, so
// "1m30s" and "1h30m0s" → "1h30m" keep everything that carries information.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Review toggles the three review gates.
type Review struct {
	Spec  bool `json:"spec"`
	Plan  bool `json:"plan"`
	Tasks bool `json:"tasks"`
}

// Backend configures one headless CLI backend.
type Backend struct {
	Model   string   `json:"model"`
	Effort  string   `json:"effort"`
	Timeout Duration `json:"timeout"`
}

// Backends lists the reviewer chain and per-backend settings.
type Backends struct {
	Reviewer []string `json:"reviewer"`
	Copilot  Backend  `json:"copilot"`
	Claude   Backend  `json:"claude"`
}

// Implementer maps task classes to models (spec D22).
type Implementer struct {
	Model           string            `json:"model"`
	ByClass         map[string]string `json:"by_class"`
	EscalateOnRetry bool              `json:"escalate_on_retry"`
}

// Agent pins the model for a single-purpose agent.
type Agent struct {
	Model string `json:"model"`
}

// Agents holds per-agent model settings.
type Agents struct {
	Implementer      Implementer `json:"implementer"`
	Planner          Agent       `json:"planner"`
	GoalAssessor     Agent       `json:"goal-assessor"`
	AlignmentAuditor Agent       `json:"alignment-auditor"`
}

// Config is the merged configuration.
type Config struct {
	Dir             string   `json:"dir"`
	Autonomy        string   `json:"autonomy"`
	Review          Review   `json:"review"`
	Goals           bool     `json:"goals"`
	Alignment       bool     `json:"alignment"`
	MaxParallel     int      `json:"max_parallel"`
	MaxRework       int      `json:"max_rework"`
	MaxFilesPerTask int      `json:"max_files_per_task"`
	WaveStaleAfter  Duration `json:"wave_stale_after"`
	LockTTL         Duration `json:"lock_ttl"`
	VerifyTimeout   Duration `json:"verify_timeout"`
	DefaultBranch   string   `json:"default_branch"`
	Backends        Backends `json:"backends"`
	Agents          Agents   `json:"agents"`
}

// TaskClasses is the closed set of plan task classes (spec §7.3).
var TaskClasses = []string{"mechanical", "bounded", "implement", "test", "docs"}

// modelSonnet names the model used by several default agent/class pins
// below; named to satisfy goconst (it recurs 5×) without changing the
// shipped value.
const modelSonnet = "sonnet"

// Shipped default values (spec §12), named to satisfy mnd.
const (
	defaultMaxParallel     = 8
	defaultMaxFilesPerTask = 12
	defaultWaveStaleAfter  = 30 * time.Minute
	defaultLockTTL         = 10 * time.Minute
	defaultVerifyTimeout   = 10 * time.Minute
	defaultBackendTimeout  = 5 * time.Minute
)

// Defaults returns the shipped defaults (spec §12).
func Defaults() Config {
	return Config{
		Dir:             "docs/takt",
		Autonomy:        "auto",
		Review:          Review{Spec: true, Plan: true, Tasks: true},
		Goals:           true,
		Alignment:       true,
		MaxParallel:     defaultMaxParallel,
		MaxRework:       1,
		MaxFilesPerTask: defaultMaxFilesPerTask,
		WaveStaleAfter:  Duration(defaultWaveStaleAfter),
		LockTTL:         Duration(defaultLockTTL),
		VerifyTimeout:   Duration(defaultVerifyTimeout),
		Backends: Backends{
			Reviewer: []string{"copilot", "claude"},
			Copilot:  Backend{Model: "gpt-5.6-sol", Effort: "high", Timeout: Duration(defaultBackendTimeout)},
			Claude:   Backend{Model: "opus", Effort: "high", Timeout: Duration(defaultBackendTimeout)},
		},
		Agents: Agents{
			Implementer: Implementer{
				Model: "opus",
				ByClass: map[string]string{
					"mechanical": "haiku",
					"bounded":    modelSonnet,
					"test":       modelSonnet,
					"docs":       modelSonnet,
				},
				EscalateOnRetry: true,
			},
			Planner:          Agent{Model: "fable"},
			GoalAssessor:     Agent{Model: modelSonnet},
			AlignmentAuditor: Agent{Model: modelSonnet},
		},
	}
}

// Load merges the config layers. sources lists files read, low→high.
// A missing implicit layer is normal and skipped; a missing $TAKT_CONFIG is
// an error, because an override the user typed must not be silently ignored
// — a typo there would otherwise configure the run from something other than
// the file they named (review finding 7).
func Load(repoRoot, home string, getenv func(string) string) (Config, []string, error) {
	cfg := Defaults()
	var sources []string
	layers := []string{filepath.Join(home, ".config", "takt", "config.json")}
	explicit := getenv("TAKT_CONFIG")
	if explicit != "" {
		layers = append(layers, explicit)
	} else {
		layers = append(layers, filepath.Join(repoRoot, ".takt.json"))
	}
	for _, p := range layers {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			if p == explicit {
				return cfg, sources, fmt.Errorf("TAKT_CONFIG points at %s, which does not exist", p)
			}
			continue
		}
		if err != nil {
			return cfg, sources, err
		}
		// Unmarshal into the existing value: only keys present override,
		// and maps merge by key — this is the whole layering mechanism.
		if uerr := json.Unmarshal(b, &cfg); uerr != nil {
			return cfg, sources, fmt.Errorf("%s: %w", p, uerr)
		}
		sources = append(sources, p)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, sources, err
	}
	return cfg, sources, nil
}

// Validate rejects values outside their closed sets.
func (c Config) Validate() error {
	if c.Autonomy != "auto" && c.Autonomy != "step" {
		return fmt.Errorf("autonomy must be auto or step, got %q", c.Autonomy)
	}
	for class := range c.Agents.Implementer.ByClass {
		if !IsTaskClass(class) {
			return fmt.Errorf("agents.implementer.by_class: unknown task class %q", class)
		}
	}
	if c.MaxParallel < 1 || c.MaxRework < 0 || c.MaxFilesPerTask < 1 {
		return errors.New("max_parallel and max_files_per_task must be ≥ 1, max_rework ≥ 0")
	}
	// A zero duration is not a smaller budget, it is a broken run: a lock
	// that has expired the moment it is taken, a wave stale before its
	// agents start, a verify command killed before it runs. Each is named,
	// because the user has to find the key that says it.
	for _, d := range []struct {
		field string
		value Duration
	}{
		{field: "lock_ttl", value: c.LockTTL},
		{field: "wave_stale_after", value: c.WaveStaleAfter},
		{field: "verify_timeout", value: c.VerifyTimeout},
	} {
		if d.value <= 0 {
			return fmt.Errorf("%s must be greater than zero, got %s", d.field, shortDuration(time.Duration(d.value)))
		}
	}
	return nil
}

// IsTaskClass reports whether s is one of TaskClasses.
func IsTaskClass(s string) bool {
	return slices.Contains(TaskClasses, s)
}

// ImplementerModel resolves the model for a task class (spec D22).
func (c Config) ImplementerModel(class string) string {
	if m, ok := c.Agents.Implementer.ByClass[class]; ok && m != "" {
		return m
	}
	return c.Agents.Implementer.Model
}
