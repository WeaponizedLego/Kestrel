// Thumbnail URL helpers. With the token accepted as a ?token= query
// param we can point <img src> directly at /api/thumb, which lets the
// browser handle caching. When the backend emits "thumbnail:ready"
// we bump a per-path version counter so any <img> bound to that path
// re-fetches with a fresh URL.

import { token } from './api'
import { onEvent } from './events'

const versions = new Map<string, number>()
const listeners = new Set<(path: string) => void>()

onEvent('thumbnail:ready', (payload) => {
  const p = payload as { path?: string } | null
  if (!p?.path) return
  versions.set(p.path, (versions.get(p.path) ?? 0) + 1)
  for (const fn of listeners) fn(p.path)
})

export function onThumbnailReady(fn: (path: string) => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function thumbSrc(path: string): string {
  const params = new URLSearchParams({ path, token })
  const v = versions.get(path)
  if (v !== undefined) params.set('v', String(v))
  return `/api/thumb?${params.toString()}`
}
