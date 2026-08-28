package backend

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// fakeElapsed is the canned Elapsed a fake review reports.
const fakeElapsed = time.Millisecond

// defaultFakeResult is used when neither TAKT_FAKE_REVIEW_FILE nor
// TAKT_FAKE_REVIEW is set.
const defaultFakeResult = `{"verdict":"approve","summary":"fake approve"}`

// fakeReviewer returns a canned result (tests and dry runs). It never shells
// out, so it is what Task 9's integration test and the cli tests use.
type fakeReviewer struct{ getenv func(string) string }

func (f *fakeReviewer) Name() string { return nameFake }

func (f *fakeReviewer) Healthy(context.Context) error { return nil }

func (f *fakeReviewer) Review(ctx context.Context, req ReviewRequest) (ReviewResult, error) {
	logPrompt(req.LogDir, req.LogID, req.Prompt)

	if err := fakeDelay(ctx, f.getenv("TAKT_FAKE_REVIEW_SLEEP")); err != nil {
		return errorResult(nameFake, nameFake, err.Error(), "", 0), nil
	}

	raw := defaultFakeResult
	if p := f.getenv("TAKT_FAKE_REVIEW_FILE_" + rubricEnvKey(req.Rubric)); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return errorResult(nameFake, nameFake, err.Error(), "", 0), nil
		}
		raw = string(b)
	} else if fp := f.getenv("TAKT_FAKE_REVIEW_FILE"); fp != "" {
		b, err := os.ReadFile(fp)
		if err != nil {
			return errorResult(nameFake, nameFake, err.Error(), "", 0), nil
		}
		raw = string(b)
	} else if v := f.getenv("TAKT_FAKE_REVIEW"); v != "" {
		raw = v
	}

	var r ReviewResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return errorResult(nameFake, nameFake, err.Error(), raw, 0), nil
	}
	r.Provider, r.Model, r.Raw, r.Elapsed = nameFake, nameFake, raw, fakeElapsed
	return r, nil
}

// rubricEnvKey turns a rubric name into its env-var suffix: upper-cased,
// with '-' as '_', so "task-followup" reads TAKT_FAKE_REVIEW_FILE_TASK_FOLLOWUP.
func rubricEnvKey(rubric string) string {
	return strings.ToUpper(strings.ReplaceAll(rubric, "-", "_"))
}

// fakeDelay makes the fake reviewer take as long as TAKT_FAKE_REVIEW_SLEEP
// says, honouring the context it was handed. A real reviewer is minutes of
// backend work, and takt has to hand it a deadline sized for that rather
// than the one it bounds git with (spec §13) — this is the seam that lets a
// test prove which deadline the reviewer is actually running under. An
// unset or unparsable value means no delay.
func fakeDelay(ctx context.Context, v string) error {
	d, err := time.ParseDuration(v)
	if v == "" || err != nil || d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
