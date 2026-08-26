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

// The agents takt dispatches: the three non-task ones Decide names (spec
// §5.3 rows 8, 10, 11, 21) and the implementer a wave launches. The same
// name identifies an agent in a dispatch op, in `takt record --agent`, in
// the agent_invalid gate's context and in the *_attempts_reset event `takt
// answer` appends, so — like the run steps above — they live here, imported
// by every package that speaks the op protocol, instead of as one constant
// block per package that could drift apart without a compile error.
const (
	AgentPlanner          = "planner"
	AgentAlignmentAuditor = "alignment-auditor"
	AgentGoalAssessor     = "goal-assessor"
	AgentImplementer      = "implementer"
)

// Agents returns every agent name a dispatch op can carry.
func Agents() []string {
	return []string{AgentPlanner, AgentAlignmentAuditor, AgentGoalAssessor, AgentImplementer}
}
