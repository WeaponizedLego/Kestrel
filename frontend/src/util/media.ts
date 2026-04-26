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

// isAudio reports whether a photo entry is actually an audio file.
// Audio entries land on the wire as Photo-shaped JSON (see
// internal/api mergeSortedMedia) with a kind:audio auto-tag — the
// frontend uses this gate to render the audio badge in the grid and
// the <audio> element in the lightbox / details panel.
export function isAudio(photo: Photo): boolean {
  const auto = photo.AutoTags ?? []
  for (const t of auto) {
    if (t === 'kind:audio') return true
  }
  return false
}
