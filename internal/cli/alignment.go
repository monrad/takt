package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
)

type alignmentVerdict struct {
	ID       string `json:"id"`
	Verdict  string `json:"verdict"` // covered | narrowed | dropped | widened | contradicted
	Evidence string `json:"evidence"`
}

// alignmentFile is alignment.json (spec §7.3).
type alignmentFile struct {
	AnchorHash string             `json:"anchor_hash"`
	Clauses    []brief.Clause     `json:"clauses"`
	Confirmed  bool               `json:"confirmed"`
	Skipped    bool               `json:"skipped,omitempty"`
	Verdicts   []alignmentVerdict `json:"verdicts,omitempty"`
}

// anchorHash binds a clause set to the anchor it was decomposed from, so an
// amended topic invalidates the audit instead of silently outliving it.
func anchorHash(topic string) string {
	sum := sha256.Sum256([]byte(topic))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func alignmentPath(bdir string) string { return filepath.Join(bdir, "alignment.json") }

// readAlignment returns nil, nil when the run has no alignment.json: the
// audit simply has not run yet, which every caller branches on.
//
//nolint:nilnil // documented "no alignment yet" sentinel, matching gate.ReadReceipt
func readAlignment(bdir string) (*alignmentFile, error) {
	b, err := os.ReadFile(alignmentPath(bdir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a alignmentFile
	if err = json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// writeAlignment replaces alignment.json atomically.
func writeAlignment(bdir string, a alignmentFile) error {
	return bundle.WriteJSONAtomic(alignmentPath(bdir), a)
}
