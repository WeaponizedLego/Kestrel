package library

import "time"

// Photo is the in-memory representation of a single image file. The
// fields split into two groups: identity (Path, Hash) which uniquely
// pins the file even if it moves, and attributes (everything else)
// that come from the file system header and EXIF block.
//
// Hash is the SHA-256 of the file contents and is the stable key used
// by thumbs.pack in Phase 5; it does not change when the file is
// renamed or moved.
type Photo struct {
	// Identity
	Path string
	Hash string

	// File-system attributes
	Name      string
	SizeBytes int64
	ModTime   time.Time

	// Image attributes
	Width  int
	Height int

	// EXIF attributes (zero values mean "absent")
	TakenAt    time.Time
	CameraMake string

	// User-assigned tags, stored lowercase and deduplicated. The
	// library is the only writer (see Library.SetTags), so readers can
	// rely on the canonical form without re-normalizing.
	Tags []string

	// Auto-derived tags (camera, year, kind, place, …) produced by
	// internal/metadata/autotag during scan. Stored separately from
	// Tags so they can be regenerated without clobbering user intent;
	// the UI renders them with a distinct chip style. Zero-value (nil)
	// on pre-v2 gob files until the next scan repopulates them.
	AutoTags []string

	// PHash is the 64-bit difference-hash of the thumbnail, used by
	// internal/library/cluster to group near-duplicate and visually
	// similar photos. Zero means "not yet computed"; legitimate hashes
	// can also be zero in theory, but the probability is astronomical
	// and the cluster package treats a hash of zero as absent.
	PHash uint64

	// Faces holds per-face detections produced by the optional
	// kestrel-vision sidecar. Empty on photos the sidecar hasn't
	// processed yet — and nil on pre-v3-era gob files until the next
	// scan repopulates them (gob zero-fills absent fields, so no
	// persistence version bump is required to add this field).
	//
	// Once written, Faces and the person:* tags derived from them
	// survive in library_meta.gob even if the sidecar is later
	// uninstalled, so filtering by named people keeps working.
	Faces []FaceDetection
}

// FaceDetection is one recognised face on a photo. BBox is in pixel
// coordinates of the original image; Embedding is an L2-normalised
// identity vector so cosine similarity against another face is just
// a dot product. PersonTag is the library-level user label (e.g.
// "alice") applied when the user names a face cluster — empty until
// the cluster is named.
type FaceDetection struct {
	BBox       [4]int
	Embedding  []float32
	Confidence float32
	PersonTag  string
}
