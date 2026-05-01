// Package watchroots owns the durable list of folders the background
// rescanner should keep in sync with disk. Every root the user points
// Kestrel at via POST /api/scan is recorded here; the scheduler reads
// this list to decide what to re-visit when the user is idle.
//
// The store also carries an "ignored" list — paths the user has
// removed and that must not be silently re-added by the periodic
// re-decomposition or the runtime watcher. An ignore entry implicitly
// covers all descendants.
//
// The store is a tiny JSON file (not library_meta.gob) so its schema
// can evolve independently and a corrupt roots file can never take
// the main library down with it — Open tolerates a missing or
// malformed file by logging and starting empty.
package watchroots

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Root is one entry in the watched-roots list. LastScannedAt is zero
// until the scheduler completes its first rescan cycle for this root.
// Origin is the user-added top-level folder this root descends from;
// for legacy entries Origin equals Path (treat-as-self-rooted).
type Root struct {
	Path          string    `json:"path"`
	Origin        string    `json:"origin,omitempty"`
	AddedAt       time.Time `json:"added_at"`
	LastScannedAt time.Time `json:"last_scanned_at,omitempty"`
}

// fileFormat is the on-disk JSON shape since the schema gained an
// ignored list. Older files are a bare JSON array of Root and are
// detected by the leading byte on load.
type fileFormat struct {
	Roots   []Root   `json:"roots"`
	Ignored []string `json:"ignored,omitempty"`
}

// Subscriber receives diffs whenever the set of roots changes.
// MarkScanned does not fire subscribers — only set membership matters
// to consumers like the watcher.
type Subscriber func(added, removed []Root)

// Store is a concurrency-safe, file-backed list of watched roots.
type Store struct {
	path string

	mu          sync.Mutex
	roots       []Root
	ignored     []string
	subs        map[int]Subscriber
	nextSubID   int
}

// Open reads path (if it exists) and returns a Store ready for
// concurrent use. A missing file is not an error — first-run binaries
// start with no watched roots. A malformed file is treated as empty
// and overwritten on the next mutation; we warn via the returned
// error so the caller can log it.
func Open(path string) (*Store, error) {
	s := &Store{path: path, subs: map[int]Subscriber{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading watched roots from %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// Legacy schema: bare array of Root.
		var roots []Root
		if err := json.Unmarshal(data, &roots); err != nil {
			return s, fmt.Errorf("parsing watched roots from %s: %w", path, err)
		}
		s.roots = roots
	} else {
		var f fileFormat
		if err := json.Unmarshal(data, &f); err != nil {
			return s, fmt.Errorf("parsing watched roots from %s: %w", path, err)
		}
		s.roots = f.Roots
		s.ignored = f.Ignored
	}
	for i := range s.roots {
		if s.roots[i].Origin == "" {
			s.roots[i].Origin = s.roots[i].Path
		}
	}
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

// Ignored returns a copy of the current ignore list.
func (s *Store) Ignored() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.ignored))
	copy(out, s.ignored)
	return out
}

// IsIgnored reports whether path equals an ignored entry or is a
// descendant of one. The check is O(N_ignored) per call — fine for the
// small lists humans curate.
func (s *Store) IsIgnored(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return isIgnoredLocked(s.ignored, path)
}

