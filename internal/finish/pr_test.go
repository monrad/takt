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

// issueURL71 and issueURL66 are the canonical "started from an issue" topic:
// `deriveSlug` accepts an issue URL, so a run begun that way has no `#N` in
// its topic at all and the closing line has to be built from the link.
const (
	issueURL71 = "https://github.com/monrad/takt/issues/71"
	issueURL66 = "https://github.com/monrad/takt/issues/66"
)

// TestBuildPRIssuesSection covers the three token forms the topic can name an
// issue with, their de-duplication and their order, and the tokens that are
// not references at all. The count assertion is the load-bearing one: the
// keyword has to be repeated per reference, because GitHub links only the
// first issue of a bare comma list and one `Closes` for four issues closes
// one. The negatives cover both boundaries of the bare form — a word or a
// slash before the `#`, a letter after the number — since the valid
// `owner/repo#N` row cannot prove that a bare tail is rejected on its own.
func TestBuildPRIssuesSection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		topic string
		want  []string
	}{
		{"no issue at all", "some other topic", nil},
		{"one bare number", "#74", []string{"#74"}},
		{"several bare numbers", "#66, #71, #72 and #74", []string{"#66", "#71", "#72", "#74"}},
		{"part of an issue", "#49 item 1", []string{"#49"}},
		{"an issue url", "fix " + issueURL71, []string{issueURL71}},
		{"a cross-repository token", "monrad/takt#71", []string{"monrad/takt#71"}},
		{
			"a mix of forms",
			"monrad/takt#71, then " + issueURL66 + ", then #72",
			[]string{"monrad/takt#71", issueURL66, "#72"},
		},
		{"a repeat of one form", "#71 and again #71", []string{"#71"}},
		{
			"two forms naming one issue",
			"#71 and " + issueURL71,
			[]string{"#71", issueURL71},
		},
		{"a letter stuck to the number", "#71b", nil},
		{"a bare issues fragment", "see /issues/12", nil},
		{"a word before the hash", "abc#71", nil},
		{"a repository-like word before the hash", "takt#71", nil},
		{"a slash before the hash", "/#71", nil},
		{"a malformed cross-repository token", "owner/#71", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertIssuesSection(t, finish.BuildPR(prSpec, tc.topic, demoGoals, nil, "docs/takt/demo").Body, tc.want)
		})
	}
}

// assertIssuesSection is TestBuildPRIssuesSection's per-row check: one
// closing keyword per reference — the assertion that fails if the keyword is
// ever emitted once for a comma list — every reference rendered verbatim, in
// the topic's order, and the whole section present only when there is one.
func assertIssuesSection(t *testing.T, body string, want []string) {
	t.Helper()
	lower := strings.ToLower(body)
	if got := strings.Count(lower, "closes "); got != len(want) {
		t.Fatalf("%d closing keywords for %d references:\n%s", got, len(want), body)
	}
	if got := strings.Contains(body, "## Issues"); got != (len(want) > 0) {
		t.Fatalf("`## Issues` present = %v:\n%s", got, body)
	}
	prev := -1
	for _, ref := range want {
		if !strings.Contains(body, ref) {
			t.Fatalf("body does not name %q verbatim:\n%s", ref, body)
		}
		at := strings.Index(lower, "closes "+strings.ToLower(ref))
		if at < 0 {
			t.Fatalf("%q carries no closing keyword:\n%s", ref, body)
		}
		if at <= prev {
			t.Fatalf("%q is out of the topic's order:\n%s", ref, body)
		}
		prev = at
	}
}

// TestBuildPRIssuesSectionSitsBetweenGoalsAndRun pins the position the user
// chose: the issues the run set out to fix are read after the verdicts on its
// goals and before the pointer at the bundle.
func TestBuildPRIssuesSectionSitsBetweenGoalsAndRun(t *testing.T) {
	t.Parallel()
	body := finish.BuildPR(prSpec, "fix #71 and #74", demoGoals, nil, "docs/takt/demo").Body
	goalsAt, issuesAt, runAt := strings.Index(body, "## Goals"),
		strings.Index(body, "## Issues"), strings.Index(body, "## Run")
	if goalsAt < 0 || issuesAt >= runAt || goalsAt >= issuesAt {
		t.Fatalf("goals at %d, issues at %d, run at %d:\n%s", goalsAt, issuesAt, runAt, body)
	}
	const want = "## Issues\n\nThese are the issues this run set out to fix; " +
		"`## Goals` above says which of them it proved.\n\nCloses #71, closes #74"
	if !strings.Contains(body, want) {
		t.Fatalf("body has no %q:\n%s", want, body)
	}
}

// TestBuildPRIssuesSectionWithoutGoalsDropsTheClause covers a --no-goals run:
// the sentence must not point at a `## Goals` section the body does not
// render, so it stops at the issues themselves.
func TestBuildPRIssuesSectionWithoutGoalsDropsTheClause(t *testing.T) {
	t.Parallel()
	body := finish.BuildPR(prSpec, "fix #74", nil, nil, "docs/takt/demo").Body
	const want = "## Issues\n\nThese are the issues this run set out to fix.\n\nCloses #74"
	if !strings.Contains(body, want) {
		t.Fatalf("body has no %q:\n%s", want, body)
	}
	if strings.Contains(body, "## Goals") {
		t.Fatalf("body:\n%s", body)
	}
}
