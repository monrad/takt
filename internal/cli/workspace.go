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

// sessionID identifies the driving session for the advisory lock (spec §4.6).
func sessionID(getenv func(string) string) string {
	if s := getenv("CLAUDE_CODE_SESSION_ID"); s != "" {
		return s
	}
	if s := getenv("TAKT_SESSION"); s != "" {
		return s
	}
	var b [sessionIDBytes]byte
	_, _ = rand.Read(b[:])
	return "takt-" + hex.EncodeToString(b[:])
}
