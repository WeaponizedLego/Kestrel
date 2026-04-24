// Package settings owns the small UI preferences (theme, sort key,
// sort order, grid cell size) that used to live in browser
// localStorage. The production binary binds a random loopback port
// every launch and localStorage is keyed per-origin, so browser-side
// persistence was effectively wiped on every restart. Storing them in
// a small JSON file under the user's config dir restores the "remember
// my preferences" behaviour the user expects.
//
// The store is a tiny JSON file (not library_meta.gob) so its schema
// can evolve independently and a corrupt settings file can never take
// the main library down with it — Open tolerates a missing or
// malformed file by logging and starting with defaults.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Settings is the full preference set persisted to disk. Field names
// match the wire format the frontend reads/writes verbatim. New fields
// are additive: an older settings.json with missing keys decodes to
// zero values, which Defaults then fills in.
type Settings struct {
	Theme     string `json:"theme"`
	SortKey   string `json:"sort_key"`
	SortOrder string `json:"sort_order"`
	CellSize  int    `json:"cell_size"`
}

// Defaults returns the baseline preference set. Mirrors the historical
// frontend defaults (ThemeController.vue: "dark"; PhotoGrid.vue:
// date/desc; selection.ts: 280 px) so users who never customise see no
// behaviour change.
func Defaults() Settings {
	return Settings{
		Theme:     "dark",
		SortKey:   "date",
		SortOrder: "desc",
		CellSize:  280,
	}
}

// Store is a concurrency-safe, file-backed Settings value. Mutations
// flush to disk immediately — the payload is tiny so batching isn't
// worth the failure-mode complexity.
type Store struct {
	path string

	mu       sync.RWMutex
	settings Settings
}

// Open reads path (if it exists) and returns a Store ready for
// concurrent use. A missing or malformed file is not an error — the
// store starts with Defaults applied and overwrites the file on the
// next mutation. We surface the read/parse failure via the returned
// error so the caller can log it.
func Open(path string) (*Store, error) {
	s := &Store{path: path, settings: Defaults()}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading settings from %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var loaded Settings
	if err := json.Unmarshal(data, &loaded); err != nil {
		return s, fmt.Errorf("parsing settings from %s: %w", path, err)
	}
	s.settings = withDefaults(loaded)
	return s, nil
}

// Get returns the current Settings. Returned by value so callers can't
// mutate the store's copy without going through Update.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Update merges every non-zero field of patch into the current
// settings, persists the result, and returns the resulting full
// Settings. The merge-non-zero rule lets the frontend send sparse
// payloads (e.g. {"theme": "dim"}) without having to read-modify-write
// the whole struct on every change.
func (s *Store) Update(patch Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := s.settings
	if patch.Theme != "" {
		merged.Theme = patch.Theme
	}
	if patch.SortKey != "" {
		merged.SortKey = patch.SortKey
	}
	if patch.SortOrder != "" {
		merged.SortOrder = patch.SortOrder
	}
	if patch.CellSize != 0 {
		merged.CellSize = patch.CellSize
	}
	if err := s.flushLocked(merged); err != nil {
		return s.settings, err
	}
	s.settings = merged
	return merged, nil
}

// flushLocked writes settings to disk. Caller must hold s.mu. Uses a
// temp-file + rename dance so a crash mid-write can't leave a
// truncated JSON file behind.
func (s *Store) flushLocked(settings Settings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating settings dir for %s: %w", s.path, err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding settings: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing settings to %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, s.path, err)
	}
	return nil
}

// withDefaults fills any zero-valued field of loaded with the matching
// default. Lets old files that pre-date a new field decode cleanly.
func withDefaults(loaded Settings) Settings {
	d := Defaults()
	if loaded.Theme == "" {
		loaded.Theme = d.Theme
	}
	if loaded.SortKey == "" {
		loaded.SortKey = d.SortKey
	}
	if loaded.SortOrder == "" {
		loaded.SortOrder = d.SortOrder
	}
	if loaded.CellSize == 0 {
		loaded.CellSize = d.CellSize
	}
	return loaded
}
