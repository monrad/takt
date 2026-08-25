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
	LockKept       LockOutcome = "kept"         // same session, heartbeat still current; no write needed
)

// heartbeatRenewDivisor sets how much of lock_ttl a holder's own heartbeat
// may age on disk before Acquire asks the caller to write it out: ttl/2, the
// usual lease-renewal window. Persisting it on every call costs a rewrite of
// state.json — a tracked file — on every single `takt next`, including the
// calls that decide nothing and commit nothing, which leaves takt's own
// bookkeeping modified in the user's worktree with nothing to pick it up
// (spec §4.7: takt commits what it writes). The price is that a session
// whose calls all skip the write keeps a heartbeat up to ttl/2 old, so
// another session sees it as live for ttl/2 rather than a full ttl after it
// really stops. Every call that writes state for its own reasons — a launch,
// a transition, a gate — still carries the fresh heartbeat with it.
const heartbeatRenewDivisor = 2

// Identity is who is asking for the lock: the session id, the host it runs
// on, and whether takt invented the id itself (spec §4.6).
type Identity struct {
	ID        string
	Host      string
	Generated bool
}

// Acquire implements the advisory session lock. It mutates s.Session on
// every outcome except LockBlocked; the caller persists the state. LockKept
// additionally says the change is not worth a write of its own — the holder
// is unchanged and its recorded heartbeat is still current — so the caller
// may skip persisting and leave the bundle byte-identical on disk. The
// refreshed heartbeat is still in s, and rides along with any write the
// caller makes for another reason.
func Acquire(s *State, who Identity, now time.Time, ttl time.Duration, force bool) LockOutcome {
	take := func(o LockOutcome) LockOutcome {
		s.Session = &Session{ID: who.ID, Host: who.Host, Heartbeat: now, Generated: who.Generated}
		return o
	}
	switch {
	case s.Session == nil || s.Session.ID == "":
		return take(LockAcquired)
	case s.Session.ID == who.ID:
		if s.Session.Host == who.Host && s.Session.Generated == who.Generated && current(s.Session, now, ttl) {
			return take(LockKept)
		}
		return take(LockHeldBySelf)
	case now.Sub(s.Session.Heartbeat) > ttl:
		return take(LockStolen)
	case force:
		return take(LockForced)
	default:
		return LockBlocked
	}
}

// current reports whether the holder's recorded heartbeat is recent enough
// that writing it out again would tell another session nothing it does not
// already know. A heartbeat stamped in the future (a clock that moved
// backwards) is not current, so the next call repairs it on disk.
func current(sess *Session, now time.Time, ttl time.Duration) bool {
	age := now.Sub(sess.Heartbeat)
	return age >= 0 && age < ttl/heartbeatRenewDivisor
}

// Release clears the session lock (archive, spec §7.5).
func Release(s *State) { s.Session = nil }
