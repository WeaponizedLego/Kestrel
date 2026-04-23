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

// ErrDestinationExists is returned by RenamePhoto when the target path
// is already present in the library — re-keying would silently
// overwrite an unrelated photo, which we refuse at the boundary so the
// caller can surface the collision rather than paper over it.
var ErrDestinationExists = errors.New("destination path already in library")

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

// RenamePhoto re-keys the photo at oldPath to newPath under a single
// write lock. The Photo struct's Path and Name fields are updated in
// place so downstream consumers that keep pointers (e.g. the cluster
// manager snapshot) see the new identity on their next read.
//
// Returns ErrPhotoNotFound when oldPath is absent, or
// ErrDestinationExists when newPath is already in use. The library is
// not mutated on either error — callers can retry or fail cleanly.
func (l *Library) RenamePhoto(oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.photos[oldPath]
	if !ok {
		return fmt.Errorf("renaming %s: %w", oldPath, ErrPhotoNotFound)
	}
	if _, clash := l.photos[newPath]; clash {
		return fmt.Errorf("renaming %s to %s: %w", oldPath, newPath, ErrDestinationExists)
	}
	delete(l.photos, oldPath)
	p.Path = newPath
	p.Name = filepath.Base(newPath)
	l.photos[newPath] = p
	l.dirty = true
	return nil
}

