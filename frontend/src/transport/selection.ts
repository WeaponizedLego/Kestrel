// Cross-island selection state. Vite bundles each island as its own
// entry, but modules imported by multiple entries still resolve to a
// single runtime instance — so the `selectedFolder` ref is shared
// across every mounted Vue app on the page. Sidebar writes it,
// PhotoGrid reads it, Vue's reactivity does the rest.
//
// Add state here only when two or more islands need to see the same
// value. Anything that lives inside one island stays inside it.

import { ref, watch } from 'vue'

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
const CELL_SIZE_STORAGE_KEY = 'kestrel.cellSize'

function loadPersistedCellSize(): number {
  if (typeof localStorage === 'undefined') return 280
  const raw = localStorage.getItem(CELL_SIZE_STORAGE_KEY)
  if (!raw) return 280
  const n = Number.parseInt(raw, 10)
  if (!Number.isFinite(n)) return 280
  return Math.min(CELL_SIZE_MAX, Math.max(CELL_SIZE_MIN, n))
}

export const cellSize = ref<number>(loadPersistedCellSize())

if (typeof window !== 'undefined') {
  watch(cellSize, (value) => {
    try {
      localStorage.setItem(CELL_SIZE_STORAGE_KEY, String(value))
    } catch {
      // Storage may be unavailable (private mode, quota); ignore.
    }
  })
}
