package deadline_test

import (
	"math"
	"testing"
	"time"

	"github.com/monrad/takt/internal/deadline"
)

// verifyWork is VerifyTimeout×VerifyCommands as a lower bound on Close,
// saturating at MaxDuration under the same domain rule the package states: a
// product that is not representable cannot be exceeded by anything but
// MaxDuration itself.
func verifyWork(b deadline.Budget) time.Duration {
	if b.VerifyTimeout <= 0 || b.VerifyCommands <= 0 {
		return 0
	}
	if int64(b.VerifyCommands) > int64(deadline.MaxDuration)/int64(b.VerifyTimeout) {
		return deadline.MaxDuration
	}
	return b.VerifyTimeout * time.Duration(b.VerifyCommands)
}

// reviewWork is the two backend passes one reviewed task costs, saturating.
func reviewWork(b deadline.Budget) time.Duration {
	if b.BackendTimeout <= 0 {
		return 0
	}
	if b.BackendTimeout > deadline.MaxDuration/2 {
		return deadline.MaxDuration
	}
	return 2 * b.BackendTimeout
}

// checkCloseBounds asserts the three lower bounds Close carries for every
// budget: the floor, the serial verify work, and both backend passes of a
// wave that reviews at least one task.
func checkCloseBounds(t *testing.T, b deadline.Budget, got time.Duration) {
	t.Helper()
	if got < 0 {
		t.Fatalf("Close(%+v) = %v: negative, so the arithmetic wrapped", b, got)
	}
	if got < deadline.Floor {
		t.Fatalf("Close(%+v) = %v; want at least Floor %v", b, got, deadline.Floor)
	}
	if w := verifyWork(b); got < w {
		t.Fatalf("Close(%+v) = %v; want at least the serial verify work %v", b, got, w)
	}
	if b.ReviewTasks >= 1 {
		if w := reviewWork(b); got < w {
			t.Fatalf("Close(%+v) = %v; want at least both backend passes %v", b, got, w)
		}
	}
}

// TestSessionStrictlyContainsEveryInner pins the containment the session side
// depends on: the deadline the session honours must fire after the binary's
// own, so the binary reports its timeout as a result instead of being killed
// mid-write. The rows stop one nanosecond short of saturation, where the
// strict form is still representable; MaxDuration itself is a row of
// TestSaturatesInsteadOfWrapping.
func TestSessionStrictlyContainsEveryInner(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		inner time.Duration
	}{
		{"zero", 0},
		{"negative clamps to zero", -time.Hour},
		{"one nanosecond", 1},
		{"the close floor", deadline.Floor},
		{"today's fixed close cap", 30 * time.Minute},
		{"a gate review", deadline.GateReview(15 * time.Minute)},
		{"an eight-task wave", deadline.Close(deadline.Budget{
			VerifyTimeout: 10 * time.Minute, VerifyCommands: 16,
			BackendTimeout: 15 * time.Minute, ReviewTasks: 8, MaxParallel: 8,
		})},
		{"a year", 365 * 24 * time.Hour},
		{"the last strictly containable input", deadline.MaxDuration - deadline.SessionMargin - 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := deadline.Session(c.inner)
			if got < 0 {
				t.Fatalf("Session(%v) = %v: negative, so the arithmetic wrapped", c.inner, got)
			}
			if got <= c.inner {
				t.Fatalf("Session(%v) = %v; the session deadline must strictly exceed the inner one", c.inner, got)
			}
			if want := max(c.inner, 0) + deadline.SessionMargin; got != want {
				t.Fatalf("Session(%v) = %v; want %v", c.inner, got, want)
			}
		})
	}
}

