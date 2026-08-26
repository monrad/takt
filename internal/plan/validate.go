package plan

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/config"
)

// MechanicalMaxFiles caps a `mechanical` task (spec §7.3).
const MechanicalMaxFiles = 3

// Problem is one validation failure. TaskID 0 means the whole index.
type Problem struct {
	TaskID  int
	Field   string
	Message string
}

func (p Problem) String() string {
	if p.TaskID == 0 {
		return p.Field + ": " + p.Message
	}
	return fmt.Sprintf("task %d %s: %s", p.TaskID, p.Field, p.Message)
}

// ValidateOpts carries the context validation needs; nil/empty members skip
// the corresponding optional check.
type ValidateOpts struct {
	RepoRoot        string
	MaxFilesPerTask int
	GoalIDs         []string          // nil → skip goal checks
	SpecHash        string            // "" → skip
	LookPath        func(string) bool // nil → skip
}

// Validate applies every rule in spec §7.3 and returns all problems found.
func Validate(idx Index, o ValidateOpts) []Problem {
	var ps []Problem
	add := func(id int, field, msg string) { ps = append(ps, Problem{TaskID: id, Field: field, Message: msg}) }

	validateHeader(idx, o, add)
	ids := validateTaskIDOrder(idx, add)
	for _, t := range idx.Tasks {
		validateTask(t, o, ids, add)
	}

	if _, err := AssignWaves(idx); err != nil {
		add(0, "depends_on", err.Error())
		return ps // reachability below assumes a DAG
	}
	validateOverlaps(idx, add)
	validateGoalCoverage(idx, o, add)
	return ps
}

// validateHeader checks the index-level schema and spec_hash.
func validateHeader(idx Index, o ValidateOpts, add func(int, string, string)) {
	if idx.Schema != 1 {
		add(0, "schema", fmt.Sprintf("unsupported schema %d (want 1)", idx.Schema))
	}
	// An index with no spec_hash and one with the wrong spec_hash are
	// different defects with different repairs. takt stamps the field itself
	// at `record --agent planner` (spec §7.3), so an empty one means the plan
	// was never recorded — not that it was drafted against a spec that has
	// since moved, which is what the drift message sends the reader looking
	// for.
	switch {
	case o.SpecHash == "":
	case idx.SpecHash == "":
		add(0, "spec_hash", "spec_hash not yet recorded — run `takt record --agent planner`")
	case idx.SpecHash != o.SpecHash:
		add(0, "spec_hash", "spec_hash does not match the current spec.md — the plan was drafted against an older spec")
	}
}

// validateTaskIDOrder checks that task ids are exactly 1..n in order and
// returns the set of known ids for dependency checks.
func validateTaskIDOrder(idx Index, add func(int, string, string)) map[int]bool {
	ids := map[int]bool{}
	for i, t := range idx.Tasks {
		if t.ID != i+1 {
			add(0, "tasks", "ids must be exactly 1..n in order")
			break
		}
		ids[t.ID] = true
	}
	return ids
}

// validateTask applies the per-task rules in spec §7.3.
func validateTask(t Task, o ValidateOpts, ids map[int]bool, add func(int, string, string)) {
	if strings.TrimSpace(t.Title) == "" {
		add(t.ID, "title", "title is empty")
	}
	if strings.TrimSpace(t.Description) == "" {
		add(t.ID, "description", "description is empty")
	}
	if !config.IsTaskClass(t.Class) {
		add(t.ID, "class",
			fmt.Sprintf("unknown class %q (want one of %s)", t.Class, strings.Join(config.TaskClasses, "|")))
	}
	validateTaskFiles(t, o, add)
	validateTaskVerify(t, o, add)
	for _, d := range t.DependsOn {
		if d == t.ID {
			add(t.ID, "depends_on", "a task cannot depend on itself")
		} else if !ids[d] {
			add(t.ID, "depends_on", fmt.Sprintf("unknown task %d", d))
		}
	}
	if o.GoalIDs != nil {
		for _, g := range t.Goals {
			if !slices.Contains(o.GoalIDs, g) {
				add(t.ID, "goals", "unknown goal "+g)
			}
		}
	}
}

