package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session is the advisory lock's holder: who is driving the run, from
// where, and when they last called (spec §4.6). It lives in the bundle's
// untracked area — logs/session.json, which logs/.gitignore keeps out of
// git — never in state.json, so refreshing the heartbeat on every call
// neither dirties the worktree nor lands a lock in a commit for a clone to
// inherit. Generated records that takt invented the id itself (nothing set
// CLAUDE_CODE_SESSION_ID or TAKT_SESSION): such a holder can never present
// its id again and is taken over silently (the takeover itself is the
// caller's rule, not Acquire's).
type Session struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Heartbeat time.Time `json:"heartbeat"`
	Generated bool      `json:"generated,omitempty"`
}

// SessionPath returns bundleDir/logs/session.json.
func SessionPath(bundleDir string) string {
	return filepath.Join(bundleDir, "logs", "session.json")
}

// ReadSession returns the recorded holder, or nil when nobody holds the
// run. A file that exists but cannot be parsed is an error, not "free":
// guessing free is how two sessions end up driving one bundle.
func ReadSession(bundleDir string) (*Session, error) {
	b, err := os.ReadFile(SessionPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // absent is the documented "free" reading, distinct from an unreadable lock
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("logs/session.json: %w", err)
	}
	if s.ID == "" {
		return nil, errors.New("logs/session.json: empty holder id")
	}
	return &s, nil
}

// WriteSession records the holder atomically. [WriteJSONAtomic] creates
// logs/ when init has not (an external bundle dir gets no .gitignore, and
// needs none). A holder with no id is refused rather than written: it is
// the one shape [ReadSession] rejects, so writing it would leave a lock
// nothing but `takt unlock` could clear.
func WriteSession(bundleDir string, s *Session) error {
	if s == nil || s.ID == "" {
		return errors.New("session: empty holder id")
	}
	return WriteJSONAtomic(SessionPath(bundleDir), s)
}

// ClearSession releases the lock; a run nobody holds is already clear.
func ClearSession(bundleDir string) error {
	err := os.Remove(SessionPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
