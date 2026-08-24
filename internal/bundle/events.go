package bundle

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Event is one line of events.jsonl (spec §4.4).
type Event struct {
	TS   time.Time      `json:"ts"`
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

// EventsPath returns bundleDir/events.jsonl.
func EventsPath(bundleDir string) string { return filepath.Join(bundleDir, "events.jsonl") }

// nowFunc is a seam for deterministic timestamps in tests.
var nowFunc = func() time.Time { return time.Now().UTC() }

// AppendEvent appends one event with O_APPEND (spec §13).
func AppendEvent(bundleDir, typ string, data map[string]any) error {
	if err := os.MkdirAll(bundleDir, 0o750); err != nil {
		return err
	}
	line, err := json.Marshal(Event{TS: nowFunc(), Type: typ, Data: data})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(EventsPath(bundleDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// scannerBufMax is the maximum size ReadEvents will grow its line buffer to,
// so a single event line up to 8MiB can be scanned.
const scannerBufMax = 8 * 1024 * 1024

// scannerBufStart is the initial capacity of the scanner's line buffer.
const scannerBufStart = 64 * 1024

// ReadEvents returns every event in order; a missing file is an empty log.
func ReadEvents(bundleDir string) ([]Event, error) {
	f, err := os.Open(EventsPath(bundleDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, scannerBufStart), scannerBufMax)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Event
		if uerr := json.Unmarshal(sc.Bytes(), &e); uerr != nil {
			return nil, uerr
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
