// Package bundle owns the run bundle on disk: directory resolution, the
// single-writer state.json, the append-only events.jsonl, and the advisory
// session lock (spec §4).
package bundle

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
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
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if abs != root && !strings.HasPrefix(abs, prefix) {
		return errors.New("path resolves outside the repository: " + p)
	}
	return nil
}

// MaxSlugLen is the hard cap on a bundle slug's length (spec §18).
const MaxSlugLen = 48

// slugPattern is the slug alphabet, stated once so error messages and the
// matcher below cannot drift apart.
const slugPattern = "^[a-z0-9]+(?:-[a-z0-9]+)*$"

var slugRe = regexp.MustCompile(slugPattern)

// slugRule is the human wording of slugPattern, used in every rejection.
const slugRule = "a slug is lowercase letters and digits in groups separated by single hyphens, e.g. issue-2154"

// ValidSlug rejects any slug that is not exactly what deriveSlug produces:
// lowercase alphanumeric groups joined by single hyphens, at most
// [MaxSlugLen] characters. A slug becomes a directory name under the bundle
// root and a git branch name, so an unvalidated one (`../../escaped`,
// `My Feature`) escapes the bundle root or commits a path takt cannot
// address (review finding 1).
func ValidSlug(s string) error {
	if s == "" {
		return errors.New("empty slug: " + slugRule)
	}
	if len(s) > MaxSlugLen {
		return fmt.Errorf("slug %q is %d characters; at most %d", s, len(s), MaxSlugLen)
	}
	if !slugRe.MatchString(s) {
		return fmt.Errorf("invalid slug %q: %s", s, slugRule)
	}
	return nil
}
