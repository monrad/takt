package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/gitx"
)

// workspace is everything a command needs about where it runs.
type workspace struct {
	Repo *gitx.Repo
	Cfg  config.Config
	Dir  bundle.Dir
	Home string
}

// workspaceHint is the recovery advice for an openWorkspace failure: not a
// repository, an unreadable or invalid config layer, or a bundle dir that
// resolves outside the repo.
const workspaceHint = "run takt inside a git repository; check .takt.json, $TAKT_CONFIG and $TAKT_DIR"

// openWorkspace resolves repo, config and bundle dir from the cwd (spec §4.1).
func openWorkspace(ctx context.Context, env Env, dirFlag string) (*workspace, error) {
	repo, err := gitx.Open(ctx, env.Cwd)
	if err != nil {
		return nil, err
	}
	home := env.Getenv("HOME")
	cfg, _, err := config.Load(repo.Root, home, env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	dir, err := bundle.ResolveDir(repo.Root, home, dirFlag, env.Getenv("TAKT_DIR"), cfg.Dir)
	if err != nil {
		return nil, err
	}
	return &workspace{Repo: repo, Cfg: cfg, Dir: dir, Home: home}, nil
}

// addDirFlag registers the --dir flag every command accepts.
func addDirFlag(fs *flag.FlagSet) *string {
	return fs.String("dir", "", "bundle directory (overrides TAKT_DIR and .takt.json)")
}

// sessionIDBytes is the amount of randomness in a generated session id.
const sessionIDBytes = 8

// generatedSessionPrefix marks a session id takt invented because neither
// CLAUDE_CODE_SESSION_ID nor TAKT_SESSION was set. It is a readability aid
// in logs only: liveness is never inferred from it, because spec §4.6 asks
// a generated id to be persisted in TAKT_SESSION, which makes a live
// session's id look exactly the same (review finding 1). What matters is
// recorded on the holder as [bundle.Session].Generated.
const generatedSessionPrefix = "takt-"

// sessionID identifies the driving session for the advisory lock (spec
// §4.6) and reports whether takt had to invent the id. An invented id lives
// for exactly one process unless the caller persists it in TAKT_SESSION, so
// a holder that recorded generated=true can never come back to claim its
// run and must not lock it for a whole lock_ttl.
func sessionID(getenv func(string) string) (string, bool) {
	if s := getenv("CLAUDE_CODE_SESSION_ID"); s != "" {
		return s, false
	}
	if s := getenv("TAKT_SESSION"); s != "" {
		return s, false
	}
	var b [sessionIDBytes]byte
	_, _ = rand.Read(b[:])
	return generatedSessionPrefix + hex.EncodeToString(b[:]), true
}
