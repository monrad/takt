package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/monrad/takt/internal/plan"
)

// planDisjointCheckName names this check.
const planDisjointCheckName = "plan-disjoint"

// PlanDisjoint re-validates plan.index.json when present: shared files must
// be ordered by depends_on (so same-wave tasks are disjoint) and paths obey
// the repo-relative rule. State tasks must match the index ids (WARN).
var PlanDisjoint = Check{Name: planDisjointCheckName, Run: func(_ context.Context, in Input) []Finding {
	raw, err := os.ReadFile(filepath.Join(in.BundleDir, "plan.index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []Finding{{Level: levelPass, Check: planDisjointCheckName, Slug: in.Slug, Message: "no plan yet"}}
	}
	if err != nil {
		return []Finding{{Level: levelError, Check: planDisjointCheckName, Slug: in.Slug, Message: err.Error()}}
	}
	idx, err := plan.ParseIndex(raw)
	if err != nil {
		return []Finding{{
			Level: levelError, Check: planDisjointCheckName, Slug: in.Slug, Message: err.Error(),
			Fix: "regenerate the plan index",
		}}
	}
	o := in.ValidateOpts
	o.LookPath, o.GoalIDs, o.SpecHash = nil, nil, "" // structural checks only here
	if ps := plan.Validate(idx, o); len(ps) > 0 {
		return []Finding{{
			Level: levelError, Check: planDisjointCheckName, Slug: in.Slug,
			Message: fmt.Sprintf("%d problem(s); first: %s", len(ps), ps[0]),
			Fix:     "run `takt plan validate` for the full list",
		}}
	}
	if len(in.State.Tasks) > 0 && len(in.State.Tasks) != len(idx.Tasks) {
		return []Finding{{
			Level: levelWarn, Check: planDisjointCheckName, Slug: in.Slug,
			Message: fmt.Sprintf("state has %d tasks, plan.index.json has %d", len(in.State.Tasks), len(idx.Tasks)),
			Fix:     "the index changed after load; reload the plan",
		}}
	}
	return []Finding{{
		Level: levelPass, Check: planDisjointCheckName, Slug: in.Slug,
		Message: fmt.Sprintf("%d tasks, shared files ordered", len(idx.Tasks)),
	}}
}}
