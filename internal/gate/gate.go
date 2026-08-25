// Package gate implements the hash-bound review receipts of spec §9: a gate
// is satisfied only by a receipt taken at the current content hash of its
// artifacts, so any edit re-arms it.
package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/plan"
)

// Gate ids.
const (
	Spec = "spec"
	Plan = "plan"
)

// Verdicts a receipt may carry.
const (
	VerdictApprove = "approve"
	VerdictRework  = "rework"
	VerdictReject  = "reject"
	VerdictError   = "error"
)

// Reviewer records who produced a receipt.
type Reviewer struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Skipped is an evidenced backend outage (never a convenience).
type Skipped struct {
	Reason       string `json:"reason"`
	EvidencePath string `json:"evidence_path"`
}

// Receipt is gates/<gate>.json.
type Receipt struct {
	Gate     string    `json:"gate"`
	Hash     string    `json:"hash"`
	Verdict  string    `json:"verdict"`
	Reviewer Reviewer  `json:"reviewer"`
	Findings string    `json:"findings"`
	TS       time.Time `json:"ts"`
	Skipped  *Skipped  `json:"skipped"`
}

// Status is the computed state of a gate.
type Status struct {
	Satisfied bool
	Verdict   string
	Hash      string
}

// Artifacts lists the files a gate hashes, in order.
func Artifacts(gate string) []string {
	switch gate {
	case Spec:
		return []string{"spec.md", "goals.md"}
	case Plan:
		return []string{"spec.md", "plan.md", "plan.index.json"}
	}
	return nil
}

// Hash computes the gate's content hash. goals.md may be absent (goals can
// be off); every other artifact must exist. plan.index.json contributes its
// canonical bytes so the display-only wave field never moves the hash.
func Hash(gate, bundleDir string) (string, []string, error) {
	arts := Artifacts(gate)
	if arts == nil {
		return "", nil, fmt.Errorf("unknown gate %q", gate)
	}
	h := sha256.New()
	var present []string
	for _, name := range arts {
		b, err := os.ReadFile(filepath.Join(bundleDir, name))
		if errors.Is(err, os.ErrNotExist) && name == "goals.md" {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("gate %s: %w", gate, err)
		}
		if name == "plan.index.json" {
			idx, perr := plan.ParseIndex(b)
			if perr != nil {
				return "", nil, perr
			}
			cb, cerr := plan.Canonical(idx)
			if cerr != nil {
				return "", nil, cerr
			}
			b = cb
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		present = append(present, name)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), present, nil
}

func receiptPath(bundleDir, gate string) string {
	return filepath.Join(bundleDir, "gates", gate+".json")
}

// ReadReceipt returns nil, nil when no receipt exists.
func ReadReceipt(bundleDir, gate string) (*Receipt, error) {
	b, err := os.ReadFile(receiptPath(bundleDir, gate))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // absence is not an error: nil,nil is the documented "no receipt" contract (spec §9)
	}
	if err != nil {
		return nil, err
	}
	var r Receipt
	if uerr := json.Unmarshal(b, &r); uerr != nil {
		return nil, fmt.Errorf("gates/%s.json: %w", gate, uerr)
	}
	return &r, nil
}

// WriteReceipt writes gates/<gate>.json atomically.
func WriteReceipt(bundleDir string, r Receipt) error {
	dir := filepath.Join(bundleDir, "gates")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, r.Gate+".json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) } // best-effort: the write/close error is already returned
	if _, werr := tmp.Write(append(b, '\n')); werr != nil {
		_ = tmp.Close() // best-effort: werr is already returned
		cleanup()
		return werr
	}
	if cerr := tmp.Close(); cerr != nil {
		cleanup()
		return cerr
	}
	return os.Rename(tmpName, receiptPath(bundleDir, r.Gate))
}

// Compute derives the gate's status from the current hash, the receipt and
// any gate_overridden event (spec §9).
func Compute(bundleDir, gate string, events []bundle.Event) (Status, error) {
	cur, _, err := Hash(gate, bundleDir)
	if err != nil {
		return Status{}, err
	}
	st := Status{Hash: cur}
	for _, e := range events {
		if e.Type == "gate_overridden" && e.Data["gate"] == gate && e.Data["hash"] == cur {
			return Status{Satisfied: true, Verdict: "overridden", Hash: cur}, nil
		}
	}
	r, err := ReadReceipt(bundleDir, gate)
	if err != nil || r == nil {
		return st, err
	}
	if r.Hash != cur {
		return st, nil // stale receipt: the artifact was edited
	}
	switch {
	case r.Skipped != nil:
		st.Satisfied, st.Verdict = true, "skipped"
	case r.Verdict == VerdictApprove:
		st.Satisfied, st.Verdict = true, r.Verdict
	default:
		st.Verdict = r.Verdict
	}
	return st, nil
}
