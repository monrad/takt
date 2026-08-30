package decide_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/deadline"
	"github.com/monrad/takt/internal/decide"
	"github.com/monrad/takt/internal/op"
)

func finishState() *bundle.State {
	return &bundle.State{Slug: "demo", Phase: bundle.PhaseFinish, Branch: "takt/demo", Base: "main",
		Config: bundle.RunConfig{Goals: true}}
}

func TestFinishRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		st     func() *bundle.State
		fin    decide.FinishFacts
		action decide.Action
		gate   string
		step   string
		agent  string
	}{
		{"20 unverified, no record → exec verify", finishState, decide.FinishFacts{}, decide.ActExec, "", "", ""},
		{
			"20 record failed → ask verification_failed",
			finishState,
			decide.FinishFacts{
				Verify: decide.VerifyFacts{Present: true, Failed: []map[string]any{{"command": "go test", "exit": 1}}},
			},
			decide.ActAsk,
			"verification_failed",
			"",
			"",
		},
		{
			"20 record without commands → ask no_verification",
			finishState,
			decide.FinishFacts{
				Verify: decide.VerifyFacts{Present: true, NoCommands: true},
			},
			decide.ActAsk,
			"no_verification",
			"",
			"",
		},
		{
			// review I2: the record landed, the state write did not. Row 20
			// only sees a passed record when verified_sha is missing, and
			// re-verifying HEAD is the only answer that cannot be wrong —
			// `verification_failed` with an empty failed list is the one
			// that always is.
			"20 record passed with no verified_sha → re-verify, never a gate",
			finishState,
			decide.FinishFacts{Verify: decide.VerifyFacts{Present: true, Passed: true}},
			decide.ActExec,
			"",
			"",
			"",
		},
		{"21 verified, goals unchecked, no record → dispatch goal-assessor", finishState,
			decide.FinishFacts{Verified: true}, decide.ActDispatch, "", "", "goal-assessor"},
		{
			// The goals-side twin: an all-achieved record with no
			// goals_checked_sha is re-assessed, never turned into
			// "Unmet goals: []" (review I2).
			"21 record with nothing unmet and no goals_checked_sha → dispatch again",
			finishState,
			decide.FinishFacts{Verified: true, Goals: decide.GoalFacts{Present: true}},
			decide.ActDispatch,
			"",
			"",
			"goal-assessor",
		},
		{
			"21 record with unmet → ask goals_unmet",
			finishState,
			decide.FinishFacts{
				Verified: true,
				Goals:    decide.GoalFacts{Present: true, Unmet: []map[string]any{{"id": "G1", "verdict": "missed"}}},
			},
			decide.ActAsk,
			"goals_unmet",
			"",
			"",
		},
		{
			"21 goals disabled skips to retro",
			func() *bundle.State { s := finishState(); s.Config.Goals = false; return s },
			decide.FinishFacts{Verified: true},
			decide.ActRun,
			"",
			"retro",
			"",
		},
		{"22 checked, no retro → run retro", finishState,
			decide.FinishFacts{Verified: true, GoalsChecked: true}, decide.ActRun, "", "retro", ""},
		{
			"23 retro, no disposition → ask branch_finish",
			finishState,
			decide.FinishFacts{
				Verified:     true,
				GoalsChecked: true,
				HasRetro:     true,
			},
			decide.ActAsk,
			"branch_finish",
			"",
			"",
		},
		{
			"24 pr not pushed → run push_pr",
			finishState,
			decide.FinishFacts{
				Verified:     true,
				GoalsChecked: true,
				HasRetro:     true,
				Disposition:  "pr",
			},
			decide.ActRun,
			"",
			"push_pr",
			"",
		},
		{
			"25 pr pushed → archive",
			finishState,
			decide.FinishFacts{
				Verified:     true,
				GoalsChecked: true,
				HasRetro:     true,
				Disposition:  "pr",
				PRPushed:     true,
			},
			decide.ActArchive,
			"",
			"",
			"",
		},
		{
			"25 keep → archive",
			finishState,
			decide.FinishFacts{
				Verified:     true,
				GoalsChecked: true,
				HasRetro:     true,
				Disposition:  "keep",
			},
			decide.ActArchive,
			"",
			"",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, err := decide.Decide(c.st(), decide.Facts{Finish: c.fin})
			if err != nil {
				t.Fatal(err)
			}
			if d.Action != c.action {
				t.Fatalf("action %s, want %s (%+v)", d.Action, c.action, d)
			}
			if c.gate != "" && d.Op.Gate != c.gate {
				t.Fatalf("gate %s, want %s", d.Op.Gate, c.gate)
			}
			if c.step != "" && d.Op.Step != c.step {
				t.Fatalf("step %s, want %s", d.Op.Step, c.step)
			}
			if c.agent != "" && (d.Agent == nil || d.Agent.Agent != c.agent) {
				t.Fatalf("agent %+v, want %s", d.Agent, c.agent)
			}
		})
	}
}

