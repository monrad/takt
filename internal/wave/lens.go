package wave

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monrad/takt/internal/backend"
	"github.com/monrad/takt/internal/bundle"
)

// Verdicts the verifier may return for one candidate (two-layers design §5.3).
const (
	VerdictConfirmed     = "confirmed"
	VerdictFalsePositive = "false_positive"
)

// LensFinding is one lens finding with the task takt attributed it to by
// its file — 0 when the file belongs to no task of the slice.
type LensFinding struct {
	backend.Finding

	Task int `json:"task"`
}

// DroppedFinding is a lens finding takt could not use — no file cited — kept
// on the record so the retro can count it (two-layers design §5.1).
type DroppedFinding struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// LensRecord is waves/<n>/lens-<lens>.s<slice>.a<attempt>.json.
type LensRecord struct {
	Lens       string           `json:"lens"`
	Wave       int              `json:"wave"`
	Slice      int              `json:"slice"`
	Attempt    int              `json:"attempt"`
	Model      string           `json:"model"`
	RecordedAt time.Time        `json:"recorded_at"`
	Findings   []LensFinding    `json:"findings"`
	Dropped    []DroppedFinding `json:"dropped,omitempty"`
}

// Candidate is one merged finding with a stable id the verifier's verdicts
// reference (two-layers design §5.2).
type Candidate struct {
	backend.Finding

	ID     string   `json:"id"`
	Task   int      `json:"task"`
	Lenses []string `json:"lenses"`
}

// CandidateVerdict is the verifier's judgment of one candidate.
type CandidateVerdict struct {
	ID        string   `json:"id"`
	Verdict   string   `json:"verdict"`
	Evidence  string   `json:"evidence"`
	Citations []string `json:"citations,omitempty"`
}

// InternalRecord is waves/<n>/internal.s<slice>.a<attempt>.json — the
// verified internal review of one dispatch (two-layers design §5.3).
type InternalRecord struct {
	Wave       int                `json:"wave"`
	Slice      int                `json:"slice"`
	Attempt    int                `json:"attempt"`
	Model      string             `json:"model"`
	RecordedAt time.Time          `json:"recorded_at"`
	Lenses     []string           `json:"lenses"`
	Candidates []Candidate        `json:"candidates"`
	Verdicts   []CandidateVerdict `json:"verdicts"`
	Confirmed  []string           `json:"confirmed"`
}

// InternalFinding is one confirmed finding as close-wave and the retry brief
// consume it: the finding plus the lenses that reported it.
type InternalFinding struct {
	backend.Finding

	Lenses []string `json:"lenses"`
}

// LensRecordPath is where one lens's record for one dispatch lives.
func LensRecordPath(bundleDir string, wave, slice, attempt int, lens string) string {
	return filepath.Join(bundleDir, "waves", strconv.Itoa(wave),
		fmt.Sprintf("lens-%s.s%d.a%d.json", lens, slice, attempt))
}

// InternalRecordPath is where the verified record for one dispatch lives.
func InternalRecordPath(bundleDir string, wave, slice, attempt int) string {
	return filepath.Join(bundleDir, "waves", strconv.Itoa(wave),
		fmt.Sprintf("internal.s%d.a%d.json", slice, attempt))
}

// ReadLensRecord returns nil, nil when the lens has no record for this
// dispatch — the sentinel every reader in this package uses for absence.
func ReadLensRecord(bundleDir string, wave, slice, attempt int, lens string) (*LensRecord, error) {
	return readJSONRecord[LensRecord](LensRecordPath(bundleDir, wave, slice, attempt, lens))
}

// WriteLensRecord writes the record atomically; a record without a slice is
// a caller bug, as for WriteClose.
func WriteLensRecord(bundleDir string, r LensRecord) error {
	if r.Slice < 1 {
		return fmt.Errorf("lens record for wave %d has no slice number", r.Wave)
	}
	return bundle.WriteJSONAtomic(LensRecordPath(bundleDir, r.Wave, r.Slice, r.Attempt, r.Lens), r)
}

