package cli

import (
	"context"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
	"github.com/monrad/takt/internal/version"
)

// initOptions is the result of parsing takt init's flags and free-text topic.
type initOptions struct {
	dir      string
	slug     string
	autonomy string
	noSpec   bool
	noPlan   bool
	noTasks  bool
	noGoals  bool
	noAlign  bool
	topic    string
}

// branchInit is what chooseBranch decided, threaded through the later steps
// so a failure after branch creation can be rolled back correctly (spec D9).
type branchInit struct {
	branch  string
	def     string
	adopted bool
	created bool // a new takt/<slug> branch was created and checked out
}

// cmdInit implements `takt init` (spec §7.1): resolve the workspace,
// validate everything that can fail without touching git, apply the branch
// rule (spec D9), freeze this run's config, write state.json and
// events.jsonl, and — for an in-repo bundle dir — commit exactly the
// bundle. Any failure after a run branch is created best-effort rolls the
// checkout back rather than stranding the user on a half-initialised branch
// (review finding 1).
func cmdInit(env Env) int {
	opts, code := initFlags(env)
	if code != 0 {
		return code
	}

	ctx := context.Background()
	ws, err := openWorkspace(ctx, env, opts.dir)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), "run takt inside a git repository")
	}
	if opts.slug == "" {
		opts.slug = deriveSlug(opts.topic)
	}
	bdir := ws.Dir.Bundle(opts.slug)

	code = checkPreconditions(ctx, env, ws, opts.slug, bdir)
	if code != 0 {
		return code
	}

	// Validate everything that can fail without a git mutation *before* any
	// git mutation (review finding 1): a bad --autonomy value or a
	// malformed .takt.json must never leave the user on a half-initialised
	// branch.
	cfg, code := validateConfig(env, ws, opts)
	if code != 0 {
		return code
	}

	bi, code := chooseBranch(ctx, env, ws, opts.slug)
	if code != 0 {
		return code
	}

	baseSHA, code := resolveBase(ctx, env, ws, bi)
	if code != 0 {
		return code
	}

	st := newRunState(env, cfg, opts, bi, baseSHA)

	code = persistState(ctx, env, ws, bi, bdir, st, baseSHA)
	if code != 0 {
		return code
	}

	committed, bundleOut, code := commitBundle(ctx, env, ws, bi, bdir, opts.slug)
	if code != 0 {
		return code
	}

	err = writeJSON(env.Stdout, map[string]any{
		keySlug: opts.slug, "bundle": bundleOut, keyBranch: bi.branch, keyBranchAdopted: bi.adopted,
		keyBase: bi.def, keyBaseSHA: baseSHA, "committed": committed,
	})
	if err != nil {
		return 1
	}
	return 0
}

// initFlags parses takt init's flags and the free-text topic argument.
func initFlags(env Env) (*initOptions, int) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slug := fs.String("slug", "", "bundle slug (default: derived from the topic)")
	autonomy := fs.String("autonomy", "", "auto|step (default from config)")
	noSpec := fs.Bool("no-review-spec", false, "disable the spec review gate for this run")
	noPlan := fs.Bool("no-review-plan", false, "disable the plan review gate for this run")
	noTasks := fs.Bool("no-review-tasks", false, "disable per-task review for this run")
	noGoals := fs.Bool("no-goals", false, "disable goals for this run")
	noAlign := fs.Bool("no-alignment", false, "disable the alignment audit for this run")
	if err := fs.Parse(env.Args); err != nil {
		return nil, usageError(env, fs, err)
	}
	topic := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if topic == "" {
		return nil, fail(env.Stderr, exitUsage, "init needs a topic", `takt init "<what you want built>"`)
	}
	return &initOptions{
		dir: *dirFlag, slug: *slug, autonomy: *autonomy,
		noSpec: *noSpec, noPlan: *noPlan, noTasks: *noTasks, noGoals: *noGoals, noAlign: *noAlign,
		topic: topic,
	}, 0
}

