package doctor

import "context"

// branchCheckName names this check.
const branchCheckName = "branch"

// Branch checks the run's branch and base still resolve and that the
// checkout is on the run's branch (spec §11).
var Branch = Check{Name: branchCheckName, Run: func(_ context.Context, in Input) []Finding {
	st := in.State
	if in.Resolve != nil {
		if !in.Resolve(st.Branch) {
			return []Finding{{
				Level: levelError, Check: branchCheckName, Slug: in.Slug,
				Message: "branch " + st.Branch + " does not exist",
				Fix:     "the run branch was deleted; restore it or archive the bundle",
			}}
		}
		if !in.Resolve(st.BaseSHA) {
			return []Finding{{
				Level: levelError, Check: branchCheckName, Slug: in.Slug,
				Message: "base_sha " + st.BaseSHA + " does not resolve",
				Fix:     "fetch the base branch",
			}}
		}
	}
	if in.CurrentBranch != "" && in.CurrentBranch != st.Branch {
		return []Finding{{
			Level: levelWarn, Check: branchCheckName, Slug: in.Slug,
			Message: "checkout is on " + in.CurrentBranch + ", the run lives on " + st.Branch,
			Fix:     "git checkout " + st.Branch,
		}}
	}
	return []Finding{{Level: levelPass, Check: branchCheckName, Slug: in.Slug, Message: "on " + st.Branch}}
}}