// ReadInternalRecord returns nil, nil when this dispatch was never verified.
func ReadInternalRecord(bundleDir string, wave, slice, attempt int) (*InternalRecord, error) {
	return readJSONRecord[InternalRecord](InternalRecordPath(bundleDir, wave, slice, attempt))
}

// WriteInternalRecord writes the verified record atomically.
func WriteInternalRecord(bundleDir string, r InternalRecord) error {
	if r.Slice < 1 {
		return fmt.Errorf("internal record for wave %d has no slice number", r.Wave)
	}
	return bundle.WriteJSONAtomic(InternalRecordPath(bundleDir, r.Wave, r.Slice, r.Attempt), r)
}

// AllInternalRecords lists every verified record of a wave, slice then
// attempt order — the retro's input (two-layers design §9).
func AllInternalRecords(bundleDir string, wave int) ([]InternalRecord, error) {
	entries, err := os.ReadDir(filepath.Join(bundleDir, "waves", strconv.Itoa(wave)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []InternalRecord
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "internal.s") || !strings.HasSuffix(name, ".json") {
			continue
		}
		var r InternalRecord
		b, rerr := os.ReadFile(filepath.Join(bundleDir, "waves", strconv.Itoa(wave), name))
		if rerr != nil {
			return nil, rerr
		}
		if uerr := json.Unmarshal(b, &r); uerr != nil {
			return nil, fmt.Errorf("%s: %w", name, uerr)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slice != out[j].Slice {
			return out[i].Slice < out[j].Slice
		}
		return out[i].Attempt < out[j].Attempt
	})
	return out, nil
}

// readJSONRecord reads one record file, nil on absence.
func readJSONRecord[T any](path string) (*T, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		//nolint:nilnil // documented "not recorded yet" sentinel
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v T
	if uerr := json.Unmarshal(b, &v); uerr != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), uerr)
	}
	return &v, nil
}

// severityRank orders severities for the merge; unknown ranks last.
//
//nolint:mnd // magic numbers are severity ranks (lower = higher precedence)
var severityRank = map[string]int{"blocking": 0, "major": 1, "minor": 2, "nit": 3}

// MergeCandidates merges the lens records mechanically (two-layers design
// §5.2): same file and same line become one candidate — highest severity
// wins, the title and detail come from the earliest contributing lens in
// order — and everything else stays separate. Candidates are sorted by
// file, line then severity rank, and ids c1..cN assigned in that order, so
// every recomputation from the same records yields the same list.
func MergeCandidates(order []string, records map[string]*LensRecord) []Candidate {
	type key struct {
		file string
		line int
	}
	merged := map[key]*Candidate{}
	var keys []key
	for _, lens := range order {
		r := records[lens]
		if r == nil {
			continue
		}
		for _, f := range r.Findings {
			k := key{f.File, f.Line}
			c, ok := merged[k]
			if !ok {
				nc := Candidate{Finding: f.Finding, Task: f.Task, Lenses: []string{lens}}
				merged[k] = &nc
				keys = append(keys, k)
				continue
			}
			if !slices.Contains(c.Lenses, lens) {
				c.Lenses = append(c.Lenses, lens)
			}
			if severityRank[f.Severity] < severityRank[c.Severity] {
				c.Severity = f.Severity
			}
		}
	}
	out := make([]Candidate, 0, len(keys))
	for _, k := range keys {
		out = append(out, *merged[k])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return severityRank[out[i].Severity] < severityRank[out[j].Severity]
	})
	for i := range out {
		out[i].ID = "c" + strconv.Itoa(i+1)
	}
	return out
}

// ConfirmedByTask groups the confirmed candidates by the task they were
// attributed to; key 0 holds the unattributed ones.
func (r *InternalRecord) ConfirmedByTask() map[int][]InternalFinding {
	confirmed := map[string]bool{}
	for _, id := range r.Confirmed {
		confirmed[id] = true
	}
	out := map[int][]InternalFinding{}
	for _, c := range r.Candidates {
		if !confirmed[c.ID] {
			continue
		}
		out[c.Task] = append(out[c.Task], InternalFinding{Finding: c.Finding, Lenses: c.Lenses})
	}
	return out
}
