package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/goals"
)

// statusGoal is one goal as shown in status output (spec §11).
type statusGoal struct {
	ID     string
	Text   string
	Signal string
}

// statusInfo is a typed view of one bundle's status; statusDoc builds it and
// both statusJSON and renderStatus consume it directly, so neither renderer
// needs a type assertion on an `any`-valued map (errcheck's
// check-type-assertions is enabled repo-wide; see cmd_plan.go for the same
// pattern applied to plan.Validate's problems).
type statusInfo struct {
	Slug          string
	Phase         string
	Branch        string
	BranchAdopted bool
	Base          string
	BaseSHA       string
	TasksTotal    int
	TasksByStatus map[string]int
	Gates         map[string]string
	ActiveWave    *bundle.ActiveWave
	PendingGate   *bundle.PendingGate
	Goals         []statusGoal
	GoalsFrozen   bool
}

func cmdStatus(env Env) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slug := fs.String("slug", "", "run to show")
	asJSON := fs.Bool("json", false, "print a JSON document instead of text")
	if err := fs.Parse(env.Args); err != nil {
		return usageError(env, fs, err)
	}
	info, code := loadStatus(env, *dirFlag, *slug)
	if code != 0 {
		return code
	}
	if *asJSON {
		if err := writeJSON(env.Stdout, statusJSON(info)); err != nil {
			return 1
		}
		return 0
	}
	fmt.Fprint(env.Stdout, renderStatus(info))
	return 0
}

// loadStatus resolves the workspace and the selected bundle, then builds its
// status view (spec §11).
func loadStatus(env Env, dirFlag, slugFlag string) (statusInfo, int) {
	ws, err := openWorkspace(context.Background(), env, dirFlag)
	if err != nil {
		return statusInfo{}, fail(env.Stderr, 1, err.Error(), "")
	}
	s, err := selectSlug(ws, slugFlag)
	if err != nil {
		return statusInfo{}, fail(env.Stderr, 1, err.Error(), "use --slug <name>")
	}
	bdir, st, err := loadBundle(ws, s)
	if err != nil {
		return statusInfo{}, fail(env.Stderr, 1, err.Error(), "")
	}
	return statusDoc(bdir, st), 0
}

// statusDoc builds the machine-readable status (spec §11).
func statusDoc(bdir string, st *bundle.State) statusInfo {
	info := statusInfo{
		Slug: st.Slug, Phase: st.Phase, Branch: st.Branch, BranchAdopted: st.BranchAdopted,
		Base: st.Base, BaseSHA: st.BaseSHA,
		TasksTotal: len(st.Tasks), TasksByStatus: taskCounts(st.Tasks),
		Gates: st.Gates, ActiveWave: st.ActiveWave, PendingGate: st.PendingGate,
		Goals: []statusGoal{},
	}
	b, err := os.ReadFile(filepath.Join(bdir, "goals.md"))
	if err != nil {
		return info
	}
	if g, gerr := goals.Parse(b); gerr == nil {
		info.Goals = statusGoals(g.Items)
		info.GoalsFrozen = st.GoalsHash != nil && *st.GoalsHash == goals.Hash(b)
	}
	return info
}

// taskCounts tallies tasks by status (spec §4.3's closed status set).
func taskCounts(tasks []bundle.Task) map[string]int {
	counts := map[string]int{
		bundle.StatusPending: 0, bundle.StatusDone: 0, bundle.StatusFailed: 0,
		bundle.StatusBlocked: 0, bundle.StatusWaived: 0,
	}
	for _, t := range tasks {
		counts[t.Status]++
	}
	return counts
}

// statusGoals converts parsed goals.md items to the status view's shape.
func statusGoals(items []goals.Goal) []statusGoal {
	list := make([]statusGoal, 0, len(items))
	for _, it := range items {
		list = append(list, statusGoal{ID: it.ID, Text: it.Text, Signal: it.Signal})
	}
	return list
}

// statusJSON renders info as the --json document (keys fixed by spec §11).
func statusJSON(info statusInfo) map[string]any {
	goalsOut := make([]map[string]any, 0, len(info.Goals))
	for _, g := range info.Goals {
		goalsOut = append(goalsOut, map[string]any{"id": g.ID, "text": g.Text, "signal": g.Signal})
	}
	return map[string]any{
		keySlug: info.Slug, "phase": info.Phase, keyBranch: info.Branch, keyBranchAdopted: info.BranchAdopted,
		keyBase: info.Base, keyBaseSHA: info.BaseSHA,
		keyTasks:       map[string]any{"total": info.TasksTotal, "by_status": info.TasksByStatus},
		"gates":        info.Gates,
		"active_wave":  info.ActiveWave,
		"pending_gate": info.PendingGate,
		"goals":        goalsOut,
		"goals_frozen": info.GoalsFrozen,
	}
}

// renderStatus is the one-screen human view.
func renderStatus(info statusInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  phase=%s  branch=%s (base %s)\n", info.Slug, info.Phase, info.Branch, info.Base)
	c := info.TasksByStatus
	fmt.Fprintf(&b, "tasks: %d total — pending %d, done %d, failed %d, blocked %d, waived %d\n",
		info.TasksTotal, c[bundle.StatusPending], c[bundle.StatusDone], c[bundle.StatusFailed],
		c[bundle.StatusBlocked], c[bundle.StatusWaived])
	if info.ActiveWave != nil {
		fmt.Fprintf(&b, "active wave: %d (attempt %d, since %s)\n",
			info.ActiveWave.N, info.ActiveWave.Attempt, info.ActiveWave.StartedAt.Format("15:04:05"))
	}
	if info.PendingGate != nil {
		fmt.Fprintf(&b, "open gate: %s\n", info.PendingGate.ID)
	}
	if info.Gates != nil {
		fmt.Fprintf(&b, "gates: spec=%s plan=%s\n", info.Gates["spec"], info.Gates["plan"])
	}
	if len(info.Goals) > 0 {
		b.WriteString("goals:\n")
		for _, g := range info.Goals {
			fmt.Fprintf(&b, "  %s — %s (%s)\n", g.ID, g.Text, g.Signal)
		}
	}
	return b.String()
}
