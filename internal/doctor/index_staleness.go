package doctor

import (
	"context"
	"os"
	"path/filepath"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/plan"
)

// indexStalenessCheckName names this check.
const indexStalenessCheckName = "index-staleness"

// Phase ranks, in spec §4.3's progress order — named so mnd does not flag
// the map literal below as magic numbers.
const (
	rankBrainstorm = iota
	rankPlan
	rankExecute
	rankFinish
	rankArchived
)

// phaseRank orders phases so a check can ask "has this phase already passed
// X" with a plain integer comparison.
var phaseRank = map[string]int{
	bundle.PhaseBrainstorm: rankBrainstorm,
	bundle.PhasePlan:       rankPlan,
	bundle.PhaseExecute:    rankExecute,
	bundle.PhaseFinish:     rankFinish,
	bundle.PhaseArchived:   rankArchived,
}

// staleGate pairs a gate with the phase rank from which it must stay
// satisfied: spec once the run has left brainstorm, plan once it has left
// plan.
type staleGate struct {
	name string
	from int
}

// staleGates is the pair index-staleness re-checks.
var staleGates = []staleGate{
	{gate.Spec, phaseRank[bundle.PhasePlan]},
	{gate.Plan, phaseRank[bundle.PhaseExecute]},
}

// IndexStaleness flags a gate that a later edit re-armed after the phase
// already passed it, and an index drafted against an older spec (spec §11).
// The gate check runs first: an unsatisfied gate is the more serious
// (ERROR) condition, so it is what a reader sees first when both fire.
var IndexStaleness = Check{Name: indexStalenessCheckName, Run: func(_ context.Context, in Input) []Finding {
	rank := phaseRank[in.State.Phase]
	var out []Finding
	out = append(out, staleGateFindings(in, rank)...)
	if f, ok := staleIndexFinding(in, rank); ok {
		out = append(out, f)
	}
	if len(out) == 0 {
		out = append(out, Finding{
			Level: levelPass, Check: indexStalenessCheckName, Slug: in.Slug,
			Message: "index and gates match the artifacts",
		})
	}
	return out
}}

// staleGateFindings ERRORs on any gate whose phase has passed it but whose
// receipt no longer matches the current artifact hash (spec §9's re-arming).
func staleGateFindings(in Input, rank int) []Finding {
	events, _ := bundle.ReadEvents(in.BundleDir)
	var out []Finding
	for _, g := range staleGates {
		if rank < g.from || !reviewEnabled(in.State, g.name) {
			continue
		}
		st, err := gate.Compute(in.BundleDir, g.name, events)
		if err != nil {
			continue
		}
		if !st.Satisfied {
			out = append(out, Finding{
				Level: levelError, Check: indexStalenessCheckName, Slug: in.Slug,
				Message: g.name + " gate is no longer satisfied (artifact edited after the review)",
				Fix:     "run `takt review " + g.name + "` again or record an override",
			})
		}
	}
	return out
}

// reviewEnabled reports whether this run has the gate's review switched on.
// A run initialised with --no-review-spec never takes a spec receipt, so
// there is nothing a later edit can make stale — flagging it ERROR told the
// user to re-run a review they had turned off (review I6, spec §12: the
// run's config is frozen at init).
func reviewEnabled(st *bundle.State, name string) bool {
	switch name {
	case gate.Spec:
		return st.Config.Review.Spec
	case gate.Plan:
		return st.Config.Review.Plan
	}
	return true
}

// staleIndexFinding WARNs once the run has reached plan phase or later and
// plan.index.json's spec_hash no longer matches the current spec.md.
func staleIndexFinding(in Input, rank int) (Finding, bool) {
	if rank < phaseRank[bundle.PhasePlan] {
		return Finding{}, false
	}
	raw, err := os.ReadFile(filepath.Join(in.BundleDir, "plan.index.json"))
	if err != nil {
		return Finding{}, false
	}
	idx, err := plan.ParseIndex(raw)
	if err != nil {
		return Finding{}, false
	}
	spec, err := os.ReadFile(filepath.Join(in.BundleDir, "spec.md"))
	if err != nil || idx.SpecHash == goals.Hash(spec) {
		return Finding{}, false
	}
	return Finding{
		Level: levelWarn, Check: indexStalenessCheckName, Slug: in.Slug,
		Message: "plan.index.json spec_hash does not match spec.md",
		Fix:     "re-run the planner (`takt next`) or accept the drift",
	}, true
}
