package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// Wave and Task locate a task-review follow-up. Wave is nil for a gate
	// follow-up and &n for a wave-n one: the pointer is what keeps wave 0 —
	// the first wave a run dispatches — distinguishable from "no wave",
	// which an int with omitempty could not express. Task is zero whenever
	// no single task owns the finding: a gate follow-up, or a wave one the
	// internal review left unattributed. Tasks are numbered from one, so
	// zero is never a task's own number (two-layers design §5.4).
	Wave *int `json:"wave,omitempty"`
	Task int  `json:"task,omitempty"`

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

// Key is a follow-up's identity: the JSON encoding of the seven-element
// array [gate, wave, task, severity, file, line, title], wave encoded as
// null when it is nil and every string trimmed of surrounding space. Detail,
// Source and TS are deliberately outside it — the same finding carried twice
// is the same finding however it was detailed, whoever declined it and
// whenever it arrived.
//
// The JSON encoding is what makes the key injective. encoding/json escapes
// the quote and encodes each element as its own array member, so no value a
// field can hold produces another field's key: a "|" or a '"' in a file name
// or a title cannot make two different findings collide, the way joining the
// elements with a delimiter would.
func (f FollowUp) Key() string {
	var w any
	if f.Wave != nil {
		w = *f.Wave
	}
	parts := []any{
		strings.TrimSpace(f.Gate), w, f.Task, strings.TrimSpace(f.Severity),
		strings.TrimSpace(f.File), f.Line, strings.TrimSpace(f.Title),
	}
	// The error is discarded because it cannot occur: parts holds only
	// strings, ints and nil, and [json.Marshal] fails only on channels,
	// funcs, complex numbers, cycles and other types none of the seven
	// elements can hold.
	b, _ := json.Marshal(parts)
	return string(b)
}

// AppendFollowUps adds items to follow-ups.json, idempotently. Append-only:
// a run accumulates follow-ups across gates and passes and nothing is ever
// removed, because they are retro input rather than a tracker.
//
// An item whose [FollowUp.Key] is already on file is not appended a second
// time — within one call as much as across calls — so a carry that runs
// twice (a retried review, a replayed close) leaves the file as one carry
// would. The one write to a stored item is an upgrade rather than a
// duplicate: a stored approve that comes back as an override becomes an
// override in place, keeping its ts, since the user declining a finding is
// strictly more decisive than a pass closing over it. No other field of a
// stored item is ever rewritten.
func AppendFollowUps(bundleDir string, items ...FollowUp) error {
	if len(items) == 0 {
		return nil
	}
	f, err := ReadFollowUps(bundleDir)
	if err != nil {
		return err
	}
	at := make(map[string]int, len(f.Items)+len(items))
	for i, it := range f.Items {
		if _, seen := at[it.Key()]; !seen {
			at[it.Key()] = i
		}
	}
	for _, it := range items {
		k := it.Key()
		i, seen := at[k]
		if !seen {
			f.Items = append(f.Items, it)
			at[k] = len(f.Items) - 1
			continue
		}
		if f.Items[i].Source == SourceApprove && it.Source == SourceOverride {
			f.Items[i].Source = SourceOverride
		}
	}
	return bundle.WriteJSONAtomic(followUpsPath(bundleDir), f)
}
