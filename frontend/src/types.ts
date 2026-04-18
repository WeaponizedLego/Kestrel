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
}
