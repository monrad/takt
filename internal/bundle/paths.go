// Package bundle owns the run bundle on disk: directory resolution, the
// single-writer state.json, the append-only events.jsonl, and the advisory
// session lock (spec §4).
package bundle

import (
	"errors"
	"path/filepath"
	"strings"
)

// CheckRelPath enforces spec §4.5: p must be a clean, relative path that
// stays inside root. It never touches the filesystem.
func CheckRelPath(root, p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if filepath.IsAbs(p) {
		return errors.New("absolute path not allowed: " + p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("path escapes the repository: " + p)
	}
	abs := filepath.Join(root, clean)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return errors.New("path resolves outside the repository: " + p)
	}
	return nil
}
