package bundle

import "time"

// LockOutcome is the result of Acquire.
type LockOutcome string

// Lock outcomes (spec §4.6).
const (
	LockAcquired   LockOutcome = "acquired"     // no holder
	LockHeldBySelf LockOutcome = "held-by-self" // same session; heartbeat refreshed
	LockStolen     LockOutcome = "stolen"       // holder's heartbeat older than ttl
	LockForced     LockOutcome = "forced"       // live holder overridden with force
	LockBlocked    LockOutcome = "blocked"      // live holder; nothing changed
)

// Acquire implements the advisory session lock. It mutates s.Session on
// every outcome except LockBlocked; the caller persists the state.
func Acquire(s *State, id, host string, now time.Time, ttl time.Duration, force bool) LockOutcome {
	take := func(o LockOutcome) LockOutcome {
		s.Session = &Session{ID: id, Host: host, Heartbeat: now}
		return o
	}
	switch {
	case s.Session == nil || s.Session.ID == "":
		return take(LockAcquired)
	case s.Session.ID == id:
		return take(LockHeldBySelf)
	case now.Sub(s.Session.Heartbeat) > ttl:
		return take(LockStolen)
	case force:
		return take(LockForced)
	default:
		return LockBlocked
	}
}

// Release clears the session lock (archive, spec §7.5).
func Release(s *State) { s.Session = nil }
