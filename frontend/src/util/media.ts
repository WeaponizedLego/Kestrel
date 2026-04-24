import type { Photo } from '../types'

// isVideo reports whether a photo represents a video file. The
// authoritative signal is the `kind:video` auto-tag emitted by the
// backend's autotag pipeline, which in turn keys off the file
// extension. Treating the auto-tag as the source of truth keeps the
// frontend free of a parallel extension list.
export function isVideo(photo: Photo): boolean {
  const auto = photo.AutoTags ?? []
  for (const t of auto) {
    if (t === 'kind:video') return true
  }
  return false
}
