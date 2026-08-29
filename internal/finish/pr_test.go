package finish_test

import (
	"os"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/goals"
)

// prSpec is a spec shaped the way `takt next`'s brainstorm step leaves one:
// an H1, the opening paragraph, then the sections.
const prSpec = "# Add O'Brien's greeting\n\nFirst paragraph line one.\nline two.\n\n" +
	"## Scope\n\nnot the paragraph\n"

// demoGoals is one run's goals.md list, in file order.
var demoGoals = []goals.Goal{
	{ID: "G1", Text: "greet works", Signal: "test", Evidence: "go test ./..."},
	{ID: "G2", Text: "docs say so", Signal: "docs", Evidence: "README.md"},
}

// TestBuildPRTitleComesFromTheSpecHeading pins the title on the spec's own
// name for the change: the H1, with the marker stripped, whatever the topic
// says. The topic is what `takt init` recorded before the spec existed, so
// once there is a spec it is the weaker of the two names.
func TestBuildPRTitleComesFromTheSpecHeading(t *testing.T) {
	t.Parallel()
	pr := finish.BuildPR(prSpec, "some other topic", nil, nil, "docs/takt/demo")
	if pr.Title != "Add O'Brien's greeting" {
		t.Fatalf("title = %q", pr.Title)
	}
	if !strings.Contains(pr.Body, "First paragraph line one.\nline two.") {
		t.Fatalf("body must open with the spec's first paragraph:\n%s", pr.Body)
	}
	if strings.Contains(pr.Body, "not the paragraph") {
		t.Fatalf("the body must stop at the blank line:\n%s", pr.Body)
	}
}

// TestBuildPRTitleFallsBackToTheTopicByRunes covers a spec with no heading:
// the title is the topic, cut at prTitleMaxRunes. The cut counts runes, so a
// topic in a multi-byte script keeps 72 characters — not 72 bytes, which
// would be 24 of these and could split one in half.
func TestBuildPRTitleFallsBackToTheTopicByRunes(t *testing.T) {
	t.Parallel()
	const maxRunes = 72
	topic := strings.Repeat("あ", 100)
	pr := finish.BuildPR("no heading here\n", topic, nil, nil, "docs/takt/demo")
	if got := []rune(pr.Title); len(got) != maxRunes {
		t.Fatalf("title is %d runes, want %d: %q", len(got), maxRunes, pr.Title)
	}
	if pr.Title != strings.Repeat("あ", maxRunes) {
		t.Fatalf("title = %q", pr.Title)
	}
	if len(pr.Title) == maxRunes {
		t.Fatal("the fixture must be multi-byte for the rune cut to be proved")
	}
}

// TestBuildPRParagraphSkipsAHeadingAfterTheH1 covers the spec shape this
// repository writes: the H1 is followed by `## Why`, so the opening prose is
// under that heading and the body must reach past it rather than quoting a
// heading as the summary.
func TestBuildPRParagraphSkipsAHeadingAfterTheH1(t *testing.T) {
	t.Parallel()
	spec := "# Title\n\n## Why\n\nBecause the backlog is long.\nAnd it stays long.\n\nlater prose\n"
	pr := finish.BuildPR(spec, "topic", nil, nil, "docs/takt/demo")
	if !strings.HasPrefix(pr.Body, "Because the backlog is long.\nAnd it stays long.\n\n") {
		t.Fatalf("body:\n%s", pr.Body)
	}
	if strings.Contains(pr.Body, "later prose") || strings.Contains(pr.Body, "## Why") {
		t.Fatalf("body:\n%s", pr.Body)
	}
}

