package doctor

import (
	"context"
	"fmt"
	"time"
)

// staleWaveCheckName names this check.
const staleWaveCheckName = "stale-wave"

// StaleWave flags a wave whose dispatching session is dead (spec §11).
//
// A sidecar that exists but cannot be read is not a dead session: runBundle
// already reports it with an unlock hint, and the wave's agents may well be
// running under it. Telling the user to `--recover` on top of that would
// push them into resetting a live wave, so the long wave is still named but
// the fix defers to the unlock — only once the holder can be read again
// does the recover hint apply (#7).
var StaleWave = Check{Name: staleWaveCheckName, Run: func(_ context.Context, in Input) []Finding {
	f := Finding{Level: levelPass, Check: staleWaveCheckName, Slug: in.Slug, Message: "no active wave"}
	aw := in.State.ActiveWave
	if aw == nil {
		return []Finding{f}
	}
	age := in.Now.Sub(aw.StartedAt)
	f.Message = fmt.Sprintf("wave %d attempt %d in flight for %s", aw.N, aw.Attempt, age.Round(time.Second))
	if age <= in.WaveStaleAfter {
		return []Finding{f}
	}
	switch {
	case in.SessionUnreadable:
		f.Level = levelWarn
		f.Message += "; session unknown (logs/session.json is unreadable)"
		f.Fix = "run `takt unlock --slug " + in.Slug + "` first; whether the wave needs recovering " +
			"can only be judged once its holder can be read"
	case in.Session == nil || in.Now.Sub(in.Session.Heartbeat) > in.LockTTL:
		f.Level = levelWarn
		f.Message += " and its session is gone"
		f.Fix = "run `takt next --recover` to reset the unrecorded tasks and re-dispatch"
	}
	return []Finding{f}
}}