// RemovePhoto drops the photo at path from the library and returns the
// removed pointer so callers (undo, restore flows) can reinsert it
// later without a second scan. Returns ErrPhotoNotFound if path is
// absent; the library is not mutated in that case.
//
// Files on disk are untouched — the caller is responsible for the
// filesystem half of the operation. Keeping the split explicit lets
// fileops.Manager order the journal, filesystem, and library steps
// safely.
func (l *Library) RemovePhoto(path string) (*Photo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.photos[path]
	if !ok {
		return nil, fmt.Errorf("removing %s: %w", path, ErrPhotoNotFound)
	}
	delete(l.photos, path)
	l.dirty = true
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

// PruneMissingUnder is PruneMissing restricted to photos whose path
// sits inside root. Used by the background rescanner so a transient
// stat error inside one watched root can't cascade into dropping
// photos from unrelated roots. Same lock discipline as PruneMissing:
// the filesystem check runs outside the lock, only the final delete
// acquires the writer.
func (l *Library) PruneMissingUnder(root string, exists func(path string) bool) []string {
	prefix := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)

	l.mu.RLock()
	paths := make([]string, 0, len(l.photos))
	for p := range l.photos {
		if strings.HasPrefix(p, prefix) {
			paths = append(paths, p)
		}
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

// RemovePhotosInFolder deletes every photo whose path lives under
// folder (transitively — sub-folder photos are included). Files on
// disk are not touched; only the in-memory index is pruned. Returns
// the list of removed paths so callers can publish counts and
// invalidate dependent caches (cluster cache, etc.) only when there
// was real work to do.
func (l *Library) RemovePhotosInFolder(folder string) []string {
	prefix := strings.TrimRight(folder, string(filepath.Separator)) + string(filepath.Separator)

	l.mu.Lock()
	defer l.mu.Unlock()

	removed := make([]string, 0)
	for path := range l.photos {
		if strings.HasPrefix(path, prefix) {
			removed = append(removed, path)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	for _, path := range removed {
		delete(l.photos, path)
	}
	l.dirty = true
	return removed
}

// FolderBucket groups untagged photos by their parent directory.
// Photos carry value copies so the Library mutex is released before the
// caller serializes them. Folders sort ascending by path; photos within
// a folder sort ascending by Name.
type FolderBucket struct {
	Folder string
	Photos []Photo
}

// UntaggedByFolder returns every photo with no user tags, grouped by
// filepath.Dir(Path). AutoTags are intentionally ignored — matching the
// cluster.Progress definition of "untagged". Use this to feed an
// onboarding / catch-up tagging view that mirrors the on-disk layout.
func (l *Library) UntaggedByFolder() []FolderBucket {
	l.mu.RLock()
	buckets := make(map[string][]Photo)
	for _, p := range l.photos {
		if len(p.Tags) > 0 {
			continue
		}
		folder := filepath.Dir(p.Path)
		buckets[folder] = append(buckets[folder], *p)
	}
	l.mu.RUnlock()

	folders := make([]string, 0, len(buckets))
	for f := range buckets {
		folders = append(folders, f)
	}
	sort.Strings(folders)

	out := make([]FolderBucket, 0, len(folders))
	for _, f := range folders {
		photos := buckets[f]
		sort.SliceStable(photos, func(i, j int) bool {
			if photos[i].Name != photos[j].Name {
				return photos[i].Name < photos[j].Name
			}
			return photos[i].Path < photos[j].Path
		})
		out = append(out, FolderBucket{Folder: f, Photos: photos})
	}
	return out
}

// TagStat describes one tag and how many photos currently carry it.
// Only user tags (Photo.Tags) are counted; AutoTags are derived on
// every scan and shouldn't be shown as editable vocabulary.
type TagStat struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// AllTags returns every distinct user tag and its photo count, sorted
// by name. AutoTags are intentionally excluded — they regenerate on
// every scan, so letting the user rename or delete them would be a
// footgun. Hidden tag is included so the user can see and manage it.
func (l *Library) AllTags() []TagStat {
	l.mu.RLock()
	defer l.mu.RUnlock()
	counts := make(map[string]int)
	for _, p := range l.photos {
		for _, t := range p.Tags {
			counts[t]++
		}
	}
	out := make([]TagStat, 0, len(counts))
	for name, count := range counts {
		out = append(out, TagStat{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RenameTag replaces every occurrence of old with new across the
// library. Both are normalized first. Returns the number of photos
// where new replaced old in place (renamed) and where the photo
// already carried new so old was just dropped (absorbed).
//
// Rename preserves the original tag's position in each photo's slice
// when the target isn't already present — useful so display order
// doesn't jump around unexpectedly after a spelling fix.
func (l *Library) RenameTag(old, next string) (renamed, absorbed int, err error) {
	oldNorm := normalizeOne(old)
	newNorm := normalizeOne(next)
	if oldNorm == "" || newNorm == "" {
		return 0, 0, fmt.Errorf("renaming tag: from and to are required")
	}
	if oldNorm == newNorm {
		return 0, 0, fmt.Errorf("renaming tag: from and to are identical (%q)", oldNorm)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range l.photos {
		r, a := replaceTag(p, oldNorm, newNorm)
		if r {
			renamed++
		}
		if a {
			absorbed++
		}
	}
	return renamed, absorbed, nil
}

// MergeTags folds source into target across the library. Semantically
// identical to RenameTag, but exposed separately so the API surface
// matches user intent — "merge" means the caller knows both tags
// currently exist. Returns per-photo counts the same way as RenameTag.
func (l *Library) MergeTags(source, target string) (renamed, absorbed int, err error) {
	srcNorm := normalizeOne(source)
	tgtNorm := normalizeOne(target)
	if srcNorm == "" || tgtNorm == "" {
		return 0, 0, fmt.Errorf("merging tags: source and target are required")
	}
	if srcNorm == tgtNorm {
		return 0, 0, fmt.Errorf("merging tags: source and target are identical (%q)", srcNorm)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range l.photos {
		r, a := replaceTag(p, srcNorm, tgtNorm)
		if r {
			renamed++
		}
		if a {
			absorbed++
		}
	}
	return renamed, absorbed, nil
}

// DeleteTag strips name from every photo that carries it. Returns the
// number of photos actually modified. Unknown tags are a no-op — the
// caller can decide whether to surface that to the user.
func (l *Library) DeleteTag(name string) int {
	norm := normalizeOne(name)
	if norm == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	affected := 0
	for _, p := range l.photos {
		idx := indexOfTag(p.Tags, norm)
		if idx < 0 {
			continue
		}
		p.Tags = append(p.Tags[:idx], p.Tags[idx+1:]...)
		affected++
	}
	return affected
}

// replaceTag swaps old for next on one photo. Returns (renamed,
// absorbed): renamed is true when old was present and next was not, so
// the slot was overwritten in place; absorbed is true when both were
// present, so old was removed and next stays. At most one of them is
// true on any given photo.
func replaceTag(p *Photo, old, next string) (renamed, absorbed bool) {
	oldIdx := indexOfTag(p.Tags, old)
	if oldIdx < 0 {
		return false, false
	}
	if indexOfTag(p.Tags, next) >= 0 {
		p.Tags = append(p.Tags[:oldIdx], p.Tags[oldIdx+1:]...)
		return false, true
	}
	p.Tags[oldIdx] = next
	return true, false
}

// indexOfTag returns the position of tag in tags, or -1 when absent.
func indexOfTag(tags []string, tag string) int {
	for i, t := range tags {
		if t == tag {
			return i
		}
	}
	return -1
}

// normalizeOne applies the single-tag form of NormalizeTags. Returns
// "" for inputs that would normalize to nothing (blank, whitespace).
func normalizeOne(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
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
