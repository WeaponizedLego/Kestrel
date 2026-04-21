// Package journal is the write-ahead log for Kestrel's file operations.
// Every move/delete appends a *-pending record before the filesystem is
// touched and a matching *-complete (or *-failed) record after, so a
// crash between the two can be detected on next startup by Replay and
// either rolled back or finished.
//
// The format is line-delimited JSON — one record per line, fsynced on
// Append — because it's trivial to reason about, trivially
// forward-compatible (add fields, old readers ignore them), and
// trivially repairable by hand if the tooling ever fails us. A torn
// trailing line (crash mid-Append) is silently dropped by Replay; the
// invariant is that any *-pending whose matching *-complete never
// landed is recoverable.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Kind identifies the operation a record describes. Stored as a string
// so a future new kind doesn't collide numerically with an old one.
type Kind string

const (
	KindMove      Kind = "move"
	KindDelete    Kind = "delete"
	KindPermDel   Kind = "permanent-delete"
	KindRestore   Kind = "restore"
)

// State tracks where in its lifecycle a record sits. Replay uses this
// to classify pending operations — any Pending without a matching
// Complete/Failed is a recovery candidate.
type State string

const (
	StatePending  State = "pending"
	StateComplete State = "complete"
	StateFailed   State = "failed"
)

// Entry is a single journal record. Fields are JSON-tagged so a crash
// dump is human-readable. Every field after ID/Kind/State/Timestamp is
// optional — kinds use what they need.
type Entry struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	State     State     `json:"state"`
	Timestamp time.Time `json:"ts"`

	// Move / Restore fields
	OldPath string `json:"old_path,omitempty"`
	NewPath string `json:"new_path,omitempty"`

	// Delete fields
	Path       string `json:"path,omitempty"`
	TrashPath  string `json:"trash_path,omitempty"`

	// Photo hash for cross-reference to library / thumbs.pack. Stored
	// so a restore can rebuild the library entry without another scan.
	PhotoHash string `json:"photo_hash,omitempty"`

	// Error message when State==StateFailed, for operator diagnosis.
	Error string `json:"error,omitempty"`
}

// Journal is the append-only log. Construct with Open; serialize
// writes under Append. Safe for concurrent Append calls.
type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// Open creates or opens the journal file at path. The parent directory
// is created if missing. A subsequent Replay can be called to inspect
// prior-run state.
func Open(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating journal dir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening journal %s: %w", path, err)
	}
	return &Journal{path: path, f: f}, nil
}

// Close flushes and releases the file handle.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// Append writes one entry and fsyncs before returning. The fsync is
// non-negotiable: the whole point of the journal is to survive a crash
// between Append and the filesystem op it describes, so the record
// must be durable before the caller proceeds.
func (j *Journal) Append(e Entry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshalling journal entry %s: %w", e.ID, err)
	}
	payload = append(payload, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return errors.New("journal: append on closed file")
	}
	if _, err := j.f.Write(payload); err != nil {
		return fmt.Errorf("appending journal entry %s: %w", e.ID, err)
	}
	if err := j.f.Sync(); err != nil {
		return fmt.Errorf("fsyncing journal after %s: %w", e.ID, err)
	}
	return nil
}

// Path returns the on-disk path the journal is written to. Useful for
// tests and for Rotate callers.
func (j *Journal) Path() string {
	return j.path
}

// Replay scans the journal file start-to-finish and returns every
// entry whose most recent State is Pending — i.e. the caller crashed
// before logging a matching Complete/Failed. Torn trailing lines (a
// partial record from a SIGKILL mid-Append) are silently dropped.
//
// Replay reads without holding the mutex because it's only meaningful
// at startup, before any Append has happened. Callers that Replay
// after writes have started will see a point-in-time snapshot and
// should not rely on consistency.
func Replay(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening journal %s for replay: %w", path, err)
	}
	defer f.Close()

	latest := make(map[string]Entry)
	scanner := bufio.NewScanner(f)
	// Allow generous line sizes — paths can be long.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Torn / corrupt line: skip, don't panic. The invariant is
			// that any *durable* record is well-formed; a line that
			// fails to parse is either the tail of a crash or
			// hand-edited garbage, and in both cases dropping it is
			// the right call.
			continue
		}
		if e.ID == "" {
			continue
		}
		latest[e.ID] = e
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("scanning journal %s: %w", path, err)
	}

	pending := make([]Entry, 0)
	for _, e := range latest {
		if e.State == StatePending {
			pending = append(pending, e)
		}
	}
	return pending, nil
}

// Rotate truncates the journal — used after a successful startup
// replay has reconciled every pending entry. The old file is renamed
// to <path>.archived-<unix> so a post-mortem can still read it; a
// future maintenance pass can prune archives older than some age.
func Rotate(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stating journal for rotate %s: %w", path, err)
	}
	archive := fmt.Sprintf("%s.archived-%d", path, time.Now().UnixNano())
	if err := os.Rename(path, archive); err != nil {
		return fmt.Errorf("rotating %s to %s: %w", path, archive, err)
	}
	return nil
}