// TestCloseBudgetsTheWave pins Close's arithmetic and its lower bounds: verify
// counted serially (per command, undivided by MaxParallel), reviews counted in
// ceil(tasks/max_parallel) rounds of two backend passes, plus Overhead, never
// below Floor.
func TestCloseBudgetsTheWave(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		b    deadline.Budget
		want time.Duration
	}{
		{"the zero value floors", deadline.Budget{}, deadline.Floor},
		{
			"a one-task wave with no work floors",
			deadline.Budget{VerifyTimeout: 10 * time.Minute, VerifyCommands: 0, ReviewTasks: 0, MaxParallel: 1},
			deadline.Floor,
		},
		{
			"a one-task wave with a trivial review floors",
			deadline.Budget{BackendTimeout: time.Second, ReviewTasks: 1, MaxParallel: 1},
			deadline.Floor,
		},
		{
			"eight tasks, two verify commands each, exceeds today's fixed 30m",
			deadline.Budget{
				VerifyTimeout: 10 * time.Minute, VerifyCommands: 16,
				BackendTimeout: 15 * time.Minute, ReviewTasks: 8, MaxParallel: 8,
			},
			// 16×10m verify (serial) + 2×15m×1 review round + 2m overhead.
			192 * time.Minute,
		},
		{
			"eight tasks over eight workers is one review round",
			deadline.Budget{BackendTimeout: 15 * time.Minute, ReviewTasks: 8, MaxParallel: 8},
			32 * time.Minute,
		},
		{
			"nine tasks over eight workers is two review rounds",
			deadline.Budget{BackendTimeout: 15 * time.Minute, ReviewTasks: 9, MaxParallel: 8},
			62 * time.Minute,
		},
		{
			"verify is not divided by max_parallel",
			deadline.Budget{VerifyTimeout: 10 * time.Minute, VerifyCommands: 16, MaxParallel: 8},
			162 * time.Minute,
		},
		{
			"a wave with no reviewed task pays no review",
			deadline.Budget{
				VerifyTimeout: 10 * time.Minute, VerifyCommands: 16,
				BackendTimeout: 15 * time.Minute, ReviewTasks: 0, MaxParallel: 8,
			},
			162 * time.Minute,
		},
		{
			"max_parallel 0 behaves as 1",
			deadline.Budget{BackendTimeout: 15 * time.Minute, ReviewTasks: 3, MaxParallel: 0},
			92 * time.Minute,
		},
		{
			"max_parallel 1 is the same three rounds",
			deadline.Budget{BackendTimeout: 15 * time.Minute, ReviewTasks: 3, MaxParallel: 1},
			92 * time.Minute,
		},
		{
			"a negative max_parallel behaves as 1 too",
			deadline.Budget{BackendTimeout: 15 * time.Minute, ReviewTasks: 3, MaxParallel: -4},
			92 * time.Minute,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := deadline.Close(c.b)
			checkCloseBounds(t, c.b, got)
			if got != c.want {
				t.Fatalf("Close(%+v) = %v; want %v", c.b, got, c.want)
			}
		})
	}
	t.Run("the eight-task wave beats the fixed cap it replaces", func(t *testing.T) {
		t.Parallel()
		b := deadline.Budget{
			VerifyTimeout: 10 * time.Minute, VerifyCommands: 16,
			BackendTimeout: 15 * time.Minute, ReviewTasks: 8, MaxParallel: 8,
		}
		if got := deadline.Close(b); got <= 30*time.Minute {
			t.Fatalf("Close(%+v) = %v; the wave this budgets cannot fit in the old fixed 30m", b, got)
		}
	})
	t.Run("the round count stays positive at the int boundary", func(t *testing.T) {
		t.Parallel()
		// ceil computed as (tasks + parallel - 1) / parallel overflows int
		// here and hands back a negative round count, which zeroes the review
		// term: Close would drop to Floor and fail both bounds below.
		b := deadline.Budget{BackendTimeout: 15 * time.Minute, ReviewTasks: math.MaxInt, MaxParallel: 8}
		got := deadline.Close(b)
		checkCloseBounds(t, b, got)
		if got != deadline.MaxDuration {
			t.Fatalf("Close(%+v) = %v; want MaxDuration %v", b, got, deadline.MaxDuration)
		}
		fewer := b
		fewer.ReviewTasks = 9
		if got < deadline.Close(fewer) {
			t.Fatalf("Close is non-decreasing in ReviewTasks: %v < %v", got, deadline.Close(fewer))
		}
	})
}

// TestVerifyAndGateReviewBounds pins the two arithmetics this package took
// over from cmd_verify.go and cmd_review.go: a verify run gets one
// verify_timeout per command plus a fixed margin, and a gate review gets the
// backend's own timeout plus Grace — strictly more, so takt's deadline never
// fires before the backend's.
func TestVerifyAndGateReviewBounds(t *testing.T) {
	t.Parallel()
	t.Run("verify is per command plus a constant margin", verifyIsPerCommandPlusMargin)
	t.Run("a gate review strictly exceeds the backend's own timeout", gateReviewExceedsTheBackend)
}

// verifyIsPerCommandPlusMargin walks Verify below saturation: the result is
// per×cmds plus one constant margin, with negatives read as zero.
func verifyIsPerCommandPlusMargin(t *testing.T) {
	t.Parallel()
	margin := deadline.Verify(0, 0)
	if margin <= 0 {
		t.Fatalf("Verify(0, 0) = %v; a verify run needs slack even with no commands", margin)
	}
	cases := []struct {
		name string
		per  time.Duration
		cmds int
	}{
		{"no commands", 10 * time.Minute, 0},
		{"one command", 10 * time.Minute, 1},
		{"a wave's worth", 10 * time.Minute, 16},
		{"zero timeout", 0, 4},
		{"a negative timeout clamps", -time.Hour, 4},
		{"a negative count clamps", 10 * time.Minute, -3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := deadline.Verify(c.per, c.cmds)
			work := max(c.per, 0) * time.Duration(max(c.cmds, 0))
			if got < work {
				t.Fatalf("Verify(%v, %d) = %v; want at least per×cmds %v", c.per, c.cmds, got, work)
			}
			if want := work + margin; got != want {
				t.Fatalf("Verify(%v, %d) = %v; want %v", c.per, c.cmds, got, want)
			}
		})
	}
}

