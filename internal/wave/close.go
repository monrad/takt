package wave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gitx"
)

// closePrefix starts the name of every per-slice close record.
const closePrefix = "close.s"

// TaskResult is one task's outcome in a close record.
type TaskResult struct {
	Task         int                   `json:"task"`
	Status       string                `json:"status"` // done | failed | blocked | rework | review_error
	Reason       string                `json:"reason,omitempty"`
	FilesChanged []string              `json:"files_changed"`
	Verify       []VerifyResult        `json:"verify,omitempty"`
	Review       *backend.ReviewResult `json:"review,omitempty"`
	// BlindReview is the first backend pass when a scoped pass replaced it
	// (two-layers design §3.5); Review then holds the verdict that graded
	// the task. Nil when no scoped pass ran.
	BlindReview *backend.ReviewResult `json:"blind_review,omitempty"`
	// Internal is the confirmed internal-lens findings attributed to this
	// task — advisory input, never a grader.
	Internal []InternalFinding `json:"internal,omitempty"`
}

// CloseResult is waves/<n>/close.s<slice>.json.
type CloseResult struct {
	Wave int `json:"wave"`
	// Slice numbers the dispatch this record answers. A wave larger than
	// max_parallel goes out in slices that all run at attempt 1, so the
	// attempt alone cannot tell one slice's record from the next one's —
	// and each slice commits on its own, so each keeps its own record.
	Slice      int          `json:"slice"`
	Attempt    int          `json:"attempt"`
	Tasks      []TaskResult `json:"tasks"`
	OutOfScope []string     `json:"out_of_scope"`
	Reverted   []string     `json:"reverted"`
	Committed  bool         `json:"committed"`
	CommitSHA  string       `json:"commit_sha,omitempty"`
	// NothingToCommit records a close that decided the wave was finished and
	// found nothing of its making left to stage — an external bundle whose
	// tasks were all waived, say. It is a landed outcome, not a failure, so
	// it is written as committed with no sha rather than as committed:false
	// with empty failure lists, which would raise a content-free gate
	// (review M2).
	NothingToCommit bool      `json:"nothing_to_commit,omitempty"`
	ClosedAt        time.Time `json:"closed_at"`
	Failed          []int     `json:"failed"`
	Blocked         []int     `json:"blocked"`
	Rework          []int     `json:"rework"`
	ReviewErrors    []int     `json:"review_errors"`
}

// ClosePath is where slice s of wave n records its close.
func ClosePath(bundleDir string, wave, slice int) string {
	return filepath.Join(bundleDir, "waves", strconv.Itoa(wave), "close.s"+strconv.Itoa(slice)+".json")
}

// ReadClose returns nil, nil when the slice has no close record: absence of
// a record is not an error condition, it means the slice has not been closed
// yet, and callers branch on the nil to decide that.
//
//nolint:nilnil // documented "not closed yet" sentinel, not an oversight
func ReadClose(bundleDir string, wave, slice int) (*CloseResult, error) {
	b, err := os.ReadFile(ClosePath(bundleDir, wave, slice))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c CloseResult
	if uerr := json.Unmarshal(b, &c); uerr != nil {
		return nil, fmt.Errorf("close.s%d.json: %w", slice, uerr)
	}
	return &c, nil
}

// AllCloses lists every slice record of a wave in ascending slice order. It
// reads only close.s<n>.json: the retired .prev copies dropClose leaves
// behind and the task digests that share the directory are not records of a
// slice that closed.
func AllCloses(bundleDir string, wave int) ([]CloseResult, error) {
	entries, err := os.ReadDir(filepath.Join(bundleDir, "waves", strconv.Itoa(wave)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []CloseResult
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, closePrefix) || !strings.HasSuffix(name, ".json") {
			continue // skips .prev and digests
		}
		s, convErr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, closePrefix), ".json"))
		if convErr != nil {
			continue
		}
		c, rerr := ReadClose(bundleDir, wave, s)
		if rerr != nil {
			return nil, rerr
		}
		if c != nil {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slice < out[j].Slice })
	return out, nil
}

// LatestClose is the wave's highest-numbered slice record, nil when the wave
// has none.
func LatestClose(bundleDir string, wave int) (*CloseResult, error) {
	all, err := AllCloses(bundleDir, wave)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	return &all[len(all)-1], nil
}

// WriteClose writes the record atomically, to its own slice's path. A record
// with no slice number is a caller bug — every close answers a dispatch, and
// every dispatch has a slice — and is refused rather than written to a path
// no reader looks at.
func WriteClose(bundleDir string, c CloseResult) error {
	if c.Slice < 1 {
		return fmt.Errorf("close record for wave %d has no slice number", c.Wave)
	}
	return bundle.WriteJSONAtomic(ClosePath(bundleDir, c.Wave, c.Slice), c)
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
