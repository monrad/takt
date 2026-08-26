package doctor

import (
	"context"
	"fmt"

	"github.com/monrad/takt/internal/bundle"
)

// stateSchemaCheckName names this check; shared with doctor.go's
// load-failure fallback so goconst sees one definition, not a repeated
// literal.
const stateSchemaCheckName = "state-schema"

// StateSchema re-validates state.json beyond what LoadState enforces:
// the active wave must reference tasks, and an open gate must have an id.
var StateSchema = Check{Name: stateSchemaCheckName, Run: func(_ context.Context, in Input) []Finding {
	f := Finding{Level: levelPass, Check: stateSchemaCheckName, Slug: in.Slug, Message: "state.json is schema-valid"}
	// An archive that never got its commit: the run says it is finished
	// while its own record is still only in the worktree, where a merge
	// cannot carry it and `git checkout` cannot run over it (review I1).
	// `takt next` on the run takes that commit again — this is the report
	// for the bundle nobody asks again.
	if uncommittedArchive(in) {
		f.Level = levelError
		f.Message = "archived run has an uncommitted bundle"
		f.Fix = "run `takt next --slug " + in.Slug + "`; it takes the archive commit again"
		return []Finding{f}
	}
	return schemaFindings(in, f)
}}

// uncommittedArchive reports whether this bundle is archived and still has
// something outstanding in git. It answers false unless the caller wired a
// Dirty hook against a real repository and the bundle is one takt commits:
// a unit test's bundle, and one that lives outside the work tree, have no
// archive commit to be missing in the first place.
func uncommittedArchive(in Input) bool {
	if in.State.Phase != bundle.PhaseArchived || in.Dirty == nil || in.RepoRoot == "" || !in.Dir.InRepo {
		return false
	}
	rel, err := in.Dir.RelToRepo(in.BundleDir)
	return err == nil && in.Dirty(rel)
}

// schemaFindings is the rest of the check: the active wave must reference
// tasks and carry a slice number, an open gate must have an id, and an
// executing run must have a plan loaded.
func schemaFindings(in Input, f Finding) []Finding {
	st := in.State
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
		// A wave dispatched before close records were kept per slice has no
		// slice number. takt heals it — the next close records it as slice 1,
		// which is right for a wave that has committed nothing — but the
		// state on disk is still from an older build, and saying so is what
		// stops the user hunting for the cause of a renumbered record.
		//
		// It does not return: this is the one finding here the user need do
		// nothing about, so it must not be the answer while something they
		// do have to act on is also true. The branches below overwrite it
		// with their ERROR when one fires, and the WARN stands when none
		// does.
		if st.ActiveWave.Slice < 1 {
			f.Level = levelWarn
			f.Message = "active_wave.slice is 0 (bundle predates per-slice close records)"
			f.Fix = "run `takt next`; the next close-wave records it as slice 1"
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
}
