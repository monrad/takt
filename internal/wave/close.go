package wave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/gitx"
)

// TaskResult is one task's outcome in a close record.
type TaskResult struct {
	Task         int                   `json:"task"`
	Status       string                `json:"status"` // done | failed | blocked | rework
	Reason       string                `json:"reason,omitempty"`
	FilesChanged []string              `json:"files_changed"`
	Verify       []VerifyResult        `json:"verify,omitempty"`
	Review       *backend.ReviewResult `json:"review,omitempty"`
}

// CloseResult is waves/<n>/close.json.
type CloseResult struct {
	Wave         int          `json:"wave"`
	Attempt      int          `json:"attempt"`
	Tasks        []TaskResult `json:"tasks"`
	OutOfScope   []string     `json:"out_of_scope"`
	Reverted     []string     `json:"reverted"`
	Committed    bool         `json:"committed"`
	CommitSHA    string       `json:"commit_sha,omitempty"`
	ClosedAt     time.Time    `json:"closed_at"`
	Failed       []int        `json:"failed"`
	Blocked      []int        `json:"blocked"`
	Rework       []int        `json:"rework"`
	ReviewErrors []int        `json:"review_errors"`
}

// ClosePath returns bundleDir/waves/<n>/close.json.
func ClosePath(bundleDir string, wave int) string {
	return filepath.Join(bundleDir, "waves", strconv.Itoa(wave), "close.json")
}

// ReadClose returns nil, nil when the wave has no close record: absence of
// a close.json is not an error condition, it means the wave has not been
// closed yet, and callers (task 7) branch on the nil to decide that.
//
//nolint:nilnil // documented "not closed yet" sentinel, not an oversight
func ReadClose(bundleDir string, wave int) (*CloseResult, error) {
	b, err := os.ReadFile(ClosePath(bundleDir, wave))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c CloseResult
	if uerr := json.Unmarshal(b, &c); uerr != nil {
		return nil, fmt.Errorf("close.json: %w", uerr)
	}
	return &c, nil
}

// WriteClose writes the record atomically.
func WriteClose(bundleDir string, c CloseResult) error {
	p := ClosePath(bundleDir, c.Wave)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "close.json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, werr := tmp.Write(append(b, '\n')); werr != nil {
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
	if rerr := os.Rename(tmpName, p); rerr != nil {
		cleanup()
		return rerr
	}
	return nil
}

// CommitWave stages exactly the task files (modifications, additions,
// deletions) plus the bundle directory when it is in-repo, and commits those
// paths and nothing else — a file the user staged themselves is neither
// committed nor unstaged (spec §4.7, review finding 2).
func CommitWave(ctx context.Context, repo *gitx.Repo, files []string, bundleRel, msg string) (string, error) {
	paths := append([]string{}, files...)
	if bundleRel != "" {
		paths = append(paths, bundleRel)
	}
	if err := repo.AddPathspec(ctx, paths...); err != nil {
		return "", err
	}
	return repo.CommitPaths(ctx, msg, paths...)
}
