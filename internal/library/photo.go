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
}
