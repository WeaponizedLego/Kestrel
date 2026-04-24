// Package metadata extracts the per-image attributes Kestrel cares
// about: pixel dimensions and a small EXIF subset (capture time,
// camera/lens identifiers, exposure hints, and GPS coordinates).
//
// The implementation is best-effort. Files that have no EXIF block, or
// whose EXIF block is malformed, still yield a usable Metadata with
// zero fields — the only fatal errors come from being unable to open
// or decode the image at all.
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

	// Extended EXIF (used by internal/metadata/autotag to derive
	// auto-tags; not individually surfaced on Photo to avoid bloating
	// the persisted struct with fields the UI never reads back).
	CameraModel string
	LensModel   string
	ISO         int
	Orientation int  // EXIF 1–8; 0 = unknown
	FlashFired  bool // derived from the low bit of EXIF Flash

	// GPS in decimal degrees; GPSValid reports whether the source EXIF
	// actually carried coordinates (so a legitimate 0,0 fix is not
	// mistaken for absence).
	GPSLat   float64
	GPSLon   float64
	GPSValid bool
}

// Extract opens path and returns its dimensions plus a best-effort EXIF
// snapshot. Returns an error only when the file cannot be opened or the
// image header cannot be decoded — EXIF problems are silently absorbed.
//
// For video files the call delegates to ffprobe (when available); a
// missing ffprobe yields a zero Metadata rather than an error so the
// scanner still indexes the file.
func Extract(path string) (Metadata, error) {
	if isVideoPath(path) {
		return extractVideoMetadata(path), nil
	}
	dims, err := readDimensions(path)
	if err != nil {
		return Metadata{}, err
	}
	snap := readExif(path)
	return Metadata{
		Width:       dims.Width,
		Height:      dims.Height,
		TakenAt:     snap.takenAt,
		CameraMake:  snap.cameraMake,
		CameraModel: snap.cameraModel,
		LensModel:   snap.lensModel,
		ISO:         snap.iso,
		Orientation: snap.orientation,
		FlashFired:  snap.flashFired,
		GPSLat:      snap.gpsLat,
		GPSLon:      snap.gpsLon,
		GPSValid:    snap.gpsValid,
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
	takenAt     time.Time
	cameraMake  string
	cameraModel string
	lensModel   string
	iso         int
	orientation int
	flashFired  bool
	gpsLat      float64
	gpsLon      float64
	gpsValid    bool
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
	snap.cameraMake = stringTag(x, exif.Make)
	snap.cameraModel = stringTag(x, exif.Model)
	snap.lensModel = stringTag(x, exif.LensModel)
	snap.iso = intTag(x, exif.ISOSpeedRatings)
	snap.orientation = intTag(x, exif.Orientation)
	snap.flashFired = flashFired(x)
	snap.gpsLat, snap.gpsLon, snap.gpsValid = gpsCoords(x)
	return snap
}

// stringTag returns the trimmed string value of name, or "" when the
// tag is missing or not a string. goexif sometimes wraps strings in
// extra quotes; the caller strips those through StringVal.
func stringTag(x *exif.Exif, name exif.FieldName) string {
	tag, err := x.Get(name)
	if err != nil {
		return ""
	}
	s, err := tag.StringVal()
	if err != nil {
		return ""
	}
	return s
}

// intTag returns the first int value of name, or 0 when the tag is
// missing or not a short/long.
func intTag(x *exif.Exif, name exif.FieldName) int {
	tag, err := x.Get(name)
	if err != nil {
		return 0
	}
	v, err := tag.Int(0)
	if err != nil {
		return 0
	}
	return v
}

// flashFired reports whether EXIF Flash indicates the flash actually
// fired. The low bit of the Flash field is set when fired; the higher
// bits encode red-eye/mode and don't concern us here.
func flashFired(x *exif.Exif) bool {
	tag, err := x.Get(exif.Flash)
	if err != nil {
		return false
	}
	v, err := tag.Int(0)
	if err != nil {
		return false
	}
	return v&1 == 1
}

// gpsCoords pulls latitude + longitude from EXIF and returns them in
// decimal degrees, along with a valid flag. Missing or malformed tags
// yield (0, 0, false).
func gpsCoords(x *exif.Exif) (float64, float64, bool) {
	lat, lon, err := x.LatLong()
	if err != nil {
		return 0, 0, false
	}
	return lat, lon, true
}