// checkPreconditions refuses a duplicate slug or a dirty index before init
// touches the repository (spec §7.1).
func checkPreconditions(ctx context.Context, env Env, ws *workspace, slug, bdir string) int {
	if _, err := os.Stat(bundle.StatePath(bdir)); err == nil {
		return fail(env.Stderr, 1, "bundle "+slug+" already exists at "+bdir,
			"pick another --slug or resume it with `takt next`")
	}
	if staged, err := ws.Repo.HasStaged(ctx); err != nil || staged {
		return fail(env.Stderr, 1, "the index has staged changes",
			"commit or unstage them first; takt init must start from a clean index")
	}
	return 0
}

// validateConfig applies the --autonomy override and validates the result
// (spec §12) before any git mutation happens (review finding 1): a bad
// --autonomy value or a malformed .takt.json must fail before chooseBranch
// ever runs, so a retry after fixing it is never mis-classified as having
// adopted a branch takt itself created.
func validateConfig(env Env, ws *workspace, opts *initOptions) (config.Config, int) {
	cfg := ws.Cfg
	if opts.autonomy != "" {
		cfg.Autonomy = opts.autonomy
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, fail(env.Stderr, exitUsage, err.Error(),
			"autonomy must be auto or step; check .takt.json and --autonomy")
	}
	return cfg, 0
}

// chooseBranch implements the branch rule (spec D9): on the default branch,
// init creates and checks out a new takt/<slug> branch; on any other
// branch, init adopts the current branch as the run branch.
func chooseBranch(ctx context.Context, env Env, ws *workspace, slug string) (*branchInit, int) {
	cur, err := ws.Repo.CurrentBranch(ctx)
	if err != nil || cur == "HEAD" {
		return nil, fail(env.Stderr, 1, "cannot init on a detached HEAD", "check out a branch first")
	}
	def, err := ws.Repo.DefaultBranch(ctx, ws.Cfg.DefaultBranch)
	if err != nil {
		return nil, fail(env.Stderr, 1, err.Error(), "set default_branch in .takt.json")
	}
	if cur != def {
		return &branchInit{branch: cur, def: def, adopted: true}, 0
	}
	branch := "takt/" + slug
	if exists, _ := ws.Repo.BranchExists(ctx, branch); exists {
		return nil, fail(env.Stderr, 1, "branch "+branch+" already exists",
			"delete it or choose another --slug")
	}
	err = ws.Repo.CreateAndCheckout(ctx, branch)
	if err != nil {
		return nil, fail(env.Stderr, 1, err.Error(), "")
	}
	return &branchInit{branch: branch, def: def, created: true}, 0
}

// resolveBase computes this run's base_sha (spec D9). A freshly created run
// branch's base_sha is HEAD, the commit it was cut from; a HeadSHA failure
// here happens after CreateAndCheckout, so it rolls the branch back (review
// finding 1). An adopted branch's base_sha is the merge-base with def —
// MergeBase failure is a hard error (review finding 2): silently falling
// back to HEAD would forge false provenance, and nothing needs rolling back
// since adopting a branch never mutates git.
func resolveBase(ctx context.Context, env Env, ws *workspace, bi *branchInit) (string, int) {
	if !bi.adopted {
		head, err := ws.Repo.HeadSHA(ctx)
		if err != nil {
			return "", failInit(ctx, env, ws, bi, err.Error())
		}
		return head, 0
	}
	mb, err := ws.Repo.MergeBase(ctx, bi.def, "HEAD")
	if err != nil {
		return "", fail(env.Stderr, 1,
			"cannot determine the merge-base of "+bi.def+" and HEAD: "+err.Error(),
			"set default_branch in .takt.json to the branch this work is based on, or create/fetch "+bi.def+" locally")
	}
	return mb, 0
}

