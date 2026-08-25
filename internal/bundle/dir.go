package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDir is the bundle directory when nothing else is configured.
const DefaultDir = "docs/takt"

// Dir is the resolved bundle location for one repository.
type Dir struct {
	RepoRoot string // absolute work-tree root
	Base     string // absolute directory holding bundles
	InRepo   bool   // Base is inside RepoRoot → bundles are committed
	RepoName string // filepath.Base(RepoRoot); namespaces external dirs
}

// ResolveDir applies the precedence flag › env › cfgDir › DefaultDir (spec §4.1).
// A relative value is inside the repo; an absolute or ~-prefixed one is external.
func ResolveDir(repoRoot, home, flag, env, cfgDir string) (Dir, error) {
	raw := DefaultDir
	switch {
	case flag != "":
		raw = flag
	case env != "":
		raw = env
	case cfgDir != "":
		raw = cfgDir
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		raw = filepath.Join(home, strings.TrimPrefix(raw, "~"))
	}
	d := Dir{RepoRoot: repoRoot, RepoName: filepath.Base(repoRoot)}
	if filepath.IsAbs(raw) {
		d.Base = filepath.Clean(raw)
		d.InRepo = false
	} else {
		if err := CheckRelPath(repoRoot, raw); err != nil {
			return Dir{}, errors.New("bundle dir: " + err.Error())
		}
		d.Base = filepath.Join(repoRoot, raw)
		d.InRepo = true
	}
	// A base that resolves to the work-tree root would make takt's own
	// bookkeeping indistinguishable from the tree it manages: nothing could
	// be excluded from a wave's scope verification any more, so closing a
	// wave would revert state.json and the digests it is reading.
	if d.Base == filepath.Clean(repoRoot) {
		return Dir{}, errors.New("bundle dir must not be the repository root")
	}
	return d, nil
}

// Bundle returns the absolute directory of one run.
func (d Dir) Bundle(slug string) string {
	if d.InRepo {
		return filepath.Join(d.Base, slug)
	}
	return filepath.Join(d.Base, d.RepoName, slug)
}

// Discarded is where a discarded run's bundle is copied before its branch
// is deleted (spec §7.5): a sibling of the live bundles, gitignored, so the
// record survives the branch that held the commits. It mirrors Bundle.
func (d Dir) Discarded(slug string) string {
	return filepath.Join(d.root(), ".discarded", slug)
}

// root returns the directory that holds this repo's bundles.
func (d Dir) root() string {
	if d.InRepo {
		return d.Base
	}
	return filepath.Join(d.Base, d.RepoName)
}

// ListSlugs returns, sorted, every subdirectory containing a state.json.
func (d Dir) ListSlugs() ([]string, error) {
	entries, err := os.ReadDir(d.root())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var slugs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(d.root(), e.Name(), "state.json")); statErr == nil {
			slugs = append(slugs, e.Name())
		}
	}
	sort.Strings(slugs)
	return slugs, nil
}

// RelToRepo converts an absolute path under RepoRoot to the slash-separated
// repo-relative form stored in state (spec §4.5).
func (d Dir) RelToRepo(abs string) (string, error) {
	rel, err := filepath.Rel(d.RepoRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("path is outside the repository: " + abs)
	}
	return filepath.ToSlash(rel), nil
}
