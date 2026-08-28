package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/monrad/takt/internal/bundle"
)

// FollowUp is a review finding that closed with its gate instead of being
// acted on: the gate approved and asked nothing of anyone, or the user
// explicitly declined. Recording it here is what stops an approving pass's
// minors from existing only in reviews/<gate>.md, reaching no plan and no
// follow-up (#29).
type FollowUp struct {
	Gate     string `json:"gate,omitempty"`
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	// Wave and Task locate a task-review follow-up; both are zero for a
	// gate follow-up (two-layers design §5.4).
	Wave   int       `json:"wave,omitempty"`
	Task   int       `json:"task,omitempty"`
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
	Source string    `json:"source"`
	TS     time.Time `json:"ts"`
}

// Sources a follow-up can come from.
const (
	SourceApprove  = "approve"
	SourceOverride = "override"
	// SourceInternal marks a confirmed internal-lens finding the backend's
	// verdict did not act on (two-layers design D11).
	SourceInternal = "internal"
)

// FollowUps is follow-ups.json.
type FollowUps struct {
	Items []FollowUp `json:"items"`
}

func followUpsPath(bundleDir string) string {
	return filepath.Join(bundleDir, "follow-ups.json")
}

// ReadFollowUps returns an empty list when the file is absent: a run that
// never carried anything forward simply has none.
func ReadFollowUps(bundleDir string) (FollowUps, error) {
	b, err := os.ReadFile(followUpsPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return FollowUps{}, nil
	}
	if err != nil {
		return FollowUps{}, err
	}
	var f FollowUps
	if uerr := json.Unmarshal(b, &f); uerr != nil {
		return FollowUps{}, fmt.Errorf("follow-ups.json: %w", uerr)
	}
	return f, nil
}

// AppendFollowUps adds items to follow-ups.json. Append-only: a run
// accumulates follow-ups across gates and passes and nothing removes them,
// because they are retro input rather than a tracker.
func AppendFollowUps(bundleDir string, items ...FollowUp) error {
	if len(items) == 0 {
		return nil
	}
	f, err := ReadFollowUps(bundleDir)
	if err != nil {
		return err
	}
	f.Items = append(f.Items, items...)
	return bundle.WriteJSONAtomic(followUpsPath(bundleDir), f)
}