// newRunState builds the frozen state.json for this run (spec §12); cfg is
// already the --autonomy-applied, validated configuration (validateConfig
// runs before any git mutation, review finding 1), and the --no-* flags
// mask the config's review/goals/alignment toggles for this run only.
func newRunState(env Env, cfg config.Config, opts *initOptions, bi *branchInit, baseSHA string) *bundle.State {
	now := time.Now().UTC()
	host, _ := os.Hostname()
	return &bundle.State{
		Schema: bundle.SchemaVersion, TaktVersion: version.Version,
		Slug: opts.slug, Topic: opts.topic, Phase: bundle.PhaseBrainstorm, CreatedAt: now,
		Branch: bi.branch, BranchAdopted: bi.adopted, Base: bi.def, BaseSHA: baseSHA,
		Config: bundle.RunConfig{
			Autonomy: cfg.Autonomy,
			Review: bundle.ReviewConfig{
				Spec:  cfg.Review.Spec && !opts.noSpec,
				Plan:  cfg.Review.Plan && !opts.noPlan,
				Tasks: cfg.Review.Tasks && !opts.noTasks,
			},
			Goals:       cfg.Goals && !opts.noGoals,
			Alignment:   cfg.Alignment && !opts.noAlign,
			MaxParallel: cfg.MaxParallel,
			MaxRework:   cfg.MaxRework,
		},
		Gates:   map[string]string{"spec": "pending", "plan": "pending"},
		Tasks:   []bundle.Task{},
		Session: &bundle.Session{ID: sessionID(env.Getenv), Host: host, Heartbeat: now},
	}
}

// persistState saves state.json and appends the init event (spec §4.4); a
// failure here happens after CreateAndCheckout when a run branch was just
// created, so it rolls the branch back (review finding 1).
func persistState(
	ctx context.Context, env Env, ws *workspace, bi *branchInit, bdir string, st *bundle.State, baseSHA string,
) int {
	if err := bundle.SaveState(bdir, st); err != nil {
		return failInit(ctx, env, ws, bi, err.Error())
	}
	if err := bundle.AppendEvent(bdir, "init", map[string]any{
		keySlug: st.Slug, keyBranch: bi.branch, keyBranchAdopted: bi.adopted, keyBase: bi.def, keyBaseSHA: baseSHA,
	}); err != nil {
		return failInit(ctx, env, ws, bi, err.Error())
	}
	return 0
}

// commitBundle stages and commits only the bundle directory when it lives
// inside the repo (spec §4.1); an external bundle dir is never committed,
// and nothing outside the bundle directory is ever staged. A failure here
// happens after CreateAndCheckout when a run branch was just created, so it
// rolls the branch back (review finding 1).
func commitBundle(ctx context.Context, env Env, ws *workspace, bi *branchInit, bdir, slug string) (bool, string, int) {
	if !ws.Dir.InRepo {
		return false, bdir, 0
	}
	rel, err := ws.Dir.RelToRepo(bdir)
	if err != nil {
		return false, "", failInit(ctx, env, ws, bi, err.Error())
	}
	err = ws.Repo.Add(ctx, rel)
	if err != nil {
		return false, "", failInit(ctx, env, ws, bi, err.Error())
	}
	if _, err = ws.Repo.Commit(ctx, "takt("+slug+"): init"); err != nil {
		return false, "", failInit(ctx, env, ws, bi, err.Error())
	}
	return true, rel, 0
}

// rollbackCreatedBranch checks out def and deletes branch — best-effort
// cleanup for an init that created a run branch but then failed before
// anything was committed (spec D9, review finding 1): the user must never
// be silently left on a half-initialised branch.
func rollbackCreatedBranch(ctx context.Context, ws *workspace, def, branch string) error {
	if err := ws.Repo.Checkout(ctx, def); err != nil {
		return err
	}
	return ws.Repo.DeleteBranch(ctx, branch)
}

// failInit fails init with msg (hint defaults to "", or a manual-recovery
// message when the rollback below fails); when bi.created is true (a run
// branch was just made) it best-effort rolls the checkout back so the user
// is told exactly what to run by hand (review finding 1).
func failInit(ctx context.Context, env Env, ws *workspace, bi *branchInit, msg string) int {
	hint := ""
	if bi.created {
		if rerr := rollbackCreatedBranch(ctx, ws, bi.def, bi.branch); rerr != nil {
			hint = "you are on branch " + bi.branch + "; run: git checkout " + bi.def + " && git branch -D " + bi.branch
		}
	}
	return fail(env.Stderr, 1, msg, hint)
}