// gateReviewExceedsTheBackend walks GateReview below saturation, where the
// strict bound is representable: takt's deadline is the backend's own plus
// Grace, so a slow reviewer reports its own timeout instead of being cut off.
func gateReviewExceedsTheBackend(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		backend time.Duration
	}{
		{"zero", 0},
		{"negative clamps to zero", -time.Hour},
		{"the old 5m default", 5 * time.Minute},
		{"the new 15m default", 15 * time.Minute},
		{"the last strictly containable input", deadline.MaxDuration - deadline.Grace - 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := deadline.GateReview(c.backend)
			if got <= c.backend {
				t.Fatalf("GateReview(%v) = %v; takt's deadline must not fire before the backend's", c.backend, got)
			}
			if want := max(c.backend, 0) + deadline.Grace; got != want {
				t.Fatalf("GateReview(%v) = %v; want %v", c.backend, got, want)
			}
		})
	}
}

// TestSaturatesInsteadOfWrapping walks the domain rule's upper end, one row
// per function: at or above MaxDuration minus the work term the function adds,
// it returns exactly MaxDuration. Strict containment is unrepresentable there
// rather than unmet, so only the non-strict bounds are asserted — the strict
// rows live in the tests above, below saturation.
func TestSaturatesInsteadOfWrapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  func() time.Duration
		want time.Duration
	}{
		{"Session at the maximum", func() time.Duration { return deadline.Session(deadline.MaxDuration) },
			deadline.MaxDuration},
		{"Session exactly at its margin", func() time.Duration {
			return deadline.Session(deadline.MaxDuration - deadline.SessionMargin)
		}, deadline.MaxDuration},
		{"GateReview at the maximum", func() time.Duration { return deadline.GateReview(deadline.MaxDuration) },
			deadline.MaxDuration},
		{"GateReview exactly at Grace", func() time.Duration {
			return deadline.GateReview(deadline.MaxDuration - deadline.Grace)
		}, deadline.MaxDuration},
		{"Verify with a saturating product", func() time.Duration {
			return deadline.Verify(deadline.MaxDuration/2, 3)
		}, deadline.MaxDuration},
		{"Verify at its margin", func() time.Duration {
			return deadline.Verify(deadline.MaxDuration-30*time.Second, 1)
		}, deadline.MaxDuration},
		{"Close with a saturating verify term", func() time.Duration {
			return deadline.Close(deadline.Budget{VerifyTimeout: deadline.MaxDuration / 2, VerifyCommands: 3})
		}, deadline.MaxDuration},
		{"Close with a saturating review term", func() time.Duration {
			return deadline.Close(deadline.Budget{
				BackendTimeout: deadline.MaxDuration / 2, ReviewTasks: 1, MaxParallel: 1,
			})
		}, deadline.MaxDuration},
		{"Close with a saturating round count", func() time.Duration {
			return deadline.Close(deadline.Budget{
				BackendTimeout: time.Hour, ReviewTasks: math.MaxInt, MaxParallel: 1,
			})
		}, deadline.MaxDuration},
		{"Close with every field negative floors", func() time.Duration {
			return deadline.Close(deadline.Budget{
				VerifyTimeout: -time.Hour, VerifyCommands: -3,
				BackendTimeout: -time.Hour, ReviewTasks: -2, MaxParallel: -4,
			})
		}, deadline.Floor},
		{"Session of a saturated inner deadline", func() time.Duration {
			return deadline.Session(deadline.Close(deadline.Budget{
				VerifyTimeout: deadline.MaxDuration / 2, VerifyCommands: 3,
			}))
		}, deadline.MaxDuration},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := c.got()
			if got < 0 {
				t.Fatalf("%s = %v: negative, so the arithmetic wrapped", c.name, got)
			}
			if got != c.want {
				t.Fatalf("%s = %v; want %v", c.name, got, c.want)
			}
		})
	}
	t.Run("a saturated budget still meets every non-strict bound", func(t *testing.T) {
		t.Parallel()
		b := deadline.Budget{
			VerifyTimeout: deadline.MaxDuration / 2, VerifyCommands: 3,
			BackendTimeout: deadline.MaxDuration / 2, ReviewTasks: 4, MaxParallel: 2,
		}
		got := deadline.Close(b)
		checkCloseBounds(t, b, got)
		if got != deadline.MaxDuration {
			t.Fatalf("Close(%+v) = %v; want MaxDuration %v", b, got, deadline.MaxDuration)
		}
		if s := deadline.Session(got); s != deadline.MaxDuration {
			t.Fatalf("Session(MaxDuration) = %v; want MaxDuration, non-strictly containing it", s)
		}
	})
}

