package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteJSONAtomic writes v to path as indented JSON, atomically: the bytes
// go to a temporary file in the target directory, are flushed to disk, and
// only then replace path. A crash therefore leaves either the record that
// was there or the whole new one, never half of either — and the durable
// records takt keeps (a wave's close record and parked baseline, the gate
// receipts, the finish records) all go through here, so that rule has one
// implementation rather than one per package. The directory is created when
// it does not exist, and the temporary file is removed on any failure.
func WriteJSONAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) } // best-effort: the write/close error is already returned
	if _, err = tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close() // best-effort: err is already returned
		cleanup()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close() // best-effort: err is already returned
		cleanup()
		return err
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err = os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