func TestArchivedStops(t *testing.T) {
	t.Parallel()
	st := finishState()
	st.Phase = bundle.PhaseArchived
	d, err := decide.Decide(st, decide.Facts{})
	if err != nil || d.Action != decide.ActStop || d.Op.Reason != "archived" {
		t.Fatalf("%v %+v", err, d)
	}
}

func TestBranchFinishOptions(t *testing.T) {
	t.Parallel()
	fin := decide.FinishFacts{Verified: true, GoalsChecked: true, HasRetro: true,
		MergeBlocked: "primary worktree is on takt/demo, not main", DiscardAllowed: true}
	d, _ := decide.Decide(finishState(), decide.Facts{Finish: fin})
	choices := map[string]op.Option{}
	for _, o := range d.Op.Options {
		choices[o.Choice] = o
	}
	if len(choices) != 4 {
		t.Fatalf("not adopted must offer merge, pr, keep, discard: %+v", d.Op.Options)
	}
	if choices["merge"].Disabled == "" || choices["discard"].Disabled != "" {
		t.Fatalf("merge must carry the blocking reason, discard must not: %+v", choices)
	}
	if d.Op.Options[0].Choice != "pr" {
		t.Fatalf("a disabled merge must not be listed first: %+v", d.Op.Options)
	}
	// Both blocked keys are part of the gate's payload — the question reads
	// them, and so will the prompt renderer.
	for _, k := range []string{"merge_blocked", "discard_blocked"} {
		if _, ok := d.Op.Context[k]; !ok {
			t.Fatalf("branch_finish context must carry %s: %+v", k, d.Op.Context)
		}
	}
	// A blocked discard renders its own reason, not merge's by accident.
	blocked := decide.FinishFacts{Verified: true, GoalsChecked: true, HasRetro: true,
		MergeBlocked: "no", DiscardBlocked: "the run adopted branch feature; integrate it yourself"}
	db, _ := decide.Decide(finishState(), decide.Facts{Finish: blocked})
	for _, o := range db.Op.Options {
		if o.Choice == "discard" && o.Disabled != blocked.DiscardBlocked {
			t.Fatalf("discard must render discard_blocked: %+v", o)
		}
	}
	st := finishState()
	st.BranchAdopted = true
	d, _ = decide.Decide(st, decide.Facts{Finish: fin})
	if len(d.Op.Options) != 2 || d.Op.Options[0].Choice != "pr" || d.Op.Options[1].Choice != "keep" {
		t.Fatalf("adopted branch offers pr and keep only: %+v", d.Op.Options)
	}
}

// branchFinishChoices lists an ask op's option choices in printed order.
func branchFinishChoices(opts []op.Option) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Choice)
	}
	return out
}