// TestMonotonicity holds the sound reading of the containment goal: every
// input that adds work can only push a deadline out. MaxParallel is the one
// documented exception and goes the other way — it is a divisor, reviews fan
// out across it, so more parallelism can only shrink the review term and
// Close is non-increasing in it. Requiring non-decreasing there would be
// unsatisfiable for the formula the package states.
func TestMonotonicity(t *testing.T) {
	t.Parallel()
	t.Run("close is non-decreasing in every work input", closeRisesWithWork)
	t.Run("close is non-increasing in max_parallel", closeFallsWithParallelism)
	t.Run("verify is non-decreasing in per and in cmds", verifyRisesWithWork)
	t.Run("gate review is non-decreasing in the backend timeout", gateReviewRisesWithTheBackend)
}

// sweep is how many steps each monotonicity walk takes.
const sweep = 24

// monotonicityBase is the budget the monotonicity walks vary one field of.
func monotonicityBase() deadline.Budget {
	return deadline.Budget{
		VerifyTimeout: 10 * time.Minute, VerifyCommands: 4,
		BackendTimeout: 15 * time.Minute, ReviewTasks: 5, MaxParallel: 3,
	}
}

// closeRisesWithWork walks each input that adds work upward and asserts Close
// never drops.
func closeRisesWithWork(t *testing.T) {
	t.Parallel()
	inputs := []struct {
		name string
		with func(int) deadline.Budget
	}{
		{"verify timeout", func(i int) deadline.Budget {
			b := monotonicityBase()
			b.VerifyTimeout = time.Duration(i) * time.Minute
			return b
		}},
		{"verify commands", func(i int) deadline.Budget {
			b := monotonicityBase()
			b.VerifyCommands = i
			return b
		}},
		{"backend timeout", func(i int) deadline.Budget {
			b := monotonicityBase()
			b.BackendTimeout = time.Duration(i) * time.Minute
			return b
		}},
		{"review tasks", func(i int) deadline.Budget {
			b := monotonicityBase()
			b.ReviewTasks = i
			return b
		}},
	}
	for _, in := range inputs {
		t.Run(in.name, func(t *testing.T) {
			t.Parallel()
			prev := time.Duration(0)
			for i := range sweep {
				b := in.with(i)
				got := deadline.Close(b)
				checkCloseBounds(t, b, got)
				if got < prev {
					t.Fatalf("Close(%+v) = %v; less than %v at the previous %s", b, got, prev, in.name)
				}
				prev = got
			}
		})
	}
}

// closeFallsWithParallelism is the documented exception: MaxParallel is a
// divisor, so raising it can only shrink the review term.
func closeFallsWithParallelism(t *testing.T) {
	t.Parallel()
	prev := deadline.MaxDuration
	for p := 1; p <= sweep; p++ {
		b := monotonicityBase()
		b.MaxParallel = p
		got := deadline.Close(b)
		checkCloseBounds(t, b, got)
		if got > prev {
			t.Fatalf("Close with max_parallel %d = %v; more than %v at the previous one — "+
				"more parallelism can only shrink the review term", p, got, prev)
		}
		prev = got
	}
}

// verifyRisesWithWork walks Verify's two inputs upward.
func verifyRisesWithWork(t *testing.T) {
	t.Parallel()
	prevPer, prevCmds := time.Duration(0), time.Duration(0)
	for i := range sweep {
		per := time.Duration(i) * time.Minute
		gotPer := deadline.Verify(per, 4)
		if gotPer < prevPer {
			t.Fatalf("Verify(%v, 4) = %v; less than %v at the previous per", per, gotPer, prevPer)
		}
		prevPer = gotPer
		gotCmds := deadline.Verify(10*time.Minute, i)
		if gotCmds < prevCmds {
			t.Fatalf("Verify(10m, %d) = %v; less than %v at the previous count", i, gotCmds, prevCmds)
		}
		prevCmds = gotCmds
	}
}

// gateReviewRisesWithTheBackend walks GateReview's single input upward.
func gateReviewRisesWithTheBackend(t *testing.T) {
	t.Parallel()
	prev := time.Duration(0)
	for i := range sweep {
		bt := time.Duration(i) * time.Minute
		got := deadline.GateReview(bt)
		if got < prev {
			t.Fatalf("GateReview(%v) = %v; less than %v at the previous timeout", bt, got, prev)
		}
		prev = got
	}
}