// TestBuildPRListsEveryGoalWithItsOutcome covers the `## Goals` section: the
// assessor's verdict, a waiver with its reason, and "not assessed" for a
// goal the record is silent about — the three things a reader of the pull
// request needs to tell apart.
func TestBuildPRListsEveryGoalWithItsOutcome(t *testing.T) {
	t.Parallel()
	gs := append([]goals.Goal{}, demoGoals...)
	gs = append(gs, goals.Goal{ID: "G3", Text: "never judged", Signal: "test", Evidence: "none"})
	rec := &finish.GoalsRecord{
		Verdicts: []finish.GoalVerdict{
			{ID: "G1", Verdict: "achieved", Evidence: "the test passes"},
			{ID: "G2", Verdict: "missed", Evidence: "no docs"},
		},
		Waived: map[string]string{"G2": "documented in the retro"},
	}
	body := finish.BuildPR(prSpec, "topic", gs, rec, "docs/takt/demo").Body
	for _, want := range []string{
		"## Goals",
		"- G1 — greet works — achieved",
		"- G2 — docs say so — waived (documented in the retro)",
		"- G3 — never judged — not assessed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body has no %q:\n%s", want, body)
		}
	}
}

// TestBuildPRWaiverWithoutAReasonIsStillAWaiver pins the waiver on the key,
// not on the text beside it: `takt waive` records a decision, and a record
// that carries the id with an empty reason still carries the decision. The
// alternative — falling through to the assessor's verdict — would print
// "missed" for a goal the user waived, which is the one reading of the pull
// request the waiver exists to prevent.
func TestBuildPRWaiverWithoutAReasonIsStillAWaiver(t *testing.T) {
	t.Parallel()
	rec := &finish.GoalsRecord{
		Verdicts: []finish.GoalVerdict{{ID: "G1", Verdict: "missed", Evidence: "no greeting"}},
		Waived:   map[string]string{"G1": ""},
	}
	body := finish.BuildPR(prSpec, "topic", demoGoals[:1], rec, "docs/takt/demo").Body
	if !strings.Contains(body, "- G1 — greet works — waived ()") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestBuildPRWithoutARecordAssessesNothing covers a run that reached the
// pull request with no finish/goals.json at all: every goal is listed and
// every one of them says so.
func TestBuildPRWithoutARecordAssessesNothing(t *testing.T) {
	t.Parallel()
	body := finish.BuildPR(prSpec, "topic", demoGoals, nil, "docs/takt/demo").Body
	if !strings.Contains(body, "- G1 — greet works — not assessed") ||
		!strings.Contains(body, "- G2 — docs say so — not assessed") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestBuildPRWithoutGoalsOmitsTheSection covers a --no-goals run: nil goals
// mean the run has none, so the body carries no empty `## Goals` heading
// that a reader would read as "the goals were all missed".
func TestBuildPRWithoutGoalsOmitsTheSection(t *testing.T) {
	t.Parallel()
	body := finish.BuildPR(prSpec, "topic", nil, nil, "docs/takt/demo").Body
	if strings.Contains(body, "## Goals") {
		t.Fatalf("body:\n%s", body)
	}
}

// TestBuildPRPointsAtTheBundle covers the `## Run` section: the pull request
// names where the run's own record lives, so a reviewer can read the spec,
// the plan and the reviews the change came out of.
func TestBuildPRPointsAtTheBundle(t *testing.T) {
	t.Parallel()
	body := finish.BuildPR(prSpec, "topic", nil, nil, "docs/takt/demo").Body
	const want = "## Run\n\nBundle: docs/takt/demo/ — spec.md, plan.md, reviews/, retro.md"
	if !strings.Contains(body, want) {
		t.Fatalf("body has no %q:\n%s", want, body)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("the body is a file; it must end in a newline: %q", body)
	}
}

// TestWritePRWritesTheBodyWhereTheOpPointsIt covers the write half: the file
// lands at finish/pr.md under the bundle, directories and all, because that
// is the path the op hands the session as --body-file.
func TestWritePRWritesTheBodyWhereTheOpPointsIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := finish.WritePR(dir, "# body\n"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(finish.PRPath(dir))
	if err != nil || string(b) != "# body\n" {
		t.Fatalf("%v %q", err, b)
	}
}
