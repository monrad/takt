package doctor

import (
	"context"
	"fmt"
)

// stateSchemaCheckName names this check; shared with doctor.go's
// load-failure fallback so goconst sees one definition, not a repeated
// literal.
const stateSchemaCheckName = "state-schema"

// StateSchema re-validates state.json beyond what LoadState enforces:
// the active wave must reference tasks, and an open gate must have an id.
var StateSchema = Check{Name: stateSchemaCheckName, Run: func(_ context.Context, in Input) []Finding {
	st := in.State
	f := Finding{Level: levelPass, Check: stateSchemaCheckName, Slug: in.Slug, Message: "state.json is schema-valid"}
	if st.ActiveWave != nil {
		found := false
		for _, t := range st.Tasks {
			if t.Wave == st.ActiveWave.N {
				found = true
				break
			}
		}
		if !found {
			f.Level = levelError
			f.Message = fmt.Sprintf("active_wave.n=%d but no task has that wave", st.ActiveWave.N)
			f.Fix = "run `takt next --recover` once plan 2 lands; until then inspect state.json"
			return []Finding{f}
		}
	}
	if st.PendingGate != nil && st.PendingGate.ID == "" {
		f.Level, f.Message, f.Fix = levelError, "pending_gate has no id", "clear it with `takt answer` once plan 2 lands"
		return []Finding{f}
	}
	if st.Phase == "execute" && len(st.Tasks) == 0 {
		f.Level, f.Message, f.Fix = levelError, "phase is execute but tasks is empty", "the plan was never loaded; re-run planning"
	}
	return []Finding{f}
}}
