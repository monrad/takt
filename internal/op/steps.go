package op

// Run steps a `run` op names and `takt done --step` closes (spec §5.2).
// decide's rows, done's flag parser and next's run-op builder all spell
// them, so they live here — one home, imported by every package that
// speaks the op protocol — instead of as three constant blocks that could
// drift apart without a compile error.
const (
	StepBrainstorm = "brainstorm"
	StepGoals      = "goals"
	StepRetro      = "retro"
	StepPushPR     = "push_pr"
)

// Steps returns the run steps in the order the loop reaches them.
func Steps() []string {
	return []string{StepBrainstorm, StepGoals, StepRetro, StepPushPR}
}
