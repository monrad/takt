package cli

import (
	"errors"
	"strings"

	"github.com/monrad/takt/internal/bundle"
)

// selectSlug picks the run a command operates on (spec §5.1).
func selectSlug(ws *workspace, flag string) (string, error) {
	if flag != "" {
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
