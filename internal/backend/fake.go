package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

// fakeElapsed is the canned Elapsed a fake review reports.
const fakeElapsed = time.Millisecond

// defaultFakeResult is used when neither TAKT_FAKE_REVIEW_FILE nor
// TAKT_FAKE_REVIEW is set. A third variable, TAKT_FAKE_REVIEW_CALLS, does not
// choose the result at all: it names the file recordReviewCall records every
// call in.
const defaultFakeResult = `{"verdict":"approve","summary":"fake approve"}`

// fakeReviewer returns a canned result (tests and dry runs). It never shells
// out, so it is what Task 9's integration test and the cli tests use.
type fakeReviewer struct{ getenv func(string) string }

func (f *fakeReviewer) Name() string { return nameFake }

func (f *fakeReviewer) Healthy(context.Context) error { return nil }

func (f *fakeReviewer) Review(ctx context.Context, req ReviewRequest) (ReviewResult, error) {
	if p := f.getenv("TAKT_FAKE_REVIEW_CALLS"); p != "" {
		if err := recordReviewCall(p, req.Rubric, req.LogID); err != nil {
			return errorResult(nameFake, nameFake, err.Error(), "", 0), nil
		}
	}

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

// recordReviewCall appends one "<rubric> <logID>" line to the file
// TAKT_FAKE_REVIEW_CALLS names — the fake's call log, the sibling of
// TAKT_FAKE_REVIEW_FILE's canned result. A test that sets the variable learns
// the exact LogID takt minted for each call and can then read that call's
// prompt log by name, instead of scanning the log directory and guessing
// which file belongs to which call. A failure to record is not swallowed: the
// caller turns it into an error verdict, so a test whose lookup would
// otherwise be silently addressing the wrong call fails instead.
func recordReviewCall(path, rubric, logID string) error {
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fh.WriteString(rubric + " " + logID + "\n")
	return errors.Join(err, fh.Close())
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
