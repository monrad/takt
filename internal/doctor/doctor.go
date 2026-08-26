// Package doctor runs read-only health checks over every bundle (spec §11).
package doctor

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
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

// Input is what a check sees for one bundle. RepoRoot is set even for a
// repo-wide check's single Input{RepoRoot, Now} (Slug ""); per-bundle
// checks get it too, copied from Options, though none currently use it.
type Input struct {
	Dir            bundle.Dir
	Slug           string
	BundleDir      string
	State          *bundle.State
	ValidateOpts   plan.ValidateOpts
	Now            time.Time
	WaveStaleAfter time.Duration
	LockTTL        time.Duration
	RepoRoot       string
	CurrentBranch  string
	Resolve        func(ref string) bool
	Dirty          func(rel string) bool
}

// Check is one named health check. RepoWide marks a check that judges the
// repository as a whole (e.g. a single .git/index.lock) rather than one
// bundle; RunWith runs it once per invocation instead of once per bundle
// (spec §11).
type Check struct {
	Name     string
	RepoWide bool
	Run      func(ctx context.Context, in Input) []Finding
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
// liveness, staleness and branch checks of spec §11; plan 3 adds the
// repo-wide index-lock check).
var Default = []Check{StateSchema, PlanDisjoint, StaleWave, IndexStaleness, Branch, IndexLock}

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
	// Dirty reports whether git has anything outstanding under the
	// repo-relative path rel. Left nil — as every caller that predates it
	// does, and as a unit test with no repository must — the checks that
	// need git simply do not run.
	Dirty        func(rel string) bool
	ValidateOpts func(bundleDir string) plan.ValidateOpts
}

// Run executes checks over every bundle with the defaults a caller that
// predates plan 2's Options needs: the clock is now, every ref resolves (so
// the branch check never fires without a real Resolve), and the two
// thresholds are config's shipped defaults. Those thresholds are not
// optional the way Resolve is — left at the zero value, every wave is past a
// "0s" staleness budget and every heartbeat past a "0s" lock TTL, so a wave
// dispatched a minute ago by a session that is alive and answering reports
// as stale. Taking them from [config.Defaults] keeps the wrapper judging by
// the same numbers a run does (spec §11, §12).
func Run(
	ctx context.Context,
	dir bundle.Dir,
	all bool,
	checks []Check,
	opts func(bundleDir string) plan.ValidateOpts,
) []Finding {
	cfg := config.Defaults()
	return RunWith(ctx, dir, Options{
		All: all, Now: time.Now(),
		WaveStaleAfter: time.Duration(cfg.WaveStaleAfter), LockTTL: time.Duration(cfg.LockTTL),
		ValidateOpts: opts, Resolve: func(string) bool { return true },
	}, checks)
}

// RunWith executes checks over every bundle (archived only with o.All, the
// one exception being an archived bundle git still has something outstanding
// under — see archivedChecks). A
// bundle whose state cannot load yields one state-schema ERROR and no other
// checks. A repo-wide check (Check.RepoWide) runs once, before the
// per-bundle loop, against Input{RepoRoot, Now} with Slug "" — sortFindings
// orders an empty slug first, so it heads the output.
func RunWith(ctx context.Context, dir bundle.Dir, o Options, checks []Check) []Finding {
	out, perBundle := runRepoWide(ctx, o, checks)
	slugs, err := dir.ListSlugs()
	if err != nil {
		return append(out, Finding{Level: levelError, Check: "bundles", Message: err.Error()})
	}
	for _, slug := range slugs {
		out = append(out, runBundle(ctx, dir, slug, o, perBundle)...)
	}
	sortFindings(out)
	return out
}

// runRepoWide splits checks into repo-wide (run once here) and per-bundle
// (returned for the caller's loop), so runBundle never re-runs a check
// that judges the repository as a whole.
func runRepoWide(ctx context.Context, o Options, checks []Check) ([]Finding, []Check) {
	var out []Finding
	perBundle := make([]Check, 0, len(checks))
	for _, c := range checks {
		if !c.RepoWide {
			perBundle = append(perBundle, c)
			continue
		}
		out = append(out, c.Run(ctx, Input{RepoRoot: o.RepoRoot, Now: o.Now})...)
	}
	return out, perBundle
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
	in := Input{
		Dir: dir, Slug: slug, BundleDir: bdir, State: st, ValidateOpts: o.ValidateOpts(bdir),
		Now: o.Now, WaveStaleAfter: o.WaveStaleAfter, LockTTL: o.LockTTL, RepoRoot: o.RepoRoot,
		CurrentBranch: o.CurrentBranch, Resolve: o.Resolve, Dirty: o.Dirty,
	}
	if st.Phase == bundle.PhaseArchived && !o.All {
		checks = archivedChecks(in, checks)
		if len(checks) == 0 {
			return nil
		}
	}
	var out []Finding
	for _, c := range checks {
		out = append(out, c.Run(ctx, in)...)
	}
	return out
}

// archivedChecks narrows the check set for an archived bundle a caller did
// not ask for with --all. The skip stands for everything an archived run's
// frozen history would only produce noise about — but not for an archive
// whose bundle was never committed. That run is precisely the one nobody
// calls `takt next` on again, so hiding its ERROR behind --all would hide it
// from the only command that looks (review I1). Just state-schema runs, and
// only when the caller asked for it in the first place.
func archivedChecks(in Input, checks []Check) []Check {
	if !uncommittedArchive(in) {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(checks), func(c Check) bool { return c.Name != stateSchemaCheckName })
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
