package cli

import (
	"context"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
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

// cmdInit implements `takt init` (spec §7.1): resolve the workspace, apply
// the branch rule (spec D9), freeze this run's config, write state.json and
// events.jsonl, and — for an in-repo bundle dir — commit exactly the bundle.
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

	branch, adopted, def, code := chooseBranch(ctx, env, ws, opts.slug)
	if code != 0 {
		return code
	}

	baseSHA, code := resolveBase(ctx, env, ws, def, adopted)
	if code != 0 {
		return code
	}

	st, code := newRunState(env, ws, opts, branch, adopted, def, baseSHA)
	if code != 0 {
		return code
	}

	code = persistState(env, bdir, st, branch, adopted, def, baseSHA)
	if code != 0 {
		return code
	}

	committed, bundleOut, code := commitBundle(ctx, env, ws, bdir, opts.slug)
	if code != 0 {
		return code
	}

	err = writeJSON(env.Stdout, map[string]any{
		"slug": opts.slug, "bundle": bundleOut, "branch": branch, "branch_adopted": adopted,
		"base": def, "base_sha": baseSHA, "committed": committed,
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

// chooseBranch implements the branch rule (spec D9): on the default branch,
// init creates and checks out a new takt/<slug> branch (branch_adopted =
// false); on any other branch, init adopts the current branch as the run
// branch (branch_adopted = true).
func chooseBranch(ctx context.Context, env Env, ws *workspace, slug string) (string, bool, string, int) {
	cur, err := ws.Repo.CurrentBranch(ctx)
	if err != nil || cur == "HEAD" {
		return "", false, "", fail(env.Stderr, 1, "cannot init on a detached HEAD", "check out a branch first")
	}
	def, err := ws.Repo.DefaultBranch(ctx, ws.Cfg.DefaultBranch)
	if err != nil {
		return "", false, "", fail(env.Stderr, 1, err.Error(), "set default_branch in .takt.json")
	}
	if cur != def {
		return cur, true, def, 0
	}
	branch := "takt/" + slug
	if exists, _ := ws.Repo.BranchExists(ctx, branch); exists {
		return "", false, def, fail(env.Stderr, 1, "branch "+branch+" already exists",
			"delete it or choose another --slug")
	}
	err = ws.Repo.CreateAndCheckout(ctx, branch)
	if err != nil {
		return "", false, def, fail(env.Stderr, 1, err.Error(), "")
	}
	return branch, false, def, 0
}

// resolveBase computes this run's base_sha (spec D9): an adopted branch
// walks back to the merge-base with def; a freshly created run branch's
// base_sha is just HEAD (the commit the branch was cut from).
func resolveBase(ctx context.Context, env Env, ws *workspace, def string, adopted bool) (string, int) {
	head, err := ws.Repo.HeadSHA(ctx)
	if err != nil {
		return "", fail(env.Stderr, 1, err.Error(), "")
	}
	if !adopted {
		return head, 0
	}
	mb, err := ws.Repo.MergeBase(ctx, def, "HEAD")
	if err != nil {
		return head, 0
	}
	return mb, 0
}

// newRunState builds the frozen state.json for this run (spec §12): a
// --autonomy override is applied then validated, and the --no-* flags mask
// the config's review/goals/alignment toggles for this run only.
func newRunState(
	env Env,
	ws *workspace,
	opts *initOptions,
	branch string,
	adopted bool,
	def, baseSHA string,
) (*bundle.State, int) {
	cfg := ws.Cfg
	if opts.autonomy != "" {
		cfg.Autonomy = opts.autonomy
	}
	if err := cfg.Validate(); err != nil {
		return nil, fail(env.Stderr, exitUsage, err.Error(), "")
	}
	now := time.Now().UTC()
	host, _ := os.Hostname()
	return &bundle.State{
		Schema: bundle.SchemaVersion, TaktVersion: version.Version,
		Slug: opts.slug, Topic: opts.topic, Phase: bundle.PhaseBrainstorm, CreatedAt: now,
		Branch: branch, BranchAdopted: adopted, Base: def, BaseSHA: baseSHA,
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
	}, 0
}

// persistState saves state.json and appends the init event (spec §4.4).
func persistState(env Env, bdir string, st *bundle.State, branch string, adopted bool, def, baseSHA string) int {
	if err := bundle.SaveState(bdir, st); err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	if err := bundle.AppendEvent(bdir, "init", map[string]any{
		"slug": st.Slug, "branch": branch, "branch_adopted": adopted, "base": def, "base_sha": baseSHA,
	}); err != nil {
		return fail(env.Stderr, 1, err.Error(), "")
	}
	return 0
}

// commitBundle stages and commits only the bundle directory when it lives
// inside the repo (spec §4.1); an external bundle dir is never committed,
// and nothing outside the bundle directory is ever staged.
func commitBundle(ctx context.Context, env Env, ws *workspace, bdir, slug string) (bool, string, int) {
	if !ws.Dir.InRepo {
		return false, bdir, 0
	}
	rel, err := ws.Dir.RelToRepo(bdir)
	if err != nil {
		return false, "", fail(env.Stderr, 1, err.Error(), "")
	}
	err = ws.Repo.Add(ctx, rel)
	if err != nil {
		return false, "", fail(env.Stderr, 1, err.Error(), "")
	}
	if _, err = ws.Repo.Commit(ctx, "takt("+slug+"): init"); err != nil {
		return false, "", fail(env.Stderr, 1, err.Error(), "")
	}
	return true, rel, 0
}
