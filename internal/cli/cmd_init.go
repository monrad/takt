package cli

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
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

// initRun accumulates everything a failed init must undo (spec D9, review
// finding 3): the branch chooseBranch created, the bundle directory and
// files persistState wrote, and the path commitBundle staged.
// checkPreconditions has already proved no state.json was there, so undoing
// init's own writes can never destroy earlier work.
type initRun struct {
	ws     *workspace
	bi     *branchInit
	bdir   string
	newDir bool   // init created bdir; it did not exist beforehand
	staged string // repo-relative path successfully staged, "" if none
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

	ctx, cancel := commandContext(env)
	defer cancel()
	ws, err := openWorkspace(ctx, env, opts.dir)
	if err != nil {
		return fail(env.Stderr, 1, err.Error(), workspaceHint)
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
	run := &initRun{ws: ws, bi: bi, bdir: bdir, newDir: !dirExists(bdir)}

	baseSHA, code := resolveBase(ctx, env, run)
	if code != 0 {
		return code
	}

	st := newRunState(env, cfg, opts, bi, baseSHA)

	code = persistState(ctx, env, run, st, baseSHA)
	if code != 0 {
		return code
	}

	committed, bundleOut, code := commitInitBundle(ctx, env, run, opts.slug)
	if code != 0 {
		return code
	}

	err = writeJSON(env.Stdout, map[string]any{
		keySlug: opts.slug, "bundle": bundleOut, keyBranch: bi.branch, keyBranchAdopted: bi.adopted,
		keyBase: bi.def, keyBaseSHA: baseSHA, keyCommitted: committed,
	})
	if err != nil {
		return 1
	}
	return 0
}

// initFlags parses takt init's flags and the free-text topic argument.
// Flags may appear anywhere among the topic's words (spec §5.1, review
// finding 2), and an explicit --slug is validated before anything else runs
// (review finding 1).
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
	positional, err := parseInterspersed(fs, env.Args)
	if err != nil {
		return nil, usageError(env, fs, err)
	}
	if *slug != "" {
		if verr := bundle.ValidSlug(*slug); verr != nil {
			return nil, fail(env.Stderr, exitUsage, verr.Error(), slugHint)
		}
	}
	topic := strings.TrimSpace(strings.Join(positional, " "))
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
func resolveBase(ctx context.Context, env Env, run *initRun) (string, int) {
	bi := run.bi
	if !bi.adopted {
		head, err := run.ws.Repo.HeadSHA(ctx)
		if err != nil {
			return "", failInit(ctx, env, run, err.Error())
		}
		return head, 0
	}
	mb, err := run.ws.Repo.MergeBase(ctx, bi.def, "HEAD")
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
	id, generated := sessionID(env.Getenv)
	return &bundle.State{
		Schema: bundle.SchemaVersion, TaktVersion: version.Current(),
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
		Gates:   map[string]string{"spec": gatePending, "plan": gatePending},
		Tasks:   []bundle.Task{},
		Session: &bundle.Session{ID: id, Host: host, Heartbeat: now, Generated: generated},
	}
}

// persistState saves state.json and appends the init event (spec §4.4); a
// failure here happens after CreateAndCheckout when a run branch was just
// created, so it rolls back everything init has done so far (review
// findings 1 and 3).
func persistState(ctx context.Context, env Env, run *initRun, st *bundle.State, baseSHA string) int {
	bi := run.bi
	if err := bundle.SaveState(run.bdir, st); err != nil {
		return failInit(ctx, env, run, err.Error())
	}
	if err := bundle.AppendEvent(run.bdir, "init", map[string]any{
		keySlug: st.Slug, keyBranch: bi.branch, keyBranchAdopted: bi.adopted, keyBase: bi.def, keyBaseSHA: baseSHA,
	}); err != nil {
		return failInit(ctx, env, run, err.Error())
	}
	if err := writeLogsIgnore(run.bdir); err != nil {
		return failInit(ctx, env, run, err.Error())
	}
	return 0
}

// logsIgnore is what init puts in the run's logs directory. Reviewer
// prompts, stdout and stderr land there and quote repo content, and spec
// §13 says they are gitignored — but the bundle tree is staged wholesale by
// every takt commit, so without an ignore file they are committed with it
// (review I4). `*` ignores everything under logs/, and `!.gitignore`
// re-includes the rule file itself so it is committed with the bundle: an
// ignore file that ignored itself would exist only on the machine that ran
// init, and the first review after a clone would commit that clone's logs.
const logsIgnore = "*\n!.gitignore\n"

// writeLogsIgnore creates <bundle>/logs/.gitignore. The directory is created
// here rather than waiting for the first review, so the ignore rule is in
// place before anything can be written into it; the backend still creates it
// on demand for bundles that predate this.
func writeLogsIgnore(bdir string) error {
	dir := filepath.Join(bdir, "logs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(logsIgnore), 0o600)
}

// commitInitBundle stages and commits only the bundle directory when it lives
// inside the repo (spec §4.1); an external bundle dir is never committed,
// and nothing outside the bundle directory is ever staged or committed
// (spec §4.7, review finding 2): checkPreconditions has already refused to
// run with anything staged, so the index holds exactly init's own writes
// and the plain commit below is scoped by construction. It deliberately
// does not use [gitx.Repo.CommitPaths] — a pathspec commit holds
// .git/index.lock across hooks, and a deadline-killed init must still be
// able to roll itself back (see rollbackInit). Recording the
// staged path on run is what lets a later failure — a rejected commit hook,
// a signing failure — take it back out of the index (review finding 3).
func commitInitBundle(ctx context.Context, env Env, run *initRun, slug string) (bool, string, int) {
	ws := run.ws
	if !ws.Dir.InRepo {
		return false, run.bdir, 0
	}
	rel, err := ws.Dir.RelToRepo(run.bdir)
	if err != nil {
		return false, "", failInit(ctx, env, run, err.Error())
	}
	if err = ws.Repo.Add(ctx, rel); err != nil {
		return false, "", failInit(ctx, env, run, err.Error())
	}
	run.staged = rel
	if _, err = ws.Repo.Commit(ctx, "takt("+slug+"): init"); err != nil {
		return false, "", failInit(ctx, env, run, err.Error())
	}
	return true, rel, 0
}

// dirExists reports whether p is an existing directory.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// rollbackInit undoes an init that failed partway through and returns the
// manual-recovery hint for whatever it could not undo — "" when the
// repository is back exactly as init found it (spec D9, review finding 3).
// Order matters: the index and the bundle files are cleaned first, so the
// checkout back to the default branch cannot carry init's own half-written
// state across with it.
func rollbackInit(ctx context.Context, run *initRun) string {
	if run.staged != "" {
		_ = run.ws.Repo.Unstage(ctx, run.staged)
	}
	removeInitWrites(run)
	if !run.bi.created {
		return ""
	}
	if err := run.ws.Repo.Checkout(ctx, run.bi.def); err != nil {
		return "you are on branch " + run.bi.branch +
			"; run: git checkout " + run.bi.def + " && git branch -D " + run.bi.branch
	}
	if err := run.ws.Repo.DeleteBranch(ctx, run.bi.branch); err != nil {
		return "branch " + run.bi.branch + " was left behind; run: git branch -D " + run.bi.branch
	}
	return ""
}

// removeInitWrites deletes exactly what init put on disk: the whole bundle
// directory when init created it, otherwise only the two files init writes,
// so a spec.md drafted by hand before running init survives the rollback.
func removeInitWrites(run *initRun) {
	if run.newDir {
		_ = os.RemoveAll(run.bdir)
		return
	}
	_ = os.Remove(bundle.StatePath(run.bdir))
	_ = os.Remove(bundle.EventsPath(run.bdir))
	_ = os.Remove(filepath.Join(run.bdir, "logs", ".gitignore"))
	_ = os.Remove(filepath.Join(run.bdir, "logs")) // only if init left it empty
}

// rollbackTimeout bounds the cleanup after a failed init. It is derived
// without the command's cancellation so a rollback still runs when the
// deadline itself caused the failure (plan-1 final re-review).
const rollbackTimeout = 30 * time.Second

// failInit fails init with msg after rolling back everything init did, and
// attaches the hint for whatever could not be rolled back (review finding
// 3): the user must never be left on a half-initialised branch, with takt's
// files staged, or with a retry-blocking bundle on disk. The rollback runs
// under its own deadline, derived without ctx's cancellation, so a rollback
// caused by ctx's own deadline expiring is not itself a no-op (plan-1 final
// re-review).
func failInit(ctx context.Context, env Env, run *initRun, msg string) int {
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	hint := rollbackInit(rbCtx, run)
	return fail(env.Stderr, 1, msg, hint)
}
