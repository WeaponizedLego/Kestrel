// Photo mirrors the Go library.Photo struct. Field names use the JSON
// casing the Go encoder emits (PascalCase, because the Go struct has no
// json tags yet — a later phase may add tags and this type will follow).
export interface Photo {
  // Identity
  Path: string
  Hash: string

  // File-system attributes
  Name: string
  SizeBytes: number
  ModTime: string

  // Image attributes
  Width: number
  Height: number

  // EXIF attributes (zero values mean "absent")
  TakenAt: string
  CameraMake: string

  // User-assigned tags. Always normalized (lowercase, deduplicated)
  // on the server; null/undefined from older .gob files is treated as
  // an empty array.
  Tags: string[] | null

  // Auto-derived tags produced by internal/metadata/autotag during
  // scan (camera, year, place, kind, …). Rendered distinctly from
  // user Tags so the UI can show what was inferred vs. confirmed.
  // Null/undefined on v1 library files — treat as an empty array.
  AutoTags: string[] | null

  // 64-bit perceptual hash used by the assisted-tagging Cluster
  // view. Transported as a string (JSON numbers lose precision past
  // 2^53). Empty string = "not computed".
  PHash?: number | string
}

// TagCluster mirrors the Go cluster.Cluster struct. Members is a list
// of canonical photo paths (library keys); Untagged counts how many
// members have no user Tags yet.
export interface TagCluster {
  id: string
  members: string[]
  size: number
  untagged: number
}

// TaggingProgress mirrors cluster.Progress. Powers the HUD at the
// top of the Tagging Queue island.
export interface TaggingProgress {
  total: number
  tagged: number
  untagged: number
  largestUntaggedSize: number
}

// UntaggedPhoto mirrors api.untaggedPhotoDTO. Hash drives the
// thumbnail URL; width/height are hints for layout.
export interface UntaggedPhoto {
  path: string
  name: string
  width: number
  height: number
  hash: string
  sizeBytes: number
}

// UntaggedFolder groups UntaggedPhotos by their parent directory.
// Feeds the folder-browser Tagging Queue island.
export interface UntaggedFolder {
  folder: string
  count: number
  photos: UntaggedPhoto[]
}
