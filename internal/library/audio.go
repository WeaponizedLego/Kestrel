package library

import "time"

// Audio is the in-memory representation of a single audio file. It
// mirrors Photo's identity-and-attributes split but drops the image
// fields (Width/Height, EXIF) that don't apply, and replaces them
// with a small set of decoder-derived attributes.
//
// PHash is the 64-bit Chromaprint-derived audio fingerprint produced
// by internal/metadata/fingerprint. It is structurally identical to
// Photo.PHash (both uint64, both treat zero as absent), but the bits
// come from a different algorithm — audio and photo PHash values are
// never compared against each other (see internal/library/cluster).
type Audio struct {
	// Identity
	Path string
	Hash string

	// File-system attributes
	Name      string
	SizeBytes int64
	ModTime   time.Time

	// Audio attributes (zero values mean "absent", same convention as
	// Photo's EXIF block).
	Codec       string  // e.g. "mp3", "flac", "aac"
	DurationSec float64 // 0 = unknown
	BitrateKbps int     // 0 = unknown
	Channels    int     // 1 mono, 2 stereo, >2 surround; 0 = unknown

	// Tags follows the same contract as Photo.Tags: lowercase,
	// deduplicated, library-mediated writes only.
	Tags []string

	// AutoTags carries kind:audio plus codec/duration/bitrate/channels
	// buckets and a year:YYYY derived from ModTime (audio has no EXIF
	// capture time). Same renderer-distinct semantics as Photo.AutoTags.
	AutoTags []string

	// PHash is the Chromaprint-derived audio fingerprint folded to 64
	// bits. Zero means "not computed" (e.g. fpcalc unavailable). Audio
	// PHash values are clustered in a separate bucket from photo dHash
	// values; see internal/library/cluster.
	PHash uint64
}
