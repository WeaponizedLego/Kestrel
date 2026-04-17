// Package metadata extracts the per-image attributes Kestrel cares
// about: pixel dimensions and a small EXIF subset (capture time and
// camera make).
//
// The implementation is best-effort. Files that have no EXIF block, or
// whose EXIF block is malformed, still yield a usable Metadata with
// zero TakenAt / empty CameraMake — the only fatal errors come from
// being unable to open or decode the image at all.
package metadata

import (
	"fmt"
	"image"
	"os"
	"time"

	// Register decoders so image.DecodeConfig can read every format the
	// scanner accepts. The blank imports are required for side-effect
	// registration with the image package.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/rwcarlsen/goexif/exif"
)

// Metadata is the subset of image attributes the in-memory library
// stores per Photo. Zero values are valid and indicate "unknown".
type Metadata struct {
	Width      int
	Height     int
	TakenAt    time.Time
	CameraMake string
}

// Extract opens path and returns its dimensions plus a best-effort EXIF
// snapshot. Returns an error only when the file cannot be opened or the
// image header cannot be decoded — EXIF problems are silently absorbed.
func Extract(path string) (Metadata, error) {
	dims, err := readDimensions(path)
	if err != nil {
		return Metadata{}, err
	}
	exifData := readExif(path)
	return Metadata{
		Width:      dims.Width,
		Height:     dims.Height,
		TakenAt:    exifData.takenAt,
		CameraMake: exifData.cameraMake,
	}, nil
}

// readDimensions decodes only the image header (no pixels) to get
// width/height — orders of magnitude cheaper than a full decode.
func readDimensions(path string) (image.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return image.Config{}, fmt.Errorf("decoding image header for %s: %w", path, err)
	}
	return cfg, nil
}

// exifSnapshot bundles the EXIF fields we surface today so readExif can
// return them in one shot without a multi-value signature.
type exifSnapshot struct {
	takenAt    time.Time
	cameraMake string
}

// readExif returns the EXIF fields we care about, or zero values when
// the file has no EXIF block or the block is malformed. EXIF problems
// are not propagated: a missing capture time is normal, not an error.
func readExif(path string) exifSnapshot {
	f, err := os.Open(path)
	if err != nil {
		return exifSnapshot{}
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return exifSnapshot{}
	}

	var snap exifSnapshot
	if t, err := x.DateTime(); err == nil {
		snap.takenAt = t
	}
	if tag, err := x.Get(exif.Make); err == nil {
		if s, err := tag.StringVal(); err == nil {
			snap.cameraMake = s
		}
	}
	return snap
}
