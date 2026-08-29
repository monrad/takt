package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/config"
)

// TestBackendFallbackMatchesTheShippedDefault pins internal/backend's
// unset-Timeout fallback to config's shipped backends.<name>.timeout (spec
// §A1). The two are separate constants on purpose — internal/backend imports
// no takt package — and internal/cli is the only package that imports both,
// so this is where they are kept from drifting.
//
// The assertion is made by driving a ReviewRequest with no Timeout through
// the fake reviewer, which records the time left on the context the review
// actually ran under. That remaining time is the fallback minus the call's
// own elapsed time, so bounding it by the measured call — not by a slack
// constant — is what makes a fallback that differs from the shipped default
// by even a second fail here.
func TestBackendFallbackMatchesTheShippedDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "review-deadline")
	reg := backend.Registry(func(k string) string {
		if k == "TAKT_FAKE_REVIEW_TIMEOUT_FILE" {
			return path
		}
		return ""
	})

	before := time.Now()
	res, err := reg["fake"].Review(context.Background(), backend.ReviewRequest{})
	after := time.Now()
	if err != nil || res.Verdict != backend.VerdictApprove {
		t.Fatalf("fake review = %+v, %v", res, err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rem, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("recorded remaining time %q: %v", b, err)
	}

	want := time.Duration(config.Defaults().Backends.Copilot.Timeout)
	if got := time.Duration(config.Defaults().Backends.Claude.Timeout); got != want {
		t.Fatalf("the two shipped deadlines differ: copilot %s, claude %s", want, got)
	}
	if elapsed := after.Sub(before); rem > want || rem < want-elapsed {
		t.Fatalf("the review ran with %s left, want within %s of the shipped %s",
			rem, elapsed, want)
	}
}