// assertOneChoosableRecommendation checks the invariant every branch_finish
// rendering has to hold: exactly one option is recommended, it is the first,
// and it is not disabled.
func assertOneChoosableRecommendation(t *testing.T, opts []op.Option) {
	t.Helper()
	n := 0
	for _, o := range opts {
		if strings.Contains(o.Label, "(Recommended)") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("exactly one option is recommended, got %d: %+v", n, opts)
	}
	if !strings.Contains(opts[0].Label, "(Recommended)") {
		t.Fatalf("the recommendation must be listed first: %+v", opts)
	}
	if opts[0].Disabled != "" {
		t.Fatalf("the recommendation must be choosable: %+v", opts[0])
	}
}

// TestQuestionBranchFinishRecommendsAChoosableOption: the recommendation has
// to be something the binary will accept. `takt init` on the default branch
// checks the run branch out in the primary worktree, so in that flow merge is
// always blocked — and it was recommended anyway, and listed first, which
// told every such run to choose the one option `takt answer` refuses (#26).
// The blocked merge is still offered, with its reason: demoted, not hidden.
func TestQuestionBranchFinishRecommendsAChoosableOption(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fin  decide.FinishFacts
		want []string
	}{
		{
			"merge blocked",
			decide.FinishFacts{Verified: true, GoalsChecked: true, HasRetro: true,
				MergeBlocked: "primary worktree is on takt/demo, not main", DiscardAllowed: true},
			[]string{"pr", "keep", "merge", "discard"},
		},
		{
			"merge allowed",
			decide.FinishFacts{Verified: true, GoalsChecked: true, HasRetro: true,
				MergeAllowed: true, DiscardAllowed: true},
			[]string{"merge", "pr", "keep", "discard"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, err := decide.Decide(finishState(), decide.Facts{Finish: c.fin})
			if err != nil {
				t.Fatal(err)
			}
			if got := branchFinishChoices(d.Op.Options); !slices.Equal(got, c.want) {
				t.Fatalf("options = %v, want %v", got, c.want)
			}
			assertOneChoosableRecommendation(t, d.Op.Options)
			blocked := c.want[0] == "pr"
			if disabled := d.Op.Options[slices.Index(c.want, "merge")].Disabled; blocked != (disabled != "") {
				t.Fatalf("merge must carry its reason exactly when it is blocked: %q", disabled)
			}
		})
	}
}

// TestVerifyExecTimeoutStrictlyContainsTheBinaryCap is row 20's half of spec
// A2.2: `exec takt verify` carries [deadline.Session] of the cap `takt
// verify` puts on itself, which is verify_timeout per command over the union
// it will actually run — not the fixed 900s that budgeted neither.
//
// The rows differ only in the command count, and each is its own subtest so
// that an empty union failing cannot hide what a three-command one does.
// The last of them is the second half of the claim: the count is genuinely
// read, so three commands buy more room than none. A timeout blind to the
// count is what makes an outer deadline fire first on a long verify.
func TestVerifyExecTimeoutStrictlyContainsTheBinaryCap(t *testing.T) {
	t.Parallel()
	for _, cmds := range []int{0, 3} {
		t.Run(strconv.Itoa(cmds)+" commands", func(t *testing.T) {
			t.Parallel()
			d := verifyExec(t, cmds)
			assertSessionContains(t, d.Op.TimeoutS, deadline.Verify(factVerifyTimeout, cmds))
		})
	}
	t.Run("a longer union buys more room", func(t *testing.T) {
		t.Parallel()
		empty, three := verifyExec(t, 0), verifyExec(t, 3)
		if three.Op.TimeoutS <= empty.Op.TimeoutS {
			t.Fatalf("a three-command union must outbudget an empty one: %d vs %d",
				three.Op.TimeoutS, empty.Op.TimeoutS)
		}
	})
}

// verifyExec is row 20's exec op for a union of the given size, which is the
// only fact the row's deadline varies with.
func verifyExec(t *testing.T, cmds int) decide.Decision {
	t.Helper()
	d, err := decide.Decide(finishState(), decide.Facts{
		VerifyTimeout: factVerifyTimeout,
		Finish:        decide.FinishFacts{VerifyCommands: cmds},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != decide.ActExec || d.Op.Command != "takt verify --slug demo" {
		t.Fatalf("%d commands: %+v", cmds, d)
	}
	return d
}
