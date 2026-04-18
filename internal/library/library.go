package library

import (
	"errors"
	"fmt"
	"sort"
	"path/filepath"
	"strings"
	"sync"
)

// ErrPhotoNotFound is returned by GetPhoto when the requested path is
// absent from the library.
var ErrPhotoNotFound = errors.New("photo not found")

// HiddenTag is the magic tag that suppresses a photo from default
// listings. Filtering by it lives in the API layer (see listPhotos);
// the library treats it like any other tag. Kept here as the single
// source of truth so tests and handlers can't drift on the spelling.
const HiddenTag = "hidden"

// SortKey selects which pre-built index Sorted reads from. Adding a
// fourth key means adding a slice, a rebuild branch, and the handler
// validation — intentionally noisy so we notice the cost.
type SortKey uint8

const (
	SortName SortKey = iota
	SortDate
	SortSize
)

// Library is the in-memory source of truth for the active photo set.
// All access is guarded by an RWMutex; callers must never touch the
// underlying map directly.
//
// Three pre-sorted slices are kept alongside the map so the frontend
// can request "photos by date/name/size" without an O(N log N) sort
// on every request. To stay cheap under an additive scanner (which
// calls AddPhoto per file), mutations only mark the indices dirty —
// the actual re-sort happens lazily on the first Sorted call after
// the dust settles. That matches docs/system-design.md → "Pre-Sorted
// Indices" which prescribes rebuilding "once in the background", and
// keeps a million-photo scan from triggering a million re-sorts.
type Library struct {
	mu     sync.RWMutex
	photos map[string]*Photo

	byName []*Photo
	byDate []*Photo
	bySize []*Photo
	dirty  bool
}

// New returns an empty Library ready for concurrent use.
func New() *Library {
	return &Library{
		photos: make(map[string]*Photo),
	}
}

// AddPhoto stores (or overwrites) a photo keyed by its absolute path.
// The sort indices are only marked dirty — the next Sorted call
// rebuilds them. Scanners that stream in thousands of photos don't
// pay for a sort per file.
func (l *Library) AddPhoto(p *Photo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.photos[p.Path] = p
	l.dirty = true
}

// GetPhoto looks up a photo by absolute path. It returns
// ErrPhotoNotFound (wrapped with context) when the path is unknown.
func (l *Library) GetPhoto(path string) (*Photo, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p, ok := l.photos[path]
	if !ok {
		return nil, fmt.Errorf("looking up %s: %w", path, ErrPhotoNotFound)
	}
	return p, nil
}