func isIgnoredLocked(ignored []string, path string) bool {
	for _, ig := range ignored {
		if path == ig {
			return true
		}
		if strings.HasPrefix(path, ig+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Subscribe registers fn to be called after every change to the set of
// roots (additions, removals — not LastScannedAt updates). The returned
// function unsubscribes. Subscribers are invoked outside the store's
// lock, so they may safely call back into the store.
func (s *Store) Subscribe(fn Subscriber) func() {
	s.mu.Lock()
	id := s.nextSubID
	s.nextSubID++
	s.subs[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

// Upsert adds path as a self-rooted entry (Origin == Path). Used by
// the watcher when a new top-level folder is added or by callers who
// don't have a parent origin to reference. Ignored paths are silently
// skipped so a manually-removed folder isn't re-added behind the
// user's back.
func (s *Store) Upsert(path string) error {
	return s.UpsertTree(path, []string{path})
}

// UpsertTree adds every entry in paths under origin in one locked
// write. Existing entries are left alone (idempotent). Ignored paths
// are silently skipped. Subscribers fire once with the net additions.
func (s *Store) UpsertTree(origin string, paths []string) error {
	if origin == "" {
		return fmt.Errorf("upserting watched root tree: origin is empty")
	}
	s.mu.Lock()
	if isIgnoredLocked(s.ignored, origin) {
		s.mu.Unlock()
		return nil
	}
	existing := make(map[string]struct{}, len(s.roots))
	for _, r := range s.roots {
		existing[r.Path] = struct{}{}
	}
	now := time.Now()
	var added []Root
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := existing[p]; ok {
			continue
		}
		if isIgnoredLocked(s.ignored, p) {
			continue
		}
		r := Root{Path: p, Origin: origin, AddedAt: now}
		s.roots = append(s.roots, r)
		existing[p] = struct{}{}
		added = append(added, r)
	}
	if len(added) == 0 {
		s.mu.Unlock()
		return nil
	}
	if err := s.flushLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	subs := s.snapshotSubsLocked()
	s.mu.Unlock()
	notify(subs, added, nil)
	return nil
}

// Remove drops path from the list and adds it to the ignore list so
// that it isn't auto-re-added by future decomposition or watcher
// events. Idempotent — removing a missing path still tombstones it.
func (s *Store) Remove(path string) error {
	if path == "" {
		return fmt.Errorf("removing watched root: path is empty")
	}
	s.mu.Lock()
	var removed []Root
	out := s.roots[:0]
	for _, r := range s.roots {
		if r.Path == path {
			removed = append(removed, r)
			continue
		}
		out = append(out, r)
	}
	s.roots = out
	if !containsString(s.ignored, path) {
		s.ignored = append(s.ignored, path)
	}
	if err := s.flushLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	subs := s.snapshotSubsLocked()
	s.mu.Unlock()
	if len(removed) > 0 {
		notify(subs, nil, removed)
	}
	return nil
}

// RemoveByOrigin drops every entry whose Origin equals origin and
// tombstones origin so re-adding a parent doesn't pull the descendant
// tree back in.
func (s *Store) RemoveByOrigin(origin string) error {
	if origin == "" {
		return fmt.Errorf("removing watched roots by origin: origin is empty")
	}
	s.mu.Lock()
	var removed []Root
	out := s.roots[:0]
	for _, r := range s.roots {
		if r.Origin == origin {
			removed = append(removed, r)
			continue
		}
		out = append(out, r)
	}
	s.roots = out
	if !containsString(s.ignored, origin) {
		s.ignored = append(s.ignored, origin)
	}
	if err := s.flushLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	subs := s.snapshotSubsLocked()
	s.mu.Unlock()
	if len(removed) > 0 {
		notify(subs, nil, removed)
	}
	return nil
}

// MarkScanned stamps the given root's LastScannedAt. Missing entries
// return nil so a late-arriving mark for a root the user just
// unwatched doesn't surface as an error. Does not fire subscribers.
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

func (s *Store) snapshotSubsLocked() []Subscriber {
	if len(s.subs) == 0 {
		return nil
	}
	out := make([]Subscriber, 0, len(s.subs))
	for _, fn := range s.subs {
		out = append(out, fn)
	}
	return out
}

func notify(subs []Subscriber, added, removed []Root) {
	for _, fn := range subs {
		fn(added, removed)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// flushLocked writes the current state to disk. Caller must hold mu.
// Uses a temp-file + rename dance so a crash mid-write can't leave a
// truncated JSON file behind.
func (s *Store) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating watched roots dir for %s: %w", s.path, err)
	}
	payload := fileFormat{Roots: s.roots, Ignored: s.ignored}
	data, err := json.MarshalIndent(payload, "", "  ")
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
