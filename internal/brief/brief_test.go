package brief_test

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/brief"
)

func TestTokenAndQuote(t *testing.T) {
	t.Parallel()
	tok, err := brief.Token()
	if err != nil || !strings.HasPrefix(tok, "UNTRUSTED-ARTIFACT-") || len(tok) != len("UNTRUSTED-ARTIFACT-")+16 {
		t.Fatalf("%q %v", tok, err)
	}
	q, err := brief.Quote(tok, "spec.md", "hello\nworld")
	if err != nil || !strings.HasPrefix(q, "BEGIN "+tok+" spec.md\n") || !strings.HasSuffix(q, "\nEND "+tok+"\n") {
		t.Fatalf("%q %v", q, err)
	}
	if _, err = brief.Quote(tok, "x", "contains "+tok+" inside"); err == nil {
		t.Fatal("a collision must be an error")
	}
}

func TestImplementerBrief(t *testing.T) {
	t.Parallel()
	s, err := brief.Render("implementer", brief.ImplementerData{
		Slug:            "demo",
		Task:            2,
		Total:           3,
		Title:           "helper",
		Description:     "Add the helper.",
		Files:           []string{"a.go", "a_test.go"},
		Verify:          []string{"go test ./..."},
		Goals:           []brief.GoalLine{{ID: "G1", Text: "it works"}},
		SpecExcerpt:     "spec says so",
		Attempt:         2,
		PreviousModel:   "sonnet",
		PreviousFailure: "verify failed: go test",
		Findings:        []string{"a.go:3 nil deref"},
		Token:           "UNTRUSTED-ARTIFACT-abcdefabcdefabcd",
		BundleDirRel:    "docs/takt/demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"task 2 of 3", "- a.go", "- go test ./...", "G1 — it works", "STATUS: done | failed | blocked", "SUMMARY:", "BLOCKERS:", "Never commit", "docs/takt/demo", "attempt 2", "sonnet", "a.go:3 nil deref", "BEGIN UNTRUSTED-ARTIFACT-abcdefabcdefabcd"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "{{") {
		t.Fatal("unrendered template action")
	}
}

func TestPlannerAndReviewBriefs(t *testing.T) {
	t.Parallel()
	if p, err := brief.Render(
		"planner",
		brief.PlannerData{
			Slug:      "demo",
			Topic:     "t",
			SpecText:  "S",
			GoalsText: "G",
			Schema:    "{schema}",
			RepoRoot:  "/r",
			Token:     "UNTRUSTED-ARTIFACT-0000000000000000",
			MaxFiles:  12,
			Problems:  []string{"task 1 files: empty"},
			Attempt:   2,
		},
	); err != nil || !strings.Contains(p, "plan.index.json") || !strings.Contains(p, "task 1 files: empty") ||
		!strings.Contains(p, "depends_on") ||
		!strings.Contains(p, "at most 12") {
		t.Fatalf("%v\n%s", err, p)
	}
	for _, gate := range []string{"spec", "plan", "task"} {
		r, err := brief.Render(
			"review-"+gate,
			brief.ReviewData{
				Gate:            gate,
				Title:           "x",
				Token:           "UNTRUSTED-ARTIFACT-0000000000000000",
				Schema:          "{s}",
				Files:           map[string]string{"spec.md": "S"},
				Diff:            "+a",
				TaskDescription: "d",
				VerifyOutput:    "ok",
			},
		)
		if err != nil || !strings.Contains(r, "```json") || !strings.Contains(r, `"verdict"`) {
			t.Fatalf("%s: %v\n%s", gate, err, r)
		}
	}
	for _, mode := range []string{"clauses", "verdicts"} {
		a, err := brief.Render(
			"alignment-"+mode,
			brief.AlignmentData{
				Mode:      mode,
				Anchor:    "do X and Y",
				Token:     "UNTRUSTED-ARTIFACT-0000000000000000",
				Clauses:   []brief.Clause{{ID: "A1", Text: "do X", Span: "do X"}},
				SpecText:  "S",
				PlanText:  "P",
				IndexText: "{}",
			},
		)
		if err != nil || !strings.Contains(a, "A1") {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	for _, step := range []string{"run-brainstorm", "run-goals"} {
		s, err := brief.Render(
			step,
			brief.RunData{Slug: "demo", Topic: "t", SpecPath: "/b/spec.md", GoalsPath: "/b/goals.md"},
		)
		if err != nil || !strings.Contains(s, "/b/") {
			t.Fatalf("%s: %v", step, err)
		}
	}
	if _, err := brief.Render("nope", nil); err == nil {
		t.Fatal("unknown template must error")
	}
}
