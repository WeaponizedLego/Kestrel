// Cross-island selection state. Vite bundles each island as its own
// entry, but modules imported by multiple entries still resolve to a
// single runtime instance — so the `selectedFolder` ref is shared
// across every mounted Vue app on the page. Sidebar writes it,
// PhotoGrid reads it, Vue's reactivity does the rest.
//
// Add state here only when two or more islands need to see the same
// value. Anything that lives inside one island stays inside it.

import { ref, watch } from 'vue'
import { apiGet, apiPut } from './api'

// selectedFolder drives the photo grid's ?folder= filter. null means
// "no filter — show all photos". Any absolute path asks the backend
// to include that folder and its descendants.
export const selectedFolder = ref<string | null>(null)

// Multi-selection state for the photo grid. selectedPaths is the
// source of truth; the grid paints selection outlines from it and the
// side panel decides whether to render single-photo details or a
// multi-selection summary based on its size.
//
// anchorPath is the pivot for shift-click range selection — plain
// clicks and ctrl-toggles update it; shift-clicks read it. Range
// resolution needs the *current* photos array, which only PhotoGrid
// has, so we expose a helper that takes it as an argument instead of
// subscribing from here.
export const selectedPaths = ref<Set<string>>(new Set())
export const anchorPath = ref<string | null>(null)

export function clearSelection() {
  if (selectedPaths.value.size === 0 && anchorPath.value === null) return
  selectedPaths.value = new Set()
  anchorPath.value = null
}

export function selectOnly(path: string) {
  selectedPaths.value = new Set([path])
  anchorPath.value = path
}

export function toggleSelection(path: string) {
  const next = new Set(selectedPaths.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  selectedPaths.value = next
  anchorPath.value = path
}

// selectRange replaces the current selection with every photo between
// the anchor and path (inclusive), resolved against the supplied
// ordered path list. When there is no anchor it falls back to a
// single-path selection so shift-click with a cold start still does
// something sensible.
export function selectRange(path: string, orderedPaths: readonly string[]) {
  if (anchorPath.value === null) {
    selectOnly(path)
    return
  }
  const anchorIdx = orderedPaths.indexOf(anchorPath.value)
  const targetIdx = orderedPaths.indexOf(path)
  if (anchorIdx < 0 || targetIdx < 0) {
    selectOnly(path)
    return
  }
  const [lo, hi] = anchorIdx <= targetIdx
    ? [anchorIdx, targetIdx]
    : [targetIdx, anchorIdx]
  const next = new Set<string>()
  for (let i = lo; i <= hi; i++) next.add(orderedPaths[i])
  selectedPaths.value = next
  // Anchor stays put so repeated shift-clicks pivot on the original
  // starting point, matching Finder / File Explorer behavior.
}

// addPathsToSelection merges additional paths into the current
// selection. Used by marquee drag with Ctrl held; a plain marquee
// replaces the selection and should use selectedPaths.value directly.
export function addPathsToSelection(paths: Iterable<string>) {
  const next = new Set(selectedPaths.value)
  for (const p of paths) next.add(p)
  selectedPaths.value = next
}

// cellSize is the thumbnail grid pitch in pixels (thumb + padding).
// Shared so the Toolbar slider can drive PhotoGrid's virtual layout.
// 280 matches the original hardcoded default (256 thumb + 24 padding).
export const CELL_SIZE_MIN = 140
export const CELL_SIZE_MAX = 480
export const CELL_SIZE_STEP = 20

export const cellSize = ref<number>(280)

// Hydrate from the server-side settings store. Browser localStorage
// can't be used: the prod binary binds a random loopback port each
// launch and localStorage is keyed per-origin, so any value stored
// there is lost on every restart. This module is imported by Sidebar,
// Toolbar and PhotoGrid; the GET runs once per page load thanks to
// ES-module caching.
if (typeof window !== 'undefined') {
  apiGet<{ cell_size?: number }>('/api/settings')
    .then((s) => {
      if (typeof s.cell_size === 'number' && Number.isFinite(s.cell_size)) {
        cellSize.value = Math.min(CELL_SIZE_MAX, Math.max(CELL_SIZE_MIN, s.cell_size))
      }
    })
    .catch((err) => {
      // Defaults are already applied — log and move on.
      console.warn('loading cell size failed', err)
    })

  // Debounce so the slider drag (which fires per pixel) doesn't flood
  // the backend with PUTs.
  let saveTimer: number | null = null
  watch(cellSize, (value) => {
    if (saveTimer !== null) window.clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => {
      apiPut('/api/settings', { cell_size: value }).catch((err) => {
        console.warn('persisting cell size failed', err)
      })
    }, 200)
  })
}
