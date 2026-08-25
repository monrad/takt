// Package wave holds the deterministic mechanics of one wave (spec §7.4):
// the git baseline before launch, scope verification and revert after,
// verify commands run fresh, the close record, and the wave commit.
package wave

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gitx"
)

// hashFile returns "sha256:<hex>" of the file, "" when it does not exist.
func hashFile(root, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// dirtyPaths returns every path that is modified, added, deleted or
// untracked right now, sorted and unique.
func dirtyPaths(ctx context.Context, repo *gitx.Repo) ([]string, error) {
	entries, err := repo.Porcelain(ctx)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, e := range entries {
		set[e.Path] = true
		if e.OrigPath != "" {
			set[e.OrigPath] = true
		}
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// Baseline records every dirty/untracked path with its content hash
// before a wave launches (spec §4.3), so a user-dirty file an agent also
// edits is still detected and user dirt left alone is not.
func Baseline(ctx context.Context, repo *gitx.Repo) ([]bundle.BaselineEntry, error) {
	paths, err := dirtyPaths(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]bundle.BaselineEntry, 0, len(paths))
	for _, p := range paths {
		h, herr := hashFile(repo.Root, p)
		if herr != nil {
			return nil, herr
		}
		out = append(out, bundle.BaselineEntry{Path: p, Hash: h})
	}
	return out, nil
}

// Touched is a path changed since the baseline.
type Touched struct {
	Path    string
	Deleted bool
}

// TouchedSince lists paths that are dirty now and were either absent from
// the baseline or have a different content hash than it recorded, plus
// baseline paths that have fallen out of `git status` entirely: an
// untracked file git never reports once it is deleted (git has no record
// of something it never tracked), so those baseline paths are checked
// directly against the filesystem instead of via dirtyPaths. The result is
// sorted by path.
func TouchedSince(ctx context.Context, repo *gitx.Repo, baseline []bundle.BaselineEntry) ([]Touched, error) {
	base := map[string]string{}
	for _, e := range baseline {
		base[e.Path] = e.Hash
	}
	paths, err := dirtyPaths(ctx, repo)
	if err != nil {
		return nil, err
	}
	dirty := map[string]bool{}
	var out []Touched
	for _, p := range paths {
		dirty[p] = true
		h, herr := hashFile(repo.Root, p)
		if herr != nil {
			return nil, herr
		}
		if prev, ok := base[p]; ok && prev == h {
			continue
		}
		out = append(out, Touched{Path: p, Deleted: h == ""})
	}
	for _, e := range baseline {
		if dirty[e.Path] {
			continue
		}
		h, herr := hashFile(repo.Root, e.Path)
		if herr != nil {
			return nil, herr
		}
		switch {
		case h == "":
			out = append(out, Touched{Path: e.Path, Deleted: true})
		case h != e.Hash:
			out = append(out, Touched{Path: e.Path})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// BaselinePath is bundleDir/waves/<n>/baseline.json, where a wave's baseline
// is parked while it has no active_wave to live on.
func BaselinePath(bundleDir string, wave int) string {
	return filepath.Join(bundleDir, "waves", strconv.Itoa(wave), "baseline.json")
}

// SaveBaseline parks a wave's baseline on disk. Answering the wave_failures
// gate with `retry` clears active_wave so the relaunch picks a fresh slice,
// which would also throw away the baseline the wave started from — and a
// retry measures against the tree the wave began in, not the one its failed
// attempt left behind (review M1, spec §7.4 step 5).
func SaveBaseline(bundleDir string, wave int, entries []bundle.BaselineEntry) error {
	p := BaselinePath(bundleDir, wave)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	if entries == nil {
		entries = []bundle.BaselineEntry{}
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

// ReadBaseline returns the parked baseline, or nil when none was parked.
// Absence is the normal case — only a retry parks one — so it is not an
// error, the same contract as [ReadClose].
func ReadBaseline(bundleDir string, wave int) ([]bundle.BaselineEntry, error) {
	b, err := os.ReadFile(BaselinePath(bundleDir, wave))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []bundle.BaselineEntry
	if uerr := json.Unmarshal(b, &entries); uerr != nil {
		return nil, fmt.Errorf("baseline.json: %w", uerr)
	}
	if entries == nil {
		entries = []bundle.BaselineEntry{}
	}
	return entries, nil
}

// DeleteBaseline drops a parked baseline once the wave has committed: the
// next slice of the same wave starts from the tree that commit left, not
// from the one the retried attempt started in.
func DeleteBaseline(bundleDir string, wave int) error {
	if err := os.Remove(BaselinePath(bundleDir, wave)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
