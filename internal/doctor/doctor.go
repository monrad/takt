// Package doctor runs read-only health checks over every bundle (spec §11).
package doctor

import (
	"context"
	"sort"

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
	Dir          bundle.Dir
	Slug         string
	BundleDir    string
	State        *bundle.State
	ValidateOpts plan.ValidateOpts
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

// Default is the check set shipped in plan 1; plan 2 appends more.
var Default = []Check{StateSchema, PlanDisjoint}

// Run executes checks over every bundle (archived only with all). A bundle
// whose state cannot load yields one state-schema ERROR and no other checks.
func Run(
	ctx context.Context,
	dir bundle.Dir,
	all bool,
	checks []Check,
	opts func(bundleDir string) plan.ValidateOpts,
) []Finding {
	var out []Finding
	slugs, err := dir.ListSlugs()
	if err != nil {
		return []Finding{{Level: levelError, Check: "bundles", Message: err.Error()}}
	}
	for _, slug := range slugs {
		out = append(out, runBundle(ctx, dir, slug, all, checks, opts)...)
	}
	sortFindings(out)
	return out
}

// runBundle loads one bundle's state and, unless it is archived and skipped,
// runs every check against it. Split out of Run to keep Run's cognitive
// complexity low (gocognit/funlen precedent from prior tasks) without
// changing behaviour.
func runBundle(
	ctx context.Context,
	dir bundle.Dir,
	slug string,
	all bool,
	checks []Check,
	opts func(bundleDir string) plan.ValidateOpts,
) []Finding {
	bdir := dir.Bundle(slug)
	st, err := bundle.LoadState(bdir)
	if err != nil {
		return []Finding{{
			Level: levelError, Check: stateSchemaCheckName, Slug: slug, Message: err.Error(),
			Fix: "restore state.json from git history; takt never repairs state silently",
		}}
	}
	if st.Phase == bundle.PhaseArchived && !all {
		return nil
	}
	in := Input{Dir: dir, Slug: slug, BundleDir: bdir, State: st, ValidateOpts: opts(bdir)}
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
