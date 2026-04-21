// Package fileops is the one place in Kestrel that destroys user
// files. Every move and delete flows through a Manager that journals
// the intent before it touches the filesystem, updates the in-memory
// library only after the filesystem succeeds, persists the metadata,
// then clears the journal entry. A crash anywhere in that sequence is
// recoverable — startup replays any *-pending record and either
// finishes or rolls back the op.
//
// The package is deliberately verbose about invariants (see
// invariants_test.go) because silently breaking any of them could
// lose a user's photos. When in doubt: add a test, don't remove one.
package fileops

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/WeaponizedLego/kestrel/internal/fileops/journal"
	"github.com/WeaponizedLego/kestrel/internal/fileops/trash"
	"github.com/WeaponizedLego/kestrel/internal/library"
)

// ErrScanInProgress is returned by file ops when the scanner is
// active. The UI surfaces a "wait for scan to finish" message; callers
// should not retry automatically.
var ErrScanInProgress = errors.New("file operations blocked while a scan is running")

// ErrNothingToUndo is returned when Undo is called with an empty undo
// stack.
var ErrNothingToUndo = errors.New("nothing to undo")

// maxUndoDepth caps the in-memory undo stack. Chosen over "unlimited"
// so a long session can't accumulate thousands of deletion records;
// once the cap is hit, the oldest op drops off (it's still in the
// trash bin and journal, just not one-click undoable).
const maxUndoDepth = 32

// Publisher is the event-hub slice the manager needs. Matches the
// interface in internal/scanner and internal/thumbnail so the Hub
// satisfies all of them at once. A nil Publisher is allowed — the
// Manager treats it as a test-mode no-op.
type Publisher interface {
	Publish(kind string, payload any)
}

// Persist is the callback the Manager invokes after every library
// mutation so library_meta.gob stays in lock-step with the in-memory
// truth. Injected (rather than importing the persistence package
// directly) so tests can swap a failing saver in to exercise the
// rollback path.
type Persist func() error

// ScanActive reports whether the scanner is currently running. The
// Manager refuses file ops when this returns true, on the theory that
// a user-visible "not now" error is much better than racing with a
// mutation we can't easily reason about.
type ScanActive func() bool

// Config wires a Manager.
type Config struct {
	Library    *library.Library
	Journal    *journal.Journal
	Trash      *trash.Bin
	Persist    Persist
	ScanActive ScanActive
	Publisher  Publisher
	FS         FileSystem // zero value → DefaultFS
}

// Manager is the single entrypoint for every file operation. Safe for
// concurrent calls from HTTP handlers; operations serialize through
// an internal mutex so the journal and library never see interleaved
// mutations from two ops.
type Manager struct {
	cfg Config
	fs  FileSystem

	mu        sync.Mutex // serializes file ops
	undoStack []Operation
}

// New constructs a Manager. Required fields are Library, Journal, and
// Trash; Persist/ScanActive/Publisher/FS can be zero-valued and will
// be replaced with sensible defaults.
func New(cfg Config) *Manager {
	fs := cfg.FS
	if fs == nil {
		fs = DefaultFS
	}
	if cfg.Persist == nil {
		cfg.Persist = func() error { return nil }
	}
	if cfg.ScanActive == nil {
		cfg.ScanActive = func() bool { return false }
	}
	return &Manager{cfg: cfg, fs: fs}
}

// Result is the per-file outcome of a batch operation. Handlers
// include the full slice in their response so the UI can render "9
// of 10 moved — foo.jpg failed: destination exists".
type Result struct {
	Path    string `json:"path"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`

	// NewPath is populated on successful moves so the UI can
	// optimistically re-key its own selection state.
	NewPath string `json:"new_path,omitempty"`

	// TrashID is populated on successful trash deletes so the UI's
	// undo toast can target the specific bin entry.
	TrashID string `json:"trash_id,omitempty"`
}

// Operation describes a single user-visible action for the undo
// stack. A batch move of 10 photos is one Operation containing 10
// per-file records; Undo reverses them as a unit.
type Operation struct {
	ID    string
	Kind  journal.Kind
	Items []OperationItem
}

// OperationItem is one file's role in a reversible Operation. The
// fields union across Kinds — Kind on the containing Operation
// determines which fields are meaningful.
type OperationItem struct {
	// Move
	OldPath string
	NewPath string
	// Delete-to-trash
	OriginalPath string
	TrashID      string
	PhotoHash    string
}

// UndoSummary is returned by Undo for UI feedback. Remaining lets the
// toast show "2 more undoable ops."
type UndoSummary struct {
	Undone    Operation `json:"undone"`
	Results   []Result  `json:"results"`
	Remaining int       `json:"remaining"`
}

// pushUndo appends op to the stack, dropping the oldest when the cap
// is reached. Call under m.mu.
func (m *Manager) pushUndo(op Operation) {
	m.undoStack = append(m.undoStack, op)
	if len(m.undoStack) > maxUndoDepth {
		// Shift left by one, releasing the reference so the garbage
		// collector can reclaim the dropped op.
		copy(m.undoStack, m.undoStack[1:])
		m.undoStack = m.undoStack[:len(m.undoStack)-1]
	}
}

// popUndo returns the most recent op and removes it from the stack.
// Returns (Operation{}, false) when the stack is empty.
func (m *Manager) popUndo() (Operation, bool) {
	if len(m.undoStack) == 0 {
		return Operation{}, false
	}
	last := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	return last, true
}

// UndoDepth is how many operations are currently reversible. Exposed
// so the toolbar can enable/disable the Undo button.
func (m *Manager) UndoDepth() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.undoStack)
}

// newOpID returns a short unique identifier for journal/undo records.
// Not cryptographic — just unique enough to correlate log entries.
func newOpID() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// publish is a nil-safe shortcut around cfg.Publisher.
func (m *Manager) publish(kind string, payload any) {
	if m.cfg.Publisher != nil {
		m.cfg.Publisher.Publish(kind, payload)
	}
}

// ensureScanIdle returns ErrScanInProgress when the scanner is
// active. Called at the top of every mutating entry point.
func (m *Manager) ensureScanIdle() error {
	if m.cfg.ScanActive() {
		return ErrScanInProgress
	}
	return nil
}

// destinationFor composes the target absolute path for a move into
// dir. If dir doesn't exist we create it. If a collision would
// happen we return an error so the caller can surface it per-file
// rather than silently overwrite.
func (m *Manager) destinationFor(src, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("destination dir must be absolute, got %q", dir)
	}
	if err := m.fs.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("preparing destination %s: %w", dir, err)
	}
	dst := filepath.Join(dir, filepath.Base(src))
	if _, err := m.fs.Stat(dst); err == nil {
		return "", fmt.Errorf("destination already exists: %s", dst)
	}
	return dst, nil
}
