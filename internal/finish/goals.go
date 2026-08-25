package finish

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

// GoalVerdict is one goal's assessment (spec §7.5 step 2).
type GoalVerdict struct {
	ID        string   `json:"id"`
	Verdict   string   `json:"verdict"` // achieved | partial | missed
	Evidence  string   `json:"evidence"`
	Citations []string `json:"citations"`
}

// GoalsRecord is finish/goals.json: the verdicts at SHA plus any waivers.
type GoalsRecord struct {
	SHA      string            `json:"sha"`
	Verdicts []GoalVerdict     `json:"verdicts"`
	Waived   map[string]string `json:"waived,omitempty"` // goal id → reason
	At       time.Time         `json:"at"`
}

// Unmet lists goals neither achieved nor waived, in goals.md order.
func (r GoalsRecord) Unmet() []GoalVerdict {
	var out []GoalVerdict
	for _, v := range r.Verdicts {
		if v.Verdict != verdictAchieved && r.Waived[v.ID] == "" {
			out = append(out, v)
		}
	}
	return out
}

// verdictAchieved is the one verdict that satisfies a goal; everything else
// is unmet until it is waived.
const verdictAchieved = "achieved"

// verdicts is the closed set of verdicts an assessor may return.
var verdicts = map[string]bool{verdictAchieved: true, "partial": true, "missed": true}

// GoalsPath is where the record lives.
func GoalsPath(bundleDir string) string { return filepath.Join(bundleDir, "finish", "goals.json") }

// ReadGoals returns (nil, nil) when no record exists.
func ReadGoals(bundleDir string) (*GoalsRecord, error) {
	b, err := os.ReadFile(GoalsPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // documented "no record" sentinel, as ReadVerify
	}
	if err != nil {
		return nil, err
	}
	var r GoalsRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WriteGoals writes the record atomically.
func WriteGoals(bundleDir string, r GoalsRecord) error {
	return bundle.WriteJSONAtomic(GoalsPath(bundleDir), r)
}

// ParseVerdicts validates the assessor's JSON: every goal id exactly once,
// a known verdict, non-empty evidence. Unknown ids are rejected so a
// hallucinated goal cannot be "achieved".
func ParseVerdicts(js []byte, ids []string) ([]GoalVerdict, error) {
	var vs []GoalVerdict
	if err := json.Unmarshal(js, &vs); err != nil {
		return nil, fmt.Errorf("verdicts are not a JSON list: %w", err)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	seen := map[string]GoalVerdict{}
	for i := range vs {
		v := &vs[i]
		if err := checkVerdict(*v, want, seen); err != nil {
			return nil, err
		}
		if v.Citations == nil {
			v.Citations = []string{}
		}
		seen[v.ID] = *v
	}
	// Returned in goals.md order, not the order the assessor happened to
	// emit: the record is the user's list of goals with a verdict against
	// each, and Unmet walks it in that same order.
	out := make([]GoalVerdict, 0, len(ids))
	for _, id := range ids {
		v, ok := seen[id]
		if !ok {
			return nil, fmt.Errorf("goal %s has no verdict", id)
		}
		out = append(out, v)
	}
	return out, nil
}

// checkVerdict is ParseVerdicts's per-entry validation; seen holds the
// entries already accepted, keyed by goal id.
func checkVerdict(v GoalVerdict, want map[string]bool, seen map[string]GoalVerdict) error {
	_, dup := seen[v.ID]
	switch {
	case !want[v.ID]:
		return fmt.Errorf("verdict for unknown goal %q", v.ID)
	case dup:
		return fmt.Errorf("goal %s judged twice", v.ID)
	case !verdicts[v.Verdict]:
		return fmt.Errorf("goal %s: verdict %q is not achieved|partial|missed", v.ID, v.Verdict)
	case strings.TrimSpace(v.Evidence) == "":
		return fmt.Errorf("goal %s: evidence is empty", v.ID)
	}
	return nil
}
