//nolint:testpackage // needs the renameFile seam / shared internal fixture
package bundle

import (
	"testing"
	"time"
)

func TestAcquireLifecycle(t *testing.T) {
	t.Parallel()
	ttl := 10 * time.Minute
	t0 := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	s := sample()

	if got := Acquire(s, Identity{ID: "A", Host: "host1"}, t0, ttl, false); got != LockAcquired {
		t.Fatalf("fresh acquire = %s", got)
	}
	if s.Session == nil || s.Session.ID != "A" || !s.Session.Heartbeat.Equal(t0) {
		t.Fatalf("session not recorded: %+v", s.Session)
	}
	// Within the renewal window the refresh is not worth a write of its own;
	// past it the caller must persist. See TestAcquireRenewsOnlyAStaleHeartbeat.
	tk := t0.Add(time.Minute)
	if got := Acquire(s, Identity{ID: "A", Host: "host1"}, tk, ttl, false); got != LockKept ||
		!s.Session.Heartbeat.Equal(tk) {
		t.Fatalf("re-acquire by self = %s, heartbeat %v", got, s.Session.Heartbeat)
	}
	t1 := tk.Add(ttl/2 + time.Second)
	if got := Acquire(
		s,
		Identity{ID: "A", Host: "host1"},
		t1,
		ttl,
		false,
	); got != LockHeldBySelf ||
		!s.Session.Heartbeat.Equal(t1) {
		t.Fatalf("re-acquire by self past the window = %s, heartbeat %v", got, s.Session.Heartbeat)
	}
	if got := Acquire(
		s,
		Identity{ID: "B", Host: "host2"},
		t1.Add(time.Minute),
		ttl,
		false,
	); got != LockBlocked ||
		s.Session.ID != "A" {
		t.Fatalf("live other session must block: %s, holder %s", got, s.Session.ID)
	}
	if got := Acquire(
		s,
		Identity{ID: "B", Host: "host2"},
		t1.Add(time.Minute),
		ttl,
		true,
	); got != LockForced ||
		s.Session.ID != "B" {
		t.Fatalf("force must take over: %s, holder %s", got, s.Session.ID)
	}
	stale := t1.Add(time.Minute).Add(ttl).Add(time.Second)
	if got := Acquire(s, Identity{ID: "C", Host: "host3", Generated: true}, stale, ttl, false); got != LockStolen ||
		s.Session.ID != "C" {
		t.Fatalf("stale lock must be stolen: %s, holder %s", got, s.Session.ID)
	}
	// Acquire records how the taker got its id, so a later call can tell a
	// live env-named session from a takt-invented one (review finding 1).
	if !s.Session.Generated {
		t.Fatal("Acquire must record Identity.Generated on the holder")
	}
	Release(s)
	if s.Session != nil {
		t.Fatal("Release must clear the session")
	}
}

// TestAcquireRenewsOnlyAStaleHeartbeat pins the lease-renewal half of the
// lock contract. The heartbeat lives in state.json, a tracked file, so
// persisting it on every call left `takt next` rewriting the bundle even
// when the call decided nothing — and a run whose last op was a `stop` ended
// with a modified state.json in the user's worktree that no commit would
// ever pick up. Acquire still refreshes the heartbeat in memory, but reports
// LockKept — "no write needed" — while the recorded one is younger than half
// the ttl and nothing else about the holder changed. A different host, or a
// takt-invented id now supplied by the environment, must still be written
// out, because those are what the orphan rule reads.
func TestAcquireRenewsOnlyAStaleHeartbeat(t *testing.T) {
	t.Parallel()
	ttl := 10 * time.Minute
	t0 := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	me := Identity{ID: "A", Host: "host1"}

	for _, tc := range []struct {
		name string
		who  Identity
		at   time.Time
		want LockOutcome
	}{
		{"same instant", me, t0, LockKept},
		{"just inside the window", me, t0.Add(ttl/2 - time.Second), LockKept},
		{"just outside the window", me, t0.Add(ttl/2 + time.Second), LockHeldBySelf},
		{"a heartbeat from the future", me, t0.Add(-time.Second), LockHeldBySelf},
		{"same id on another host", Identity{ID: "A", Host: "host2"}, t0, LockHeldBySelf},
		{"a generated id now supplied by the environment",
			Identity{ID: "A", Host: "host1", Generated: true}, t0, LockHeldBySelf},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sample()
			s.Session = &Session{ID: "A", Host: "host1", Heartbeat: t0}
			before := *s.Session
			if got := Acquire(s, tc.who, tc.at, ttl, false); got != tc.want {
				t.Fatalf("outcome = %s, want %s", got, tc.want)
			}
			// Either way the in-memory heartbeat is current, so a write the
			// caller makes for another reason carries it.
			if !s.Session.Heartbeat.Equal(tc.at) {
				t.Fatalf("heartbeat = %v, want %v", s.Session.Heartbeat, tc.at)
			}
			if tc.want == LockKept &&
				(s.Session.ID != before.ID || s.Session.Host != before.Host ||
					s.Session.Generated != before.Generated) {
				t.Fatalf("LockKept must not change the holder: %+v → %+v", before, *s.Session)
			}
		})
	}
}
