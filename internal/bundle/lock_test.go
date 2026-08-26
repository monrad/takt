package bundle_test

import (
	"testing"
	"time"

	"github.com/monrad/takt/internal/bundle"
)

func TestAcquireOutcomesOverTheRecordedHolder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	me := bundle.Identity{ID: "me", Host: "h"}
	live := &bundle.Session{ID: "you", Host: "h", Heartbeat: now.Add(-time.Minute)}
	stale := &bundle.Session{ID: "you", Host: "h", Heartbeat: now.Add(-time.Hour)}
	mine := &bundle.Session{ID: "me", Host: "h", Heartbeat: now.Add(-9 * time.Minute)}
	cases := []struct {
		name   string
		held   *bundle.Session
		force  bool
		want   bundle.LockOutcome
		holder string
	}{
		{"free", nil, false, bundle.LockAcquired, "me"},
		{"mine", mine, false, bundle.LockHeldBySelf, "me"},
		{"stale other", stale, false, bundle.LockStolen, "me"},
		{"live other", live, false, bundle.LockBlocked, "you"},
		{"live other, forced", live, true, bundle.LockForced, "me"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, s := bundle.Acquire(c.held, me, now, 10*time.Minute, c.force)
			if got != c.want || s == nil || s.ID != c.holder {
				t.Fatalf("Acquire = %v, %+v; want %v held by %s", got, s, c.want, c.holder)
			}
			if got != bundle.LockBlocked && !s.Heartbeat.Equal(now) {
				t.Fatalf("every taken outcome refreshes the heartbeat: %v", s.Heartbeat)
			}
			if got == bundle.LockBlocked && s != c.held {
				t.Fatal("blocked must hand back the holder unchanged")
			}
		})
	}
}

// TestAcquireRecordsHowTheIdWasObtained pins the part of the holder the
// orphan rule reads: a takt-invented id can never be presented again, so
// Acquire must carry Identity.Generated onto the record it hands back — a
// generated id and an environment-supplied one are indistinguishable as
// strings (spec §4.6, plan 2 review finding 1).
func TestAcquireRecordsHowTheIdWasObtained(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	who := bundle.Identity{ID: "invented", Host: "h", Generated: true}
	got, s := bundle.Acquire(nil, who, now, 10*time.Minute, false)
	if got != bundle.LockAcquired || s == nil || !s.Generated || s.Host != "h" {
		t.Fatalf("Acquire = %v, %+v; want the whole identity on the holder", got, s)
	}
}
