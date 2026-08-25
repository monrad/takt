package backend

import (
	"context"
	"encoding/json"
	"os"
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

func (f *fakeReviewer) Review(_ context.Context, req ReviewRequest) (ReviewResult, error) {
	logPrompt(req.LogDir, req.LogID, req.Prompt)

	raw := defaultFakeResult
	if p := f.getenv("TAKT_FAKE_REVIEW_FILE"); p != "" {
		b, err := os.ReadFile(p)
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
