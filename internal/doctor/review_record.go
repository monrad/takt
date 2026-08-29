package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/monrad/takt/internal/gate"
)

// reviewRecordCheckName names this check.
const reviewRecordCheckName = "review-record"

// ReviewRecord WARNs when reviews/<gate>.json — the structured findings a
// review pass wrote — carries a hash that no longer matches the gate's
// receipt in gates/<gate>.json (#43.3). Only a receipt that is a reviewer's
// answer is compared: an `error` verdict reviewed nothing, and a skip is a
// documented outage, not a review, so neither is a baseline a mismatch could
// be measured against. A findings file with no hash at all predates the
// field and is likewise skipped — PASS, not silently ignored. This check
// only reports the drift; priorFindingsForScopedPass's content-first
// reasoning about which findings to scope a confirming pass to is unchanged.
var ReviewRecord = Check{Name: reviewRecordCheckName, Run: func(_ context.Context, in Input) []Finding {
	var out []Finding
	for _, g := range []string{gate.Spec, gate.Plan} {
		if f, ok := reviewRecordFinding(in, g); ok {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		out = append(out, Finding{
			Level: levelPass, Check: reviewRecordCheckName, Slug: in.Slug,
			Message: "review records match their receipts",
		})
	}
	return out
}}

// reviewRecordFinding compares one gate's receipt against its findings file
// and returns the WARN when they disagree, ok false when there is nothing to
// report (no receipt, an error/skipped receipt, or a hashless/unreadable
// findings file).
func reviewRecordFinding(in Input, g string) (Finding, bool) {
	rc, err := gate.ReadReceipt(in.BundleDir, g)
	if err != nil || rc == nil || rc.Verdict == gate.VerdictError || rc.Skipped != nil {
		return Finding{}, false
	}
	// path is in.BundleDir/reviews/<gate>.json under the bundle dir; gate is
	// spec|plan (the fixed loop in ReviewRecord), so gosec's G304 does not fire.
	path := filepath.Join(in.BundleDir, "reviews", g+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return Finding{}, false
	}
	var rec struct {
		Hash string `json:"hash"`
	}
	if uerr := json.Unmarshal(b, &rec); uerr != nil || rec.Hash == "" || rec.Hash == rc.Hash {
		return Finding{}, false
	}
	return Finding{
		Level: levelWarn, Check: reviewRecordCheckName, Slug: in.Slug,
		Message: "reviews/" + g + ".json was written at a different hash than gates/" + g + ".json",
		Fix:     "takt review " + g + " --force --slug " + in.Slug,
	}, true
}
