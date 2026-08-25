// Package wave holds the deterministic mechanics of one wave (spec §7.4):
// the git baseline before launch, scope verification and revert after,
// verify commands run fresh, the close record, and the wave commit.
package wave

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"

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
