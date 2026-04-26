package library

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// AddAudio stores (or overwrites) an audio entry keyed by its
// absolute path. Mirrors AddPhoto: writes only mark indices dirty,
// the next SortedAudio call rebuilds.
func (l *Library) AddAudio(a *Audio) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.audios[a.Path] = a
	l.dirty = true
}

// GetAudio looks up an audio entry by absolute path. Returns
// ErrPhotoNotFound (wrapped) when path is unknown — the error is
// shared with the photo lookup because callers conceptually search
// for "the media at this path", not specifically for audio.
func (l *Library) GetAudio(path string) (*Audio, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.audios[path]
	if !ok {
		return nil, fmt.Errorf("looking up %s: %w", path, ErrPhotoNotFound)
	}
	return a, nil
}

// RemoveAudio drops the audio at path and returns the removed
// pointer for undo / restore flows. Returns ErrPhotoNotFound if the
// path is absent; the library is not mutated in that case.
func (l *Library) RemoveAudio(path string) (*Audio, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.audios[path]
	if !ok {
		return nil, fmt.Errorf("removing %s: %w", path, ErrPhotoNotFound)
	}
	delete(l.audios, path)
	l.dirty = true
	return a, nil
}

// RenameAudio re-keys the audio at oldPath to newPath under a single
// write lock. Same semantics as RenamePhoto.
func (l *Library) RenameAudio(oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.audios[oldPath]
	if !ok {
		return fmt.Errorf("renaming %s: %w", oldPath, ErrPhotoNotFound)
	}
	if _, clash := l.audios[newPath]; clash {
		return fmt.Errorf("renaming %s to %s: %w", oldPath, newPath, ErrDestinationExists)
	}
	delete(l.audios, oldPath)
	a.Path = newPath
	a.Name = filepath.Base(newPath)
	l.audios[newPath] = a
	l.dirty = true
	return nil
}

// PruneMissingAudio mirrors PruneMissing for the audio map.
func (l *Library) PruneMissingAudio(exists func(path string) bool) []string {
	l.mu.RLock()
	paths := make([]string, 0, len(l.audios))
	for p := range l.audios {
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
		delete(l.audios, p)
	}
	l.dirty = true
	return missing
}

// PruneMissingAudioUnder mirrors PruneMissingUnder for the audio map.
func (l *Library) PruneMissingAudioUnder(root string, exists func(path string) bool) []string {
	prefix := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)

	l.mu.RLock()
	paths := make([]string, 0, len(l.audios))
	for p := range l.audios {
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
		delete(l.audios, p)
	}
	l.dirty = true
	return missing
}

// LenAudio reports how many audio entries are currently in the
// library.
func (l *Library) LenAudio() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.audios)
}

// AllAudios returns a snapshot copy of every audio entry, in
// unspecified order. Prefer SortedAudio when the caller cares about
// ordering.
func (l *Library) AllAudios() []*Audio {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*Audio, 0, len(l.audios))
	for _, a := range l.audios {
		out = append(out, a)
	}
	return out
}

// SortedAudio returns a snapshot slice ordered by key. Mirrors
// Sorted; rebuilds happen lazily on first access after a mutation
// (shared dirty flag with the photo indices).
func (l *Library) SortedAudio(key SortKey, desc bool) []*Audio {
	l.mu.RLock()
	if !l.dirty {
		out := audioSnapshotLocked(l.audioIndexForLocked(key), desc)
		l.mu.RUnlock()
		return out
	}
	l.mu.RUnlock()

	l.mu.Lock()
	if l.dirty {
		l.rebuildIndicesLocked()
		l.dirty = false
	}
	out := audioSnapshotLocked(l.audioIndexForLocked(key), desc)
	l.mu.Unlock()
	return out
}

// ReplaceAllAudio atomically swaps the audio map for a fresh set.
// Used at startup when loading library_meta.gob v5+.
func (l *Library) ReplaceAllAudio(audios []*Audio) {
	next := make(map[string]*Audio, len(audios))
	for _, a := range audios {
		next[a.Path] = a
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.audios = next
	l.dirty = true
}

// audioIndexForLocked returns the pre-built audio slice matching key.
func (l *Library) audioIndexForLocked(key SortKey) []*Audio {
	switch key {
	case SortDate:
		return l.audioByDate
	case SortSize:
		return l.audioBySize
	default:
		return l.audioByName
	}
}

// audioSnapshotLocked is the audio analogue of sliceSnapshotLocked.
func audioSnapshotLocked(src []*Audio, desc bool) []*Audio {
	out := make([]*Audio, len(src))
	if desc {
		for i, a := range src {
			out[len(src)-1-i] = a
		}
	} else {
		copy(out, src)
	}
	return out
}

// rebuildAudioIndicesLocked regenerates audioByName / audioByDate /
// audioBySize from the audio map. Caller must hold l.mu for writing.
// Audio has no EXIF capture time; "by date" sorts on ModTime so the
// UI's date sort is meaningful for both kinds of media.
func (l *Library) rebuildAudioIndicesLocked() {
	base := make([]*Audio, 0, len(l.audios))
	for _, a := range l.audios {
		base = append(base, a)
	}

	l.audioByName = cloneAudioSlice(base)
	sort.SliceStable(l.audioByName, func(i, j int) bool {
		a, b := l.audioByName[i], l.audioByName[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})

	l.audioByDate = cloneAudioSlice(base)
	sort.SliceStable(l.audioByDate, func(i, j int) bool {
		a, b := l.audioByDate[i], l.audioByDate[j]
		if !a.ModTime.Equal(b.ModTime) {
			return a.ModTime.Before(b.ModTime)
		}
		return a.Path < b.Path
	})

	l.audioBySize = cloneAudioSlice(base)
	sort.SliceStable(l.audioBySize, func(i, j int) bool {
		a, b := l.audioBySize[i], l.audioBySize[j]
		if a.SizeBytes != b.SizeBytes {
			return a.SizeBytes < b.SizeBytes
		}
		return a.Path < b.Path
	})
}

// cloneAudioSlice returns a shallow copy of s.
func cloneAudioSlice(s []*Audio) []*Audio {
	out := make([]*Audio, len(s))
	copy(out, s)
	return out
}

// AudioAsPhoto projects an Audio into a Photo-shaped value with the
// fields the wire format and the untagged-by-folder view care about.
// Width/Height/EXIF stay zero; AutoTags is the gate the frontend uses
// to render the audio badge (kind:audio) and pick the audio player.
//
// Exported so the API layer can project audios into the merged
// /api/photos response without copying the field list. This is a
// one-way projection — callers get a value, not a pointer to the
// stored entry, so mutations on the result do not affect the library.
func AudioAsPhoto(a *Audio) Photo {
	return Photo{
		Path:      a.Path,
		Hash:      a.Hash,
		Name:      a.Name,
		SizeBytes: a.SizeBytes,
		ModTime:   a.ModTime,
		Tags:      append([]string(nil), a.Tags...),
		AutoTags:  append([]string(nil), a.AutoTags...),
	}
}
