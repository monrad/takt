// Package finish owns the finish-phase records (spec §7.5): verification,
// goal verdicts and retro inputs, each written atomically under
// <bundle>/finish/. It knows nothing about git or the op protocol.
package finish

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/wave"
)

// VerifyRecord is finish/verify.json: what `takt verify` ran at SHA.
type VerifyRecord struct {
	SHA        string              `json:"sha"`
	Passed     bool                `json:"passed"`
	NoCommands bool                `json:"no_commands"`
	Commands   []string            `json:"commands"`
	Results    []wave.VerifyResult `json:"results"`
	Overridden string              `json:"overridden,omitempty"` // the user's reason
	Skipped    bool                `json:"skipped,omitempty"`    // proceeded with no commands
	At         time.Time           `json:"at"`
}

// VerifyPath is where the record lives.
func VerifyPath(bundleDir string) string { return filepath.Join(bundleDir, "finish", "verify.json") }

func extraPath(bundleDir string) string {
	return filepath.Join(bundleDir, "finish", "verify-extra.json")
}

// ReadVerify returns (nil, nil) when no record exists.
func ReadVerify(bundleDir string) (*VerifyRecord, error) {
	b, err := os.ReadFile(VerifyPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // documented "no record" sentinel, as wave.ReadClose
	}
	if err != nil {
		return nil, err
	}
	var r VerifyRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WriteVerify writes the record atomically.
func WriteVerify(bundleDir string, r VerifyRecord) error {
	return writeJSONAtomic(VerifyPath(bundleDir), r)
}

// UnionCommands is every task's verify commands plus the user's extras, in
// first-appearance order, trimmed and deduplicated.
func UnionCommands(idx plan.Index, extra []string) []string {
	var out []string
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, t := range idx.Tasks {
		for _, c := range t.Verify {
			add(c)
		}
	}
	for _, c := range extra {
		add(c)
	}
	return out
}

// ReadExtra returns the user-supplied commands (no_verification/specify).
func ReadExtra(bundleDir string) ([]string, error) {
	b, err := os.ReadFile(extraPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AppendExtra adds one command; adding it twice is a no-op.
func AppendExtra(bundleDir, cmd string) error {
	cur, err := ReadExtra(bundleDir)
	if err != nil {
		return err
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return errors.New("verify command is empty")
	}
	if slices.Contains(cur, cmd) {
		return nil
	}
	return writeJSONAtomic(extraPath(bundleDir), append(cur, cmd))
}

// writeJSONAtomic is the temp+rename+fsync pattern every takt record uses.
func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
