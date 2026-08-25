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
	t1 := t0.Add(time.Minute)
	if got := Acquire(
		s,
		Identity{ID: "A", Host: "host1"},
		t1,
		ttl,
		false,
	); got != LockHeldBySelf ||
		!s.Session.Heartbeat.Equal(t1) {
		t.Fatalf("re-acquire by self = %s, heartbeat %v", got, s.Session.Heartbeat)
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