// validateTaskFiles applies the files-list rules for one task. A file listed
// twice is reported as its own problem: left to validateOverlaps it came out
// as "tasks 1 and 1 share x.go … add depends_on", which points the planner
// at a dependency it cannot add (review minor finding).
func validateTaskFiles(t Task, o ValidateOpts, add func(int, string, string)) {
	if len(t.Files) == 0 {
		add(t.ID, "files", "files is empty — every task declares the files it may change")
	}
	seen := make(map[string]bool, len(t.Files))
	for _, f := range t.Files {
		if seen[f] {
			add(t.ID, "files", "duplicate file "+f)
			continue
		}
		seen[f] = true
		if err := bundle.CheckRelPath(o.RepoRoot, f); err != nil {
			add(t.ID, "files", err.Error())
		}
	}
	if o.MaxFilesPerTask > 0 && len(t.Files) > o.MaxFilesPerTask {
		add(t.ID, "files",
			fmt.Sprintf("%d files; at most %d per task — split the task", len(t.Files), o.MaxFilesPerTask))
	}
	if t.Class == "mechanical" && len(t.Files) > MechanicalMaxFiles {
		add(t.ID, "files", fmt.Sprintf("a mechanical task may touch at most %d files", MechanicalMaxFiles))
	}
}

// validateTaskVerify applies the verify-list rules for one task.
func validateTaskVerify(t Task, o ValidateOpts, add func(int, string, string)) {
	if len(t.Verify) == 0 {
		add(t.ID, "verify", "verify is empty — every task must prove itself")
	}
	for _, v := range t.Verify {
		tok := strings.Fields(v)
		if len(tok) == 0 {
			add(t.ID, "verify", "blank command")
			continue
		}
		if o.LookPath != nil && !o.LookPath(tok[0]) {
			add(t.ID, "verify", fmt.Sprintf("%q not found on PATH", tok[0]))
		}
	}
}

// validateOverlaps requires shared files to be ordered (transitively) by
// depends_on. Assumes idx is acyclic (checked by the caller). Each task
// contributes a file at most once, so a task never appears twice as an owner
// and can never be reported as sharing a file with itself — the duplicate
// itself is validateTaskFiles's problem to report.
func validateOverlaps(idx Index, add func(int, string, string)) {
	reach := reachability(idx)
	byFile := map[string][]int{}
	for _, t := range idx.Tasks {
		seen := make(map[string]bool, len(t.Files))
		for _, f := range t.Files {
			if seen[f] {
				continue
			}
			seen[f] = true
			byFile[f] = append(byFile[f], t.ID)
		}
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		owners := byFile[f]
		for i := range owners {
			for j := i + 1; j < len(owners); j++ {
				a, b := owners[i], owners[j]
				if !reach[a][b] && !reach[b][a] {
					add(
						b,
						"files",
						fmt.Sprintf(
							"tasks %d and %d share %s but neither depends on the other — add depends_on",
							a,
							b,
							f,
						),
					)
				}
			}
		}
	}
}

// validateGoalCoverage requires every declared goal to be served by at
// least one task.
func validateGoalCoverage(idx Index, o ValidateOpts, add func(int, string, string)) {
	if o.GoalIDs == nil {
		return
	}
	served := map[string]bool{}
	for _, t := range idx.Tasks {
		for _, g := range t.Goals {
			served[g] = true
		}
	}
	for _, g := range o.GoalIDs {
		if !served[g] {
			add(0, "goals", "goal "+g+" is served by no task")
		}
	}
}

// reachability returns reach[a][b] == true when b transitively depends on a.
func reachability(idx Index) map[int]map[int]bool {
	children := map[int][]int{}
	for _, t := range idx.Tasks {
		for _, d := range t.DependsOn {
			children[d] = append(children[d], t.ID)
		}
	}
	reach := map[int]map[int]bool{}
	for _, t := range idx.Tasks {
		reach[t.ID] = map[int]bool{}
		stack := append([]int{}, children[t.ID]...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if reach[t.ID][n] {
				continue
			}
			reach[t.ID][n] = true
			stack = append(stack, children[n]...)
		}
	}
	return reach
}
