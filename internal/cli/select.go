package cli

import (
	"errors"
	"strings"

	"github.com/monrad/takt/internal/bundle"
)

// slugHint is the recovery advice attached to every rejected --slug value.
// The rule itself is already in the error, so the hint says what to do.
const slugHint = "pass a --slug like issue-2154, or omit --slug and let takt derive one from the topic"

// badSlugError marks a --slug value rejected by [bundle.ValidSlug]. It is a
// distinct type so a caller can report it as a usage error (exit 2) instead
// of a runtime failure: the value never reached the filesystem.
type badSlugError struct{ err error }

func (e badSlugError) Error() string { return e.err.Error() }

func (e badSlugError) Unwrap() error { return e.err }

// selectSlug picks the run a command operates on (spec §5.1). An explicit
// --slug is validated here, so every command that takes one — status, plan
// validate, and whatever plans 2 and 3 add — rejects a path-escaping or
// unaddressable slug before it is ever joined onto the bundle root
// (review finding 1).
func selectSlug(ws *workspace, flag string) (string, error) {
	if flag != "" {
		if err := bundle.ValidSlug(flag); err != nil {
			return "", badSlugError{err: err}
		}
		return flag, nil
	}
	slugs, err := ws.Dir.ListSlugs()
	if err != nil {
		return "", err
	}
	var active []string
	for _, s := range slugs {
		st, lerr := bundle.LoadState(ws.Dir.Bundle(s))
		if lerr != nil || st.Phase == bundle.PhaseArchived {
			continue
		}
		active = append(active, s)
	}
	switch len(active) {
	case 0:
		return "", errors.New("no active run in " + ws.Dir.Base)
	case 1:
		return active[0], nil
	default:
		return "", errors.New("several active runs, pass --slug: " + strings.Join(active, ", "))
	}
}

// loadBundle resolves the bundle dir and loads its state.
func loadBundle(ws *workspace, slug string) (string, *bundle.State, error) {
	dir := ws.Dir.Bundle(slug)
	st, err := bundle.LoadState(dir)
	if err != nil {
		return dir, nil, err
	}
	return dir, st, nil
}

// failSelectSlug reports a selectSlug failure through the JSON error
// contract: a rejected --slug value is a usage error (exit 2), while no
// active run or several active runs is a runtime failure (exit 1).
func failSelectSlug(env Env, err error) int {
	if _, ok := errors.AsType[badSlugError](err); ok {
		return fail(env.Stderr, exitUsage, err.Error(), slugHint)
	}
	return fail(env.Stderr, 1, err.Error(), "use --slug <name>")
}
