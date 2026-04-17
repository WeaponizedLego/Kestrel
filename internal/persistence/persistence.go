// Package persistence reads and writes the metadata half of Kestrel's
// on-disk state — library_meta.gob. The thumbnail pack file is owned
// by internal/thumbnail in a later phase.
//
// The file format is a tiny header followed by a gob-encoded
// []*library.Photo. The header carries a magic string (so we fail loud
// on accidentally pointing at a foreign file) and a schema version (so
// future Photo struct changes can migrate forward instead of silently
// returning garbage). Both header and payload are gob streams written
// to the same file in sequence.
package persistence

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/WeaponizedLego/kestrel/internal/library"
)

// magic identifies a Kestrel metadata file. Stored as a fixed string
// in the header so a mistyped path or a clobbered file fails fast
// instead of decoding gibberish into Photo fields.
const magic = "KSTL"

// CurrentVersion is the schema version this build writes. Bump it when
// the Photo struct gains/loses fields in a way old readers can't
// handle, and add a branch in Load to migrate the older payload.
const CurrentVersion uint32 = 1

// header is the first gob-encoded value in every metadata file. Kept
// deliberately small so a future migration can read it without
// touching the payload.
type header struct {
	Magic   string
	Version uint32
}

// ErrUnknownVersion is returned by Load when the file's version field
// is newer than CurrentVersion (or older than any version this binary
// knows how to migrate). Callers can decide whether to abort or fall
// back to a fresh library.
var ErrUnknownVersion = errors.New("unknown metadata file version")

// ErrBadMagic is returned by Load when the file does not start with
// the expected magic string — almost certainly the wrong file.
var ErrBadMagic = errors.New("not a kestrel metadata file")

// Save writes photos to path atomically: it encodes to a sibling
// "<path>.tmp" file first, fsyncs, then renames over the destination.
// That way a crash mid-write leaves the previous good file untouched.
func Save(path string, photos []*library.Photo) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating metadata dir for %s: %w", path, err)
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating temp metadata file %s: %w", tmp, err)
	}
	// Best-effort cleanup if anything below fails before the rename.
	defer os.Remove(tmp)

	enc := gob.NewEncoder(f)
	if err := enc.Encode(header{Magic: magic, Version: CurrentVersion}); err != nil {
		f.Close()
		return fmt.Errorf("encoding header to %s: %w", tmp, err)
	}
	if err := enc.Encode(photos); err != nil {
		f.Close()
		return fmt.Errorf("encoding photos to %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("flushing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, path, err)
	}
	return nil
}

// Load reads photos from path. A missing file is not an error — it
// returns (nil, nil) so a first-run binary can carry on with an empty
// library. A present-but-corrupt file or a version mismatch returns a
// wrapped error; the caller decides whether to keep going or abort.
func Load(path string) ([]*library.Photo, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	dec := gob.NewDecoder(f)

	var h header
	if err := dec.Decode(&h); err != nil {
		return nil, fmt.Errorf("decoding header from %s: %w", path, err)
	}
	if h.Magic != magic {
		return nil, fmt.Errorf("checking magic in %s: %w", path, ErrBadMagic)
	}
	if h.Version != CurrentVersion {
		return nil, fmt.Errorf("checking version in %s (got %d, want %d): %w",
			path, h.Version, CurrentVersion, ErrUnknownVersion)
	}

	var photos []*library.Photo
	if err := dec.Decode(&photos); err != nil {
		return nil, fmt.Errorf("decoding photos from %s: %w", path, err)
	}
	return photos, nil
}
