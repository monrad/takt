package backend_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
)

func TestExtractJSONPrefersFencedBlock(t *testing.T) {
	t.Parallel()
	text := "Some prose {\"not\":\"this\"}\n```json\n{\"verdict\":\"rework\",\"summary\":\"s\"}\n```\ntrailing"
	b, err := backend.ExtractJSON(text)
	if err != nil || !strings.Contains(string(b), `"rework"`) {
		t.Fatalf("%s %v", b, err)
	}
	b, err = backend.ExtractJSON(`prefix {"a":{"b":1}} suffix {"verdict":"approve"}`)
	if err != nil || string(b) != `{"verdict":"approve"}` {
		t.Fatalf("last top-level object: %s %v", b, err)
	}
	if _, err = backend.ExtractJSON("no json here"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractJSONHandlesBracesInsideStrings(t *testing.T) {
	t.Parallel()
	text := `prose {"verdict":"approve","summary":"see {details} \"here\" too"} trailing`
	want := `{"verdict":"approve","summary":"see {details} \"here\" too"}`
	b, err := backend.ExtractJSON(text)
	if err != nil || string(b) != want {
		t.Fatalf("%s %v", b, err)
	}
}

func TestParseResultValidatesVerdict(t *testing.T) {
	t.Parallel()
	r, err := backend.ParseResult(
		[]byte(
			`{"verdict":"approve","summary":"ok","findings":[{"severity":"minor","file":"a.go","line":3,"title":"t","detail":"d"}]}`,
		),
	)
	if err != nil || r.Verdict != backend.VerdictApprove || len(r.Findings) != 1 || r.Findings[0].Line != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = backend.ParseResult([]byte(`{"verdict":"maybe"}`)); err == nil {
		t.Fatal("unknown verdict must fail")
	}
}

func TestFakeReviewerFromEnvAndFile(t *testing.T) {
	t.Parallel()
	logDir := t.TempDir()
	reg := backend.Registry(func(k string) string {
		if k == "TAKT_FAKE_REVIEW" {
			return `{"verdict":"rework","summary":"needs work"}`
		}
		return ""
	})
	fake := reg["fake"]
	if err := fake.Healthy(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := fake.Review(
		context.Background(),
		backend.ReviewRequest{Rubric: "task", Prompt: "PROMPT-BODY", LogDir: logDir, LogID: "t1", Timeout: time.Second},
	)
	if err != nil || res.Verdict != backend.VerdictRework || res.Provider != "fake" {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(logDir, "t1.prompt")); string(b) != "PROMPT-BODY" {
		t.Fatalf("prompt not logged: %q", b)
	}
	f := filepath.Join(t.TempDir(), "r.json")
	_ = os.WriteFile(f, []byte(`{"verdict":"reject","summary":"no"}`), 0o600)
	reg = backend.Registry(func(k string) string {
		if k == "TAKT_FAKE_REVIEW_FILE" {
			return f
		}
		return ""
	})
	if res, _ = reg["fake"].Review(
		context.Background(),
		backend.ReviewRequest{},
	); res.Verdict != backend.VerdictReject {
		t.Fatalf("%+v", res)
	}
	reg = backend.Registry(func(string) string { return "" })
	if res, _ = reg["fake"].Review(
		context.Background(),
		backend.ReviewRequest{},
	); res.Verdict != backend.VerdictApprove {
		t.Fatalf("default must approve: %+v", res)
	}
}

type stub struct {
	name    string
	healthy error
}

func (s stub) Name() string                  { return s.name }
func (s stub) Healthy(context.Context) error { return s.healthy }
func (s stub) Review(context.Context, backend.ReviewRequest) (backend.ReviewResult, error) {
	return backend.ReviewResult{Verdict: backend.VerdictApprove, Provider: s.name}, nil
}

func TestSelectFirstHealthy(t *testing.T) {
	t.Parallel()
	reg := map[string]backend.Reviewer{
		"copilot": stub{"copilot", os.ErrNotExist},
		"claude":  stub{"claude", nil},
	}
	r, err := backend.Select(context.Background(), []string{"copilot", "claude"}, reg)
	if err != nil || r.Name() != "claude" {
		t.Fatalf("%v %v", r, err)
	}
	if _, err = backend.Select(context.Background(), []string{"copilot"}, reg); err == nil {
		t.Fatal("no healthy reviewer must error")
	}
	if _, err = backend.Select(context.Background(), []string{"nope"}, reg); err == nil {
		t.Fatal("unknown backend must error")
	}
}

func TestSeverityCountsTalliesBySeverity(t *testing.T) {
	t.Parallel()
	r := backend.ReviewResult{Findings: []backend.Finding{
		{Severity: "blocking"}, {Severity: "minor"}, {Severity: "minor"},
	}}
	got := r.SeverityCounts()
	if got["blocking"] != 1 || got["minor"] != 2 {
		t.Fatalf("SeverityCounts() = %v", got)
	}
	if got["nit"] != 0 {
		t.Fatalf("an absent severity must tally to zero, got %d", got["nit"])
	}
	if none := (backend.ReviewResult{}).SeverityCounts(); none != nil {
		t.Fatalf("no findings must tally to nil, got %v", none)
	}
}
