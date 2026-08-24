package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the state.json schema this binary writes and reads.
const SchemaVersion = 1

// Phases (spec §4.3): the only progress enum.
const (
	PhaseBrainstorm = "brainstorm"
	PhasePlan       = "plan"
	PhaseExecute    = "execute"
	PhaseFinish     = "finish"
	PhaseArchived   = "archived"
)

// Task statuses (spec §4.3).
const (
	StatusPending = "pending"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusBlocked = "blocked"
	StatusWaived  = "waived"
)

var phases = map[string]bool{
	PhaseBrainstorm: true, PhasePlan: true, PhaseExecute: true, PhaseFinish: true, PhaseArchived: true,
}

var statuses = map[string]bool{
	StatusPending: true, StatusDone: true, StatusFailed: true, StatusBlocked: true, StatusWaived: true,
}

// ReviewConfig mirrors config.Review; duplicated here so bundle does not
// import config (state is the frozen copy, spec §12).
type ReviewConfig struct {
	Spec  bool `json:"spec"`
	Plan  bool `json:"plan"`
	Tasks bool `json:"tasks"`
}

// RunConfig is the per-run configuration frozen at init.
type RunConfig struct {
	Autonomy    string       `json:"autonomy"`
	Review      ReviewConfig `json:"review"`
	Goals       bool         `json:"goals"`
	Alignment   bool         `json:"alignment"`
	MaxParallel int          `json:"max_parallel"`
	MaxRework   int          `json:"max_rework"`
}

// Task is one plan task as tracked in state.
type Task struct {
	ID         int             `json:"id"`
	Wave       int             `json:"wave"`
	Status     string          `json:"status"`
	Files      []string        `json:"files"`
	Class      string          `json:"class"`
	Attempt    int             `json:"attempt"`
	LastDigest json.RawMessage `json:"last_digest,omitempty"`
}

// BaselineEntry records a dirty/untracked path and its content hash before a wave launches.
type BaselineEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// ActiveWave marks a wave that has been dispatched and not yet closed.
type ActiveWave struct {
	N         int             `json:"n"`
	Slice     int             `json:"slice"`
	Attempt   int             `json:"attempt"`
	StartedAt time.Time       `json:"started_at"`
	SessionID string          `json:"session_id"`
	Baseline  []BaselineEntry `json:"baseline"`
}

// PendingGate is a durable question awaiting the user.
type PendingGate struct {
	ID       string          `json:"id"`
	OpenedAt time.Time       `json:"opened_at"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Session is the advisory lock holder (spec §4.6).
type Session struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Heartbeat time.Time `json:"heartbeat"`
}

// State is state.json. Field order is the on-disk key order.
type State struct {
	Schema          int               `json:"schema"`
	TaktVersion     string            `json:"takt_version"`
	Slug            string            `json:"slug"`
	Topic           string            `json:"topic"`
	Phase           string            `json:"phase"`
	CreatedAt       time.Time         `json:"created_at"`
	Branch          string            `json:"branch"`
	BranchAdopted   bool              `json:"branch_adopted"`
	Base            string            `json:"base"`
	BaseSHA         string            `json:"base_sha"`
	Config          RunConfig         `json:"config"`
	GoalsHash       *string           `json:"goals_hash"`
	Gates           map[string]string `json:"gates"`
	Tasks           []Task            `json:"tasks"`
	ActiveWave      *ActiveWave       `json:"active_wave"`
	PendingGate     *PendingGate      `json:"pending_gate"`
	VerifiedSHA     *string           `json:"verified_sha"`
	GoalsCheckedSHA *string           `json:"goals_checked_sha"`
	Session         *Session          `json:"session"`
}

// StatePath returns bundleDir/state.json.
func StatePath(bundleDir string) string { return filepath.Join(bundleDir, "state.json") }

// LoadState reads and validates state.json.
func LoadState(bundleDir string) (*State, error) {
	b, err := os.ReadFile(StatePath(bundleDir))
	if err != nil {
		return nil, err
	}
	var s State
	if uerr := json.Unmarshal(b, &s); uerr != nil {
		return nil, fmt.Errorf("state.json: %w", uerr)
	}
	if s.Schema > SchemaVersion {
		return nil, fmt.Errorf(
			"state.json schema %d is newer than this takt (%d); upgrade takt",
			s.Schema,
			SchemaVersion,
		)
	}
	if verr := s.Validate(); verr != nil {
		return nil, fmt.Errorf("state.json: %w", verr)
	}
	return &s, nil
}

// renameFile is a seam for the atomicity test.
var renameFile = os.Rename

// SaveState writes state.json atomically: temp file in the same directory,
// fsync, rename (spec §13). Nil slices are normalised so JSON shows [] not null.
func SaveState(bundleDir string, s *State) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Tasks == nil {
		s.Tasks = []Task{}
	}
	if s.Gates == nil {
		s.Gates = map[string]string{}
	}
	if err := os.MkdirAll(bundleDir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(bundleDir, "state.json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, werr := tmp.Write(b); werr != nil {
		_ = tmp.Close()
		cleanup()
		return werr
	}
	if serr := tmp.Sync(); serr != nil {
		_ = tmp.Close()
		cleanup()
		return serr
	}
	if cerr := tmp.Close(); cerr != nil {
		cleanup()
		return cerr
	}
	if rerr := renameFile(tmpName, StatePath(bundleDir)); rerr != nil {
		cleanup()
		return rerr
	}
	return nil
}

// Validate checks closed sets and path rules; it does not touch the filesystem.
func (s *State) Validate() error {
	if s.Slug == "" {
		return errors.New("slug is empty")
	}
	if !phases[s.Phase] {
		return fmt.Errorf("unknown phase %q", s.Phase)
	}
	seen := map[int]bool{}
	for _, t := range s.Tasks {
		if seen[t.ID] {
			return fmt.Errorf("duplicate task id %d", t.ID)
		}
		seen[t.ID] = true
		if !statuses[t.Status] {
			return fmt.Errorf("task %d: unknown status %q", t.ID, t.Status)
		}
		if t.Wave < 0 {
			return fmt.Errorf("task %d: negative wave", t.ID)
		}
		for _, f := range t.Files {
			if err := CheckRelPath("/", f); err != nil { // root irrelevant for the syntactic checks
				return fmt.Errorf("task %d: %w", t.ID, err)
			}
		}
	}
	return nil
}

// Task returns the task with id, or nil.
func (s *State) Task(id int) *Task {
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			return &s.Tasks[i]
		}
	}
	return nil
}