// PruneMissing removes every photo for which exists(path) returns
// false. The predicate is called without any library locks held, so a
// slow os.Stat doesn't block readers; only the final remove step
// acquires the write lock.
//
// Returns the list of removed paths so callers can log / broadcast
// specifics if they want.
func (l *Library) PruneMissing(exists func(path string) bool) []string {
	// Snapshot the paths under an RLock so we don't hold any lock
	// while hitting the filesystem. Stat'ing 1M files under a write
	// lock would freeze every reader for minutes.
	l.mu.RLock()
	paths := make([]string, 0, len(l.photos))
	for p := range l.photos {
		paths = append(paths, p)
	}
	l.mu.RUnlock()

	missing := make([]string, 0)
	for _, p := range paths {
		if !exists(p) {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range missing {
		delete(l.photos, p)
	}
	l.dirty = true
	return missing
}

// SetTags replaces the tag set on the photo at path. Input is
// normalized (lowercased, trimmed, deduplicated) before storage, so
// callers can pass whatever the user typed. Returns ErrPhotoNotFound
// (wrapped) when path is unknown.
func (l *Library) SetTags(path string, tags []string) error {
	normalized := NormalizeTags(tags)
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.photos[path]
	if !ok {
		return fmt.Errorf("setting tags on %s: %w", path, ErrPhotoNotFound)
	}
	p.Tags = normalized
	return nil
}

// AddTagsToPaths merges tags into every photo in paths. Unknown paths
// are silently skipped — the handler layer decides whether that's
// worth reporting to the user. Returns the number of photos that
// actually gained a new tag (existing-tag no-ops don't count).
func (l *Library) AddTagsToPaths(paths []string, tags []string) int {
	additions := NormalizeTags(tags)
	if len(additions) == 0 || len(paths) == 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	updated := 0
	for _, path := range paths {
		p, ok := l.photos[path]
		if !ok {
			continue
		}
		merged, changed := mergeTags(p.Tags, additions)
		if changed {
			p.Tags = merged
			updated++
		}
	}
	return updated
}

// AddTagsToFolder merges tags into every photo whose path lives under
// folder (transitively — sub-folder photos are included). Existing
// tags are preserved; only new ones are appended, so right-clicking a
// folder to "tag all" feels additive rather than destructive.
// Returns the number of photos actually modified.
func (l *Library) AddTagsToFolder(folder string, tags []string) int {
	additions := NormalizeTags(tags)
	if len(additions) == 0 {
		return 0
	}
	prefix := strings.TrimRight(folder, string(filepath.Separator)) + string(filepath.Separator)

	l.mu.Lock()
	defer l.mu.Unlock()

	updated := 0
	for _, p := range l.photos {
		if !strings.HasPrefix(p.Path, prefix) {
			continue
		}
		merged, changed := mergeTags(p.Tags, additions)
		if changed {
			p.Tags = merged
			updated++
		}
	}
	return updated
}

// mergeTags returns the union of existing and additions, preserving
// existing order and appending only tags not already present. The
// second return reports whether any new tag was added.
func mergeTags(existing, additions []string) ([]string, bool) {
	if len(additions) == 0 {
		return existing, false
	}
	have := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		have[t] = struct{}{}
	}
	out := existing
	changed := false
	for _, t := range additions {
		if _, dup := have[t]; dup {
			continue
		}
		have[t] = struct{}{}
		out = append(out, t)
		changed = true
	}
	return out, changed
}

// NormalizeTags returns a canonical form of raw: lowercased, trimmed,
// with empties and duplicates removed, order-preserving. Exported so
// handlers and tests can share the normalization contract.
func NormalizeTags(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		clean := strings.ToLower(strings.TrimSpace(t))
		if clean == "" {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

// AllPhotos returns a snapshot copy of every photo currently stored
// in an unspecified order. Prefer Sorted when the caller cares about
// ordering — it serves from a pre-built index and avoids a per-call
// sort.
func (l *Library) AllPhotos() []*Photo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*Photo, 0, len(l.photos))
	for _, p := range l.photos {
		out = append(out, p)
	}
	return out
}

// Sorted returns a snapshot slice ordered by key. When desc is true,
// the slice is reversed before returning. The returned slice is a
// copy — callers may mutate it freely.
//
// If mutations have accumulated since the last rebuild, the indices
// are regenerated under the write lock first. Concurrent readers see
// the stable post-rebuild state; only the first post-mutation reader
// pays the sort cost.
func (l *Library) Sorted(key SortKey, desc bool) []*Photo {
	// Fast path: indices are clean, take a shared lock and copy.
	l.mu.RLock()
	if !l.dirty {
		out := sliceSnapshotLocked(l.indexForLocked(key), desc)
		l.mu.RUnlock()
		return out
	}
	l.mu.RUnlock()

	// Slow path: promote to exclusive, re-check dirty (another
	// goroutine may have rebuilt while we waited), rebuild, copy.
	l.mu.Lock()
	if l.dirty {
		l.rebuildIndicesLocked()
		l.dirty = false
	}
	out := sliceSnapshotLocked(l.indexForLocked(key), desc)
	l.mu.Unlock()
	return out
}

// sliceSnapshotLocked returns a defensive copy of src, reversed when
// desc is true. Caller must hold l.mu.
func sliceSnapshotLocked(src []*Photo, desc bool) []*Photo {
	out := make([]*Photo, len(src))
	if desc {
		for i, p := range src {
			out[len(src)-1-i] = p
		}
	} else {
		copy(out, src)
	}
	return out
}

// ReplaceAll atomically swaps the library contents for a fresh set.
// Used at startup when loading library_meta.gob. The sort indices
// are marked dirty and will be rebuilt on the next Sorted call.
//
// Note: scans are additive — they use AddPhoto per file, not
// ReplaceAll — so multi-root libraries accumulate across runs.
func (l *Library) ReplaceAll(photos []*Photo) {
	next := make(map[string]*Photo, len(photos))
	for _, p := range photos {
		next[p.Path] = p
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.photos = next
	l.dirty = true
}

// Len reports how many photos are currently in the library.
func (l *Library) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.photos)
}

// indexForLocked returns the pre-built slice matching key. Caller
// must hold l.mu (R or W).
func (l *Library) indexForLocked(key SortKey) []*Photo {
	switch key {
	case SortDate:
		return l.byDate
	case SortSize:
		return l.bySize
	default:
		return l.byName
	}
}

// rebuildIndicesLocked regenerates byName / byDate / bySize from the
// map. Caller must hold l.mu for writing. Sorts are stable and break
// ties on Path so the output is deterministic across runs — handy
// for tests and for pagination stability in the UI.
func (l *Library) rebuildIndicesLocked() {
	base := make([]*Photo, 0, len(l.photos))
	for _, p := range l.photos {
		base = append(base, p)
	}

	l.byName = cloneSlice(base)
	sort.SliceStable(l.byName, func(i, j int) bool {
		a, b := l.byName[i], l.byName[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})

	l.byDate = cloneSlice(base)
	sort.SliceStable(l.byDate, func(i, j int) bool {
		a, b := l.byDate[i], l.byDate[j]
		// Zero TakenAt (no EXIF) sinks to the end of ascending order
		// so "oldest first" doesn't mean "everything with missing
		// metadata first".
		az, bz := a.TakenAt.IsZero(), b.TakenAt.IsZero()
		if az != bz {
			return !az
		}
		if !a.TakenAt.Equal(b.TakenAt) {
			return a.TakenAt.Before(b.TakenAt)
		}
		return a.Path < b.Path
	})

	l.bySize = cloneSlice(base)
	sort.SliceStable(l.bySize, func(i, j int) bool {
		a, b := l.bySize[i], l.bySize[j]
		if a.SizeBytes != b.SizeBytes {
			return a.SizeBytes < b.SizeBytes
		}
		return a.Path < b.Path
	})
}

// cloneSlice returns a shallow copy of s. Used so each index can be
// re-sorted independently without disturbing the others.
func cloneSlice(s []*Photo) []*Photo {
	out := make([]*Photo, len(s))
	copy(out, s)
	return out
}
