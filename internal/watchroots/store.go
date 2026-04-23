// Package watchroots owns the durable list of folders the background
// rescanner should keep in sync with disk. Every root the user points
// Kestrel at via POST /api/scan is recorded here; the scheduler reads
// this list to decide what to re-visit when the user is idle.
//
// The store is a tiny JSON file (not library_meta.gob) so its schema
// can evolve independently and a corrupt roots file can never take
// the main library down with it — Open tolerates a missing or
// malformed file by logging and starting empty.
package watchroots

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Root is one entry in the watched-roots list. LastScannedAt is zero
// until the scheduler completes its first rescan cycle for this root.
type Root struct {
	Path          string    `json:"path"`
	AddedAt       time.Time `json:"added_at"`
	LastScannedAt time.Time `json:"last_scanned_at,omitempty"`
}

// Store is a concurrency-safe, file-backed list of watched roots.
// All mutations flush to disk immediately — the list is small (a
// handful of entries, not thousands) so batching isn't worth the
// failure-mode complexity.
type Store struct {
	path string

	mu    sync.Mutex
	roots []Root
}

// Open reads path (if it exists) and returns a Store ready for
// concurrent use. A missing file is not an error — first-run binaries
// start with no watched roots. A malformed file is treated as empty
// and overwritten on the next mutation; we warn via the returned
// error so the caller can log it.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading watched roots from %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var roots []Root
	if err := json.Unmarshal(data, &roots); err != nil {
		return s, fmt.Errorf("parsing watched roots from %s: %w", path, err)
	}
	s.roots = roots
	return s, nil
}

// List returns a copy of the current roots, sorted by Path for
// deterministic iteration by the scheduler.
func (s *Store) List() []Root {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Root, len(s.roots))
	copy(out, s.roots)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Upsert adds root to the list, or no-ops if it's already present.
// Existing entries are left alone — we don't reset AddedAt or
// LastScannedAt just because the user re-ran a manual scan.
func (s *Store) Upsert(path string) error {
	if path == "" {
		return fmt.Errorf("upserting watched root: path is empty")
	}
	s.mu.Lock()
	for _, r := range s.roots {
		if r.Path == path {
			s.mu.Unlock()
			return nil
		}
	}
	s.roots = append(s.roots, Root{Path: path, AddedAt: time.Now()})
	err := s.flushLocked()
	s.mu.Unlock()
	return err
}

// Remove drops path from the list. Missing entries return nil —
// idempotent removal is a friendlier API for the "user clicked the
// trash icon twice" case.
func (s *Store) Remove(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.roots[:0]
	for _, r := range s.roots {
		if r.Path != path {
			out = append(out, r)
		}
	}
	s.roots = out
	return s.flushLocked()
}

// MarkScanned stamps the given root's LastScannedAt. Missing entries
// return nil so a late-arriving mark for a root the user just
// unwatched doesn't surface as an error.
func (s *Store) MarkScanned(path string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.roots {
		if s.roots[i].Path == path {
			s.roots[i].LastScannedAt = at
			return s.flushLocked()
		}
	}
	return nil
}

// flushLocked writes the current state to disk. Caller must hold mu.
// Uses a temp-file + rename dance so a crash mid-write can't leave a
// truncated JSON file behind.
func (s *Store) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating watched roots dir for %s: %w", s.path, err)
	}
	data, err := json.MarshalIndent(s.roots, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding watched roots: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing watched roots to %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, s.path, err)
	}
	return nil
}
