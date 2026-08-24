package gitx

import (
	"bytes"
	"errors"
)

// Entry is one record of `git status --porcelain=v1 -z`.
// X is the index status, Y the worktree status ("??" for untracked).
// OrigPath is set for renames/copies (the second NUL-terminated field).
type Entry struct {
	X, Y     byte
	Path     string
	OrigPath string
}

// minPorcelainFieldLen is the shortest a well-formed porcelain-v1 field can
// be: two status bytes, one separating space, and at least one path byte.
const minPorcelainFieldLen = 4

// ParsePorcelainZ parses NUL-separated porcelain v1 output.
func ParsePorcelainZ(b []byte) ([]Entry, error) {
	var out []Entry
	fields := bytes.Split(b, []byte{0})
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) == 0 {
			continue // trailing NUL
		}
		if len(f) < minPorcelainFieldLen || f[2] != ' ' {
			return nil, errors.New("gitx: malformed porcelain record: " + string(f))
		}
		e := Entry{X: f[0], Y: f[1], Path: string(f[3:])}
		if e.X == 'R' || e.X == 'C' {
			i++
			if i >= len(fields) {
				return nil, errors.New("gitx: rename record without original path")
			}
			e.OrigPath = string(fields[i])
		}
		out = append(out, e)
	}
	return out, nil
}
