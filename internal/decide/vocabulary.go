package decide

import "github.com/monrad/takt/internal/op"

// Stop-reason names used at the stop(...) call sites in decide.go and
// returned by Vocab below — the same identifiers at both ends is the
// compile-time tie: renaming or dropping one without touching the other
// fails the build, not just a silently stale prompt. The run steps that used
// to share this block moved to [op.Steps] once `takt done --step` and next's
// run-op builder came to spell them too.
const (
	reasonWaveInFlight = "wave_in_flight"
	reasonArchived     = "archived"
)

// Vocabulary is everything `takt next` can put in an op that the command
// prompt must know how to execute (spec §6: "a test asserts that every op
// kind and every ask gate id Decide can emit appears in the prompt's table").
type Vocabulary struct {
	Gates        []string // ask gate ids
	RunSteps     []string // run steps
	ExecCommands []string // takt subcommands exec ops name
	StopReasons  []string // stop reasons
}

// Vocab is the single source of truth the prompt parity test reads.
func Vocab() Vocabulary {
	return Vocabulary{
		Gates: []string{gateOwner, gateReview, gateAlignmentConfirm, gatePlanInvalid, gateAgentInvalid,
			gateWaveFailures, gateReviewError, gateVerificationFailed, gateNoVerification, gateGoalsUnmet,
			gateBranchFinish},
		RunSteps:     op.Steps(),
		ExecCommands: []string{"review", "close-wave", "verify"},
		StopReasons:  []string{reasonWaveInFlight, reasonArchived},
	}
}
