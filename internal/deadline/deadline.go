// Package deadline derives the deadlines that wrap a backend call from the
// work they have to contain, rather than from fixed constants.
//
// One close-wave, one verify run and one gate review each hold a bounded
// amount of inner work — every verify command is capped by verify_timeout and
// every backend call by backends.<name>.timeout — so the enclosing deadline is
// a sum of bounded parts plus overhead, tight by construction. Its job is a
// backstop against a hang, never a budget the work is expected to fit into.
//
// The containment relation the whole package exists for is that an outer
// deadline must not fire before the inner one it wraps: the session honours
// [Session] of whatever cap the binary applies to itself, and the binary's own
// cap exceeds the backend's. A deadline that fires is then reported as a
// result by the process that owns the work, never as a kill from outside it.
//
// It imports no takt package by design, which is what lets both the binary and
// the session-op planner compute the same numbers from the same terms.
//
// # Saturating arithmetic over one declared domain
//
// A [time.Duration] is an int64 of nanoseconds, so plain + and * wrap negative
// near the maximum. Every step here saturates instead: a sum or product that
// would exceed [MaxDuration] yields [MaxDuration], and every negative duration
// or count is clamped to zero before use.
//
// The resulting domain rule is stated once and applies to every function
// alike. Let w be the work term a function adds to its input ([SessionMargin],
// [Grace], its own margin, [Overhead]):
//
//   - Below saturation, whenever the result is representable, the strict bound
//     holds: Session(x) > x and GateReview(bt) > bt.
//   - At or above MaxDuration-w the function returns exactly [MaxDuration].
//     Strict containment is then unrepresentable rather than unmet — a
//     deadline of 292 years is indistinguishable from no deadline — and only
//     the non-strict form is claimed.
//
// The uniformity is the point: an exemption granted to one function and not
// the others is what makes an invariant list quietly unsatisfiable.
package deadline

import (
	"math"
	"time"
)

// MaxDuration is the largest representable [time.Duration], about 292 years.
// Every function here returns it rather than a wrapped negative when its
// arithmetic would overflow.
const MaxDuration = time.Duration(math.MaxInt64)

// Overhead is what a close-wave spends outside its verify commands and its
// reviews: scope, the git reads and writes, result serialization and process
// start.
const Overhead = 2 * time.Minute

// SessionMargin is what the session allows on top of the deadline the binary
// applies to itself, so the binary always gets to report its own timeout as a
// result instead of being cut off mid-write.
const SessionMargin = 5 * time.Minute

// Floor is the smallest close-wave deadline. A wave of one task with no verify
// commands still gets slack for the work Overhead only approximates.
const Floor = 10 * time.Minute

// Bootstrap bounds opening a run's target — reading state, the bundle and the
// plan index — before the wave's own budget can be computed from them.
const Bootstrap = 2 * time.Minute

// Grace is what a gate review is allowed on top of the backend's own timeout:
// takt's deadline must not fire before the backend's, so a slow reviewer
// reports its own timeout instead of being cut off by takt's.
const Grace = 30 * time.Second

// verifyMargin is what a verify run is allowed on top of verify_timeout per
// command: process start and result serialization for each of them.
const verifyMargin = 30 * time.Second

// reviewPasses is the number of backend calls one task's review can cost: the
// blind pass, plus the scoped confirming second pass a task can trigger.
const reviewPasses = 2

// Budget is the work one close-wave has to fit into.
type Budget struct {
	VerifyTimeout  time.Duration // per verify command (config.verify_timeout)
	VerifyCommands int           // verify commands across the wave's done tasks
	BackendTimeout time.Duration // per backend call (backends.<name>.timeout)
	ReviewTasks    int           // tasks that get a backend review
	MaxParallel    int
}

// Close is the binary's own cap for a close-wave:
//
//	max(Floor, VerifyTimeout×VerifyCommands + 2×BackendTimeout×ceil(ReviewTasks/MaxParallel) + Overhead)
//
// Verify is counted serially and undivided by MaxParallel — verify_timeout
// applies per command, and the commands run one after another within a task
// and one task after another across the wave. Reviews are divided by it,
// because they fan out to MaxParallel goroutines. The 2× is the blind pass
// plus the possible scoped confirming second pass.
//
// There is deliberately no ceiling: every inner unit is separately bounded, so
// the sum is tight, and an upper clamp would reintroduce the arbitrary
// constant this package removes.
func Close(b Budget) time.Duration {
	total := addDur(addDur(verifyTerm(b), reviewTerm(b)), Overhead)
	if total < Floor {
		return Floor
	}
	return total
}

// Verify is the binary's own cap for a verify run of cmds commands, each
// bounded by per.
func Verify(per time.Duration, cmds int) time.Duration {
	return addDur(mulDur(per, cmds), verifyMargin)
}

// GateReview is the binary's own cap for a gate review against a backend whose
// own timeout is backend.
func GateReview(backend time.Duration) time.Duration {
	return addDur(backend, Grace)
}

// Session is the deadline the session honours for a command the binary caps at
// inner. It strictly exceeds inner everywhere below saturation.
func Session(inner time.Duration) time.Duration {
	return addDur(inner, SessionMargin)
}

// verifyTerm is the wave's serial verify time: one verify_timeout per command,
// undivided by MaxParallel.
func verifyTerm(b Budget) time.Duration {
	return mulDur(b.VerifyTimeout, b.VerifyCommands)
}

// reviewTerm is the wave's review time: reviewPasses backend calls per task,
// in ceil(ReviewTasks/MaxParallel) rounds of MaxParallel concurrent reviews.
// A wave with no reviewed task runs no round and so pays nothing.
func reviewTerm(b Budget) time.Duration {
	return mulDur(mulDur(b.BackendTimeout, reviewPasses), rounds(clampCount(b.ReviewTasks), b.MaxParallel))
}

// rounds is ceil(tasks/parallel) for tasks >= 0, with a parallel below one
// read as one. It divides first and then corrects for a remainder: the usual
// (tasks + parallel - 1) / parallel overflows int for a large tasks and hands
// back a negative round count, which would silently zero the review term.
func rounds(tasks, parallel int) int {
	if parallel < 1 {
		parallel = 1
	}
	n := tasks / parallel
	if tasks%parallel != 0 {
		n++
	}
	return n
}

// addDur adds two durations, clamping negatives to zero and saturating at
// MaxDuration instead of wrapping.
func addDur(a, b time.Duration) time.Duration {
	x, y := clampDur(a), clampDur(b)
	if x > MaxDuration-y {
		return MaxDuration
	}
	return x + y
}

// mulDur multiplies a duration by a count, reading either as zero when it is
// negative and saturating at MaxDuration instead of wrapping. Nothing
// negative reaches the multiplication, so no intermediate is ever wrapped.
func mulDur(d time.Duration, n int) time.Duration {
	if d <= 0 || n <= 0 {
		return 0
	}
	if int64(n) > int64(MaxDuration)/int64(d) {
		return MaxDuration
	}
	return d * time.Duration(n)
}

// clampDur reads a negative duration as zero.
func clampDur(d time.Duration) time.Duration {
	return max(d, 0)
}

// clampCount reads a negative count as zero.
func clampCount(n int) int {
	return max(n, 0)
}
