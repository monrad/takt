package doctor

import (
	"context"
	"fmt"
	"time"
)

// staleWaveCheckName names this check.
const staleWaveCheckName = "stale-wave"

// StaleWave flags a wave whose dispatching session is dead (spec §11).
var StaleWave = Check{Name: staleWaveCheckName, Run: func(_ context.Context, in Input) []Finding {
	f := Finding{Level: levelPass, Check: staleWaveCheckName, Slug: in.Slug, Message: "no active wave"}
	aw := in.State.ActiveWave
	if aw == nil {
		return []Finding{f}
	}
	age := in.Now.Sub(aw.StartedAt)
	f.Message = fmt.Sprintf("wave %d attempt %d in flight for %s", aw.N, aw.Attempt, age.Round(time.Second))
	dead := in.State.Session == nil || in.Now.Sub(in.State.Session.Heartbeat) > in.LockTTL
	if age > in.WaveStaleAfter && dead {
		f.Level = levelWarn
		f.Message += " and its session is gone"
		f.Fix = "run `takt next --recover` to reset the unrecorded tasks and re-dispatch"
	}
	return []Finding{f}
}}
