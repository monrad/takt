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

// Identity is who is asking for the lock: the session id, the host it runs
// on, and whether takt invented the id itself (spec §4.6).
type Identity struct {
	ID        string
	Host      string
	Generated bool
}

// Acquire implements the advisory session lock over the recorded holder
// (nil when the run is free). It returns the outcome and the holder to
// record: the caller writes it with [WriteSession] on every outcome except
// LockBlocked, which hands the live holder back unchanged. The record is
// untracked (see [Session]), so refreshing it on every call is free — there
// is no lease and no "not worth a write" outcome any more.
func Acquire(held *Session, who Identity, now time.Time, ttl time.Duration, force bool) (LockOutcome, *Session) {
	next := &Session{ID: who.ID, Host: who.Host, Heartbeat: now, Generated: who.Generated}
	switch {
	case held == nil || held.ID == "":
		return LockAcquired, next
	case held.ID == who.ID:
		return LockHeldBySelf, next
	case now.Sub(held.Heartbeat) > ttl:
		return LockStolen, next
	case force:
		return LockForced, next
	default:
		return LockBlocked, held
	}
}
