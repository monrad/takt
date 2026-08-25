package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"strings"

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
// CLAUDE_CODE_SESSION_ID nor TAKT_SESSION was set.
const generatedSessionPrefix = "takt-"

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
	return generatedSessionPrefix + hex.EncodeToString(b[:])
}

// generatedSession reports whether id was invented by sessionID rather than
// supplied by the environment. Spec §4.6 asks such an id to be persisted in
// TAKT_SESSION for the process tree; when nothing persisted it, it lived for
// exactly one process and no later invocation can present it again. Holding
// the advisory lock in that name would block the run's own next command for
// a whole lock_ttl behind an owner gate no one can answer, so a generated
// holder is taken over instead of asked about.
func generatedSession(id string) bool { return strings.HasPrefix(id, generatedSessionPrefix) }
