// Package doctor runs read-only health checks over every bundle (spec §11).
package doctor

import (
	"context"
	"sort"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/plan"
)

// Finding is one line of doctor output.
type Finding struct {
	Level   string `json:"level"` // PASS | WARN | ERROR
	Check   string `json:"check"`
	Slug    string `json:"slug"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Input is what a check sees for one bundle.
type Input struct {
	Dir            bundle.Dir
	Slug           string
	BundleDir      string
	State          *bundle.State
	ValidateOpts   plan.ValidateOpts
	Now            time.Time
	WaveStaleAfter time.Duration
	LockTTL        time.Duration
	CurrentBranch  string
	Resolve        func(ref string) bool
}

// Check is one named health check.
type Check struct {
	Name string
	Run  func(ctx context.Context, in Input) []Finding
}

// Finding levels, shared by every check so goconst sees one definition
// instead of repeated string literals.
const (
	levelPass  = "PASS"
	levelWarn  = "WARN"
	levelError = "ERROR"
)

// Default is the check set every `takt doctor` run applies unless a caller
// narrows it (plan 1 shipped state-schema and plan-disjoint; plan 2 adds the
// liveness, staleness and branch checks of spec §11).
var Default = []Check{StateSchema, PlanDisjoint, StaleWave, IndexStaleness, Branch}

// Options parameterises a doctor run: the clock and thresholds the
// liveness/staleness checks judge against, the branch context, and how a
// ref resolves and a bundle dir validates.
type Options struct {
	All            bool
	Now            time.Time
	WaveStaleAfter time.Duration
	LockTTL        time.Duration
	RepoRoot       string
	CurrentBranch  string
	Resolve        func(ref string) bool // does a ref/sha resolve in the repo
	ValidateOpts   func(bundleDir string) plan.ValidateOpts
}

// Run executes checks over every bundle with the defaults a caller that
// predates plan 2's Options needs: the clock is now, and every ref resolves
// (so the branch check never fires without a real Resolve). Kept so
// existing callers of the plan-1 signature are unaffected (spec §11).
func Run(
	ctx context.Context,
	dir bundle.Dir,
	all bool,
	checks []Check,
	opts func(bundleDir string) plan.ValidateOpts,
) []Finding {
	return RunWith(ctx, dir, Options{
		All: all, Now: time.Now(), ValidateOpts: opts, Resolve: func(string) bool { return true },
	}, checks)
}

// RunWith executes checks over every bundle (archived only with o.All). A
// bundle whose state cannot load yields one state-schema ERROR and no other
// checks.
func RunWith(ctx context.Context, dir bundle.Dir, o Options, checks []Check) []Finding {
	var out []Finding
	slugs, err := dir.ListSlugs()
	if err != nil {
		return []Finding{{Level: levelError, Check: "bundles", Message: err.Error()}}
	}
	for _, slug := range slugs {
		out = append(out, runBundle(ctx, dir, slug, o, checks)...)
	}
	sortFindings(out)
	return out
}

// runBundle loads one bundle's state and, unless it is archived and skipped,
// runs every check against it. Split out of RunWith to keep it cognitively
// simple (gocognit/funlen precedent from prior tasks) without changing
// behaviour.
func runBundle(ctx context.Context, dir bundle.Dir, slug string, o Options, checks []Check) []Finding {
	bdir := dir.Bundle(slug)
	st, err := bundle.LoadState(bdir)
	if err != nil {
		return []Finding{{
			Level: levelError, Check: stateSchemaCheckName, Slug: slug, Message: err.Error(),
			Fix: "restore state.json from git history; takt never repairs state silently",
		}}
	}
	if st.Phase == bundle.PhaseArchived && !o.All {
		return nil
	}
	in := Input{
		Dir: dir, Slug: slug, BundleDir: bdir, State: st, ValidateOpts: o.ValidateOpts(bdir),
		Now: o.Now, WaveStaleAfter: o.WaveStaleAfter, LockTTL: o.LockTTL,
		CurrentBranch: o.CurrentBranch, Resolve: o.Resolve,
	}
	var out []Finding
	for _, c := range checks {
		out = append(out, c.Run(ctx, in)...)
	}
	return out
}

// sortFindings orders findings by slug then check, so output is deterministic.
func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Slug != fs[j].Slug {
			return fs[i].Slug < fs[j].Slug
		}
		return fs[i].Check < fs[j].Check
	})
}

// HasError reports whether any finding is an ERROR.
func HasError(fs []Finding) bool {
	for _, f := range fs {
		if f.Level == "ERROR" {
			return true
		}
	}
	return false
}
