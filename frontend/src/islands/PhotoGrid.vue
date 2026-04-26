<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue'
import { apiGet, apiPost, apiPut, friendlyError } from '../transport/api'
import { onEvent } from '../transport/events'
import { thumbSrc, onThumbnailReady } from '../transport/thumbs'
import {
  selectedFolder,
  cellSize,
  selectedPaths,
  anchorPath,
  clearSelection,
  selectOnly,
  selectRange,
  toggleSelection,
  addPathsToSelection,
} from '../transport/selection'
import { resyncing, runResync } from '../transport/resync'
import { requestedSearchTokens } from '../transport/search'
import { copyImageToClipboard } from '../transport/clipboard'
import { requestDelete, requestMove } from '../transport/fileops'
import type { Photo } from '../types'
import { isVideo } from '../util/media'

// Lazy-loaded so the viewer JS/CSS only downloads when a user opens
// a photo — matches TASKS.md Phase 8 "PhotoViewer lazy-loaded via
// dynamic import".
const PhotoViewer = defineAsyncComponent(() => import('./PhotoViewer.vue'))
const FolderPicker = defineAsyncComponent(() => import('./FolderPicker.vue'))
const TagInput = defineAsyncComponent(() => import('../components/TagInput.vue'))
const SelectionSummary = defineAsyncComponent(() => import('../components/SelectionSummary.vue'))

const overscanRows = 2 // render a little above/below the viewport

const folder = ref('')
const pickerOpen = ref(false)

// Scan lifecycle state — driven by WS events so every tab stays in
// sync, and by a status probe on mount so a mid-scan refresh picks
// up where it left off.
const scanning = ref(false)
const cancelling = ref(false)

function openPicker() { pickerOpen.value = true }
function closePicker() { pickerOpen.value = false }
function onPickerChoose(path: string) {
  folder.value = path
  pickerOpen.value = false
}
const photos = shallowRef<Photo[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

// Virtual scroll state — recomputed on scroll and resize.
const scroller = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewportHeight = ref(0)
const viewportWidth = ref(0)

// Force <img> refreshes when thumbnail:ready fires for a visible path.
// Keyed by path; the value is a monotonic counter appended to the URL.
const thumbVersion = ref(0)
const touchedPaths = new Set<string>()

const columns = computed(() => {
  return Math.max(1, Math.floor(viewportWidth.value / cellSize.value))
})
const totalRows = computed(() => Math.ceil(photos.value.length / columns.value))
const totalHeight = computed(() => totalRows.value * cellSize.value)

interface Cell {
  photo: Photo
  index: number
  x: number
  y: number
}

const visibleCells = computed<Cell[]>(() => {
  if (!photos.value.length || !viewportHeight.value) return []
  const cols = columns.value
  const pitch = cellSize.value
  const firstRow = Math.max(0, Math.floor(scrollTop.value / pitch) - overscanRows)
  const lastRow = Math.min(
    totalRows.value,
    Math.ceil((scrollTop.value + viewportHeight.value) / pitch) + overscanRows,
  )
  const cells: Cell[] = []
  for (let row = firstRow; row < lastRow; row++) {
    for (let col = 0; col < cols; col++) {
      const index = row * cols + col
      if (index >= photos.value.length) break
      cells.push({
        photo: photos.value[index],
        index,
        x: col * pitch,
        y: row * pitch,
      })
    }
  }
  return cells
})

// Focused cell for keyboard nav; -1 means "nothing focused yet".
const focused = ref(-1)
const viewerIndex = ref(-1)
const viewerPhoto = computed<Photo | null>(() =>
  viewerIndex.value >= 0 ? photos.value[viewerIndex.value] ?? null : null,
)
const viewerRef = ref<{ openPreview: () => void } | null>(null)

// Sort + search controls. Server does the heavy lifting (see
// CLAUDE.md: "No JS-side sorting or filtering"); the grid just
// composes query params and debounces the search typing.
type SortKey = 'name' | 'date' | 'size'
type SortOrder = 'asc' | 'desc'
const sortKey = ref<SortKey>('date')
const sortOrder = ref<SortOrder>('desc')

// Hydrate sort prefs from the server-side settings store. localStorage
// can't be used here — the prod binary binds a random loopback port
// each launch and localStorage is keyed per-origin, so any value
// stored there evaporates on restart.
apiGet<{ sort_key?: string; sort_order?: string }>('/api/settings')
  .then((s) => {
    if (s.sort_key === 'name' || s.sort_key === 'date' || s.sort_key === 'size') {
      sortKey.value = s.sort_key
    }
    if (s.sort_order === 'asc' || s.sort_order === 'desc') {
      sortOrder.value = s.sort_order
    }
  })
  .catch((err) => {
    console.warn('loading sort preferences failed', err)
  })
const searchTokens = ref<string[]>([])
const searchMode = ref<'all' | 'any'>('all')
const searchDebounced = ref('')
const searchDebounceMs = 250

// Tokens are committed by TagInput (space/Enter/blur), so there's no
// per-keystroke flood to debounce. The timer still cushions rapid
// add/remove bursts.
let searchTimer: number | null = null
watch(searchTokens, (value) => {
  if (searchTimer !== null) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    searchDebounced.value = value.join(' ')
  }, searchDebounceMs)
}, { deep: true })

// One-shot handoff from other islands (e.g. TagManager clicking a
// tag) — copy the requested tokens into our own state and reset the
// shared ref so a repeat click with the same tokens still fires.
watch(requestedSearchTokens, (tokens) => {
  if (tokens === null) return
  searchTokens.value = [...tokens]
  requestedSearchTokens.value = null
})

async function scan() {
  if (!folder.value) return
  error.value = null
  try {
    // Fire-and-forget: /api/scan now returns 202 immediately with
    // {id, root}. Progress/completion arrive over the WS event
    // stream; scanning=true is set there so a mid-flight page load
    // (which subscribes on mount) still picks it up.
    await apiPost<{ id: string; root: string }>('/api/scan', { folder: folder.value })
  } catch (err) {
    error.value = friendlyError(err)
  }
}

// Re-sync runs quietly in the background. The button lives here; the
// state and result message are shared with the StatusBar via the
// transport/resync module, so the user sees progress and outcome in
// the footer instead of next to the button.

async function cancelScan() {
  if (!scanning.value) return
  cancelling.value = true
  try {
    await apiPost<{ cancelled: boolean }>('/api/scan/cancel', {})
  } catch (err) {
    error.value = friendlyError(err)
    cancelling.value = false
  }
}

async function loadPhotos() {
  loading.value = true
  error.value = null

  // Snapshot anchor paths so a refresh (scan finish, resync, etc.)
  // doesn't warp the viewport. We key on path, not index, because
  // deletions in the middle of the list would otherwise shift every
  // later photo under the user's scroll position.
  const anchorTopPath = visibleCells.value[0]?.photo.Path ?? null
  const anchorFocusedPath = focused.value >= 0
    ? photos.value[focused.value]?.Path ?? null
    : null

  try {
    const q = new URLSearchParams({ sort: sortKey.value, order: sortOrder.value })
    if (searchDebounced.value) {
      q.set('q', searchDebounced.value)
      q.set('match', searchMode.value)
    }
    if (selectedFolder.value) q.set('folder', selectedFolder.value)
    const next = await apiGet<Photo[]>(`/api/photos?${q.toString()}`)
    photos.value = next

    // Restore focus/scroll from the snapshot above. If the anchor
    // photo was deleted the focus falls back to the nearest surviving
    // index, matching users' expectation that "my place" persists.
    if (anchorFocusedPath) {
      const idx = next.findIndex((p) => p.Path === anchorFocusedPath)
      focused.value = idx >= 0 ? idx : (next.length ? 0 : -1)
    } else {
      focused.value = next.length ? 0 : -1
    }
    if (anchorTopPath) {
      const topIdx = next.findIndex((p) => p.Path === anchorTopPath)
      if (topIdx >= 0) {
        nextTick(() => {
          const el = scroller.value
          if (!el) return
          const row = Math.floor(topIdx / columns.value)
          el.scrollTop = row * cellSize.value
          scrollTop.value = el.scrollTop
        })
      }
    }
  } catch (err) {
    error.value = friendlyError(err)
    photos.value = []
  } finally {
    loading.value = false
  }
}

// pendingScrollToIndex overrides the default top-visible anchoring
// during the next metrics tick. openAt sets it when the viewer first
// mounts so the clicked photo is brought into view after the reflow.
let pendingScrollToIndex: number | null = null

function updateMetrics() {
  const el = scroller.value
  if (!el) return
  const newWidth = el.clientWidth
  const oldWidth = viewportWidth.value
  const pitch = cellSize.value

  // Scroll-to-index takes priority over top-visible anchoring: when
  // the viewer opens, the photo the user clicked must stay on screen
  // even if it wasn't the top-left cell before.
  let anchorIndex = -1
  let scrollMode: 'pin-top' | 'ensure-visible' = 'pin-top'
  if (pendingScrollToIndex !== null) {
    anchorIndex = pendingScrollToIndex
    scrollMode = 'ensure-visible'
    pendingScrollToIndex = null
  } else if (oldWidth > 0 && newWidth !== oldWidth) {
    // Width changed without an explicit target (window resize, slider
    // drag, closing the viewer) — anchor on the first on-screen photo
    // so the rebuilt grid keeps the user's place.
    const oldCols = Math.max(1, Math.floor(oldWidth / pitch))
    const topRow = Math.floor(el.scrollTop / pitch)
    anchorIndex = topRow * oldCols
  }

  viewportHeight.value = el.clientHeight
  viewportWidth.value = newWidth
  scrollTop.value = el.scrollTop

  if (anchorIndex >= 0) {
    const targetIndex = anchorIndex
    nextTick(() => {
      const scr = scroller.value
      if (!scr) return
      const newCols = Math.max(1, Math.floor(scr.clientWidth / cellSize.value))
      const newRow = Math.floor(targetIndex / newCols)
      const rowTop = newRow * cellSize.value
      if (scrollMode === 'ensure-visible') {
        const rowBottom = rowTop + cellSize.value
        if (rowTop < scr.scrollTop) scr.scrollTop = rowTop
        else if (rowBottom > scr.scrollTop + scr.clientHeight)
          scr.scrollTop = rowBottom - scr.clientHeight
      } else {
        scr.scrollTop = rowTop
      }
      scrollTop.value = scr.scrollTop
    })
  }
}

let viewportPostDebounce: number | null = null
function scheduleViewportPrefetch() {
  if (viewportPostDebounce !== null) window.clearTimeout(viewportPostDebounce)
  viewportPostDebounce = window.setTimeout(() => {
    viewportPostDebounce = null
    const visible = visibleCells.value.map((c) => c.photo.Path)
    if (!visible.length) return
    const cols = columns.value
    const lastVisibleIdx = visibleCells.value[visibleCells.value.length - 1]?.index ?? 0
    const lookahead = photos.value
      .slice(lastVisibleIdx + 1, lastVisibleIdx + 1 + cols * 4)
      .map((p) => p.Path)
    apiPost('/api/viewport', { paths: visible, lookahead }).catch((err) => {
      console.error('viewport hint failed', err)
    })
  }, 120)
}

function onScroll() {
  updateMetrics()
  scheduleViewportPrefetch()
  if (ctxOpen.value) closeCtxMenu()
}

function imgSrc(path: string): string {
  // Touch thumbVersion so this computed depends on it — every
  // thumbnail:ready event bumps the counter and reshapes the src.
  void thumbVersion.value
  if (touchedPaths.has(path)) touchedPaths.delete(path)
  return thumbSrc(path)
}

function openAt(index: number) {
  if (index < 0 || index >= photos.value.length) return
  viewerIndex.value = index
  focused.value = index
  selectOnly(photos.value[index].Path)
}

// onCellClick routes a cell click through the modifier-key handling
// the feature spec calls for. Plain click opens the viewer on that
// photo (and collapses the selection to it); Ctrl/Meta toggles; Shift
// extends from the selection anchor. Any modifier click closes an
// open viewer so the panel can switch to the multi-selection summary.
function onCellClick(index: number, e: MouseEvent) {
  if (index < 0 || index >= photos.value.length) return
  const path = photos.value[index].Path
  focused.value = index
  if (e.shiftKey) {
    e.preventDefault()
    viewerIndex.value = -1
    selectRange(path, photos.value.map((p) => p.Path))
    return
  }
  if (e.ctrlKey || e.metaKey) {
    e.preventDefault()
    viewerIndex.value = -1
    toggleSelection(path)
    return
  }
  openAt(index)
}

// onCellDblClick is the shortcut for "open the full-size lightbox
// preview". It reuses openAt() so selection/viewer state match a
// normal click, then flips the viewer's previewOpen flag — which
// streams the original file from /api/photo (not the thumbnail).
async function onCellDblClick(index: number) {
  if (index < 0 || index >= photos.value.length) return
  openAt(index)
  await nextTick()
  viewerRef.value?.openPreview()
}

// Right-click context menu. Mirrors the file-manager convention: if
// the right-clicked photo is part of the current multi-selection the
// menu acts on the whole selection; otherwise it collapses selection
// to that one photo. Move/Delete dispatch through the FileOps island
// (folder picker, confirm modal, undo toast all live there).
const ctxOpen = ref(false)
const ctxX = ref(0)
const ctxY = ref(0)
const ctxIndex = ref(-1)
const ctxTargets = ref<string[]>([])
const ctxIsVideo = ref(false)
const ctxSingle = computed(() => ctxTargets.value.length === 1)
const ctxCopyError = ref<string | null>(null)

function openCtxMenu(index: number, e: MouseEvent) {
  if (index < 0 || index >= photos.value.length) return
  e.preventDefault()
  const photo = photos.value[index]
  const path = photo.Path
  if (selectedPaths.value.has(path) && selectedPaths.value.size > 1) {
    const ordered: string[] = []
    for (const p of photos.value) {
      if (selectedPaths.value.has(p.Path)) ordered.push(p.Path)
    }
    ctxTargets.value = ordered
  } else {
    selectOnly(path)
    viewerIndex.value = -1
    focused.value = index
    ctxTargets.value = [path]
  }
  ctxIndex.value = index
  ctxIsVideo.value = isVideo(photo)
  ctxX.value = e.clientX
  ctxY.value = e.clientY
  ctxCopyError.value = null
  ctxOpen.value = true
}

function closeCtxMenu() {
  ctxOpen.value = false
  ctxCopyError.value = null
}

async function ctxPreview() {
  if (!ctxSingle.value) return
  const idx = ctxIndex.value
  closeCtxMenu()
  if (idx < 0 || idx >= photos.value.length) return
  openAt(idx)
  await nextTick()
  viewerRef.value?.openPreview()
}

async function ctxCopy() {
  if (!ctxSingle.value || ctxIsVideo.value) return
  const path = ctxTargets.value[0]
  try {
    await copyImageToClipboard(path)
    closeCtxMenu()
  } catch (err) {
    ctxCopyError.value = friendlyError(err)
  }
}

function ctxMove() {
  if (ctxTargets.value.length === 0) return
  requestMove([...ctxTargets.value])
  closeCtxMenu()
}

function ctxDelete() {
  if (ctxTargets.value.length === 0) return
  requestDelete([...ctxTargets.value])
  closeCtxMenu()
}

function onCtxKey(e: KeyboardEvent) {
  if (ctxOpen.value && e.key === 'Escape') {
    e.stopPropagation()
    closeCtxMenu()
  }
}

// multiSelection photos — the panel switches to SelectionSummary when
// this has 2+ items. Order follows the grid's current sort so the
// aggregate "n selected" count is stable across sort flips.
const multiSelection = computed<Photo[]>(() => {
  if (selectedPaths.value.size < 2) return []
  const out: Photo[] = []
  for (const p of photos.value) {
    if (selectedPaths.value.has(p.Path)) out.push(p)
  }
  return out
})

function clearAllSelection() {
  clearSelection()
  viewerIndex.value = -1
}

// Marquee drag selection. We track coordinates in scroller content
// space (i.e. including scrollTop) so selection rectangles extend
// correctly when the drag crosses into an unscrolled region. Only
// mousedown on the scroller's blank area starts a marquee — cells
// own their click handling above.
interface MarqueeRect { x1: number; y1: number; x2: number; y2: number }
const marquee = ref<MarqueeRect | null>(null)
let marqueeBase = new Set<string>()
let marqueeAdditive = false

// Auto-scroll state while a marquee drag is near the viewport edge.
// We keep the latest mouse position in client coordinates and tick on
// rAF; each tick nudges scrollTop and re-derives the content-space
// marquee corner so the rectangle grows with the scroll.
const autoScrollEdge = 48
const autoScrollMax = 24
let autoScrollRAF = 0
let lastClientX = 0
let lastClientY = 0

const marqueeBox = computed(() => {
  const m = marquee.value
  if (!m) return null
  return {
    left: Math.min(m.x1, m.x2),
    top: Math.min(m.y1, m.y2),
    width: Math.abs(m.x2 - m.x1),
    height: Math.abs(m.y2 - m.y1),
  }
})

function onScrollerMouseDown(e: MouseEvent) {
  if (e.button !== 0) return
  const target = e.target as HTMLElement | null
  if (target?.closest('.photo-grid__cell')) return
  const el = scroller.value
  if (!el) return
  e.preventDefault()

  marqueeAdditive = e.ctrlKey || e.metaKey || e.shiftKey
  marqueeBase = marqueeAdditive ? new Set(selectedPaths.value) : new Set()
  if (!marqueeAdditive) {
    clearSelection()
    viewerIndex.value = -1
  }

  const pt = pointInContent(e, el)
  marquee.value = { x1: pt.x, y1: pt.y, x2: pt.x, y2: pt.y }
  window.addEventListener('mousemove', onMarqueeMove)
  window.addEventListener('mouseup', onMarqueeUp)
}

function onMarqueeMove(e: MouseEvent) {
  const el = scroller.value
  const m = marquee.value
  if (!el || !m) return
  lastClientX = e.clientX
  lastClientY = e.clientY
  const pt = pointInContent(e, el)
  marquee.value = { ...m, x2: pt.x, y2: pt.y }
  applyMarqueeSelection()
  ensureAutoScroll()
}

function onMarqueeUp() {
  marquee.value = null
  if (autoScrollRAF !== 0) {
    cancelAnimationFrame(autoScrollRAF)
    autoScrollRAF = 0
  }
  // Nudge the anchor to the last photo we touched so a follow-up
  // shift-click extends from here rather than the previous plain
  // click — this matches what Finder does after a drag.
  const top = [...selectedPaths.value].pop()
  if (top) anchorPath.value = top
  window.removeEventListener('mousemove', onMarqueeMove)
  window.removeEventListener('mouseup', onMarqueeUp)
}

// ensureAutoScroll kicks the rAF loop if it isn't already running.
// autoScrollTick keeps itself alive while the mouse is still near an
// edge and the marquee is still active.
function ensureAutoScroll() {
  if (autoScrollRAF !== 0) return
  autoScrollRAF = requestAnimationFrame(autoScrollTick)
}

function autoScrollTick() {
  autoScrollRAF = 0
  const el = scroller.value
  const m = marquee.value
  if (!el || !m) return
  const rect = el.getBoundingClientRect()
  const dy = edgeVelocity(lastClientY, rect.top, rect.bottom)
  const dx = edgeVelocity(lastClientX, rect.left, rect.right)
  if (dx === 0 && dy === 0) return

  // Scroll and clamp so we don't keep scheduling work past the end of
  // the list. Reading scrollTop back catches the browser's own clamp.
  el.scrollTop = Math.max(0, el.scrollTop + dy)
  el.scrollLeft = Math.max(0, el.scrollLeft + dx)
  // Keep the marquee corner tracking the stationary cursor: as the
  // content scrolls under it, the content-space coordinate shifts.
  const pt = {
    x: lastClientX - rect.left + el.scrollLeft,
    y: lastClientY - rect.top + el.scrollTop,
  }
  marquee.value = { ...m, x2: pt.x, y2: pt.y }
  applyMarqueeSelection()
  autoScrollRAF = requestAnimationFrame(autoScrollTick)
}

// edgeVelocity returns a signed pixels-per-tick delta based on how
// deep into the edge threshold the pointer has crept. Linear ramp
// from 0 at the threshold to autoScrollMax at the viewport edge.
function edgeVelocity(pos: number, min: number, max: number): number {
  if (pos < min + autoScrollEdge) {
    const depth = Math.min(autoScrollEdge, min + autoScrollEdge - pos)
    return -Math.round((depth / autoScrollEdge) * autoScrollMax)
  }
  if (pos > max - autoScrollEdge) {
    const depth = Math.min(autoScrollEdge, pos - (max - autoScrollEdge))
    return Math.round((depth / autoScrollEdge) * autoScrollMax)
  }
  return 0
}

function pointInContent(e: MouseEvent, el: HTMLElement) {
  const rect = el.getBoundingClientRect()
  return {
    x: e.clientX - rect.left + el.scrollLeft,
    y: e.clientY - rect.top + el.scrollTop,
  }
}

function applyMarqueeSelection() {
  const m = marquee.value
  if (!m) return
  const pitch = cellSize.value
  const cols = columns.value
  const gutter = 3
  const inner = pitch - gutter * 2

  const xmin = Math.min(m.x1, m.x2)
  const xmax = Math.max(m.x1, m.x2)
  const ymin = Math.min(m.y1, m.y2)
  const ymax = Math.max(m.y1, m.y2)

  const next = new Set(marqueeBase)
  // Instead of iterating every photo, compute the row/column span the
  // rectangle overlaps and visit only those cells. O(cells in rect)
  // even for million-photo libraries.
  const firstRow = Math.max(0, Math.floor(ymin / pitch))
  const lastRow = Math.min(Math.max(totalRows.value - 1, 0), Math.floor(ymax / pitch))
  const firstCol = Math.max(0, Math.floor(xmin / pitch))
  const lastCol = Math.min(cols - 1, Math.floor(xmax / pitch))
  for (let r = firstRow; r <= lastRow; r++) {
    for (let c = firstCol; c <= lastCol; c++) {
      // Only select cells whose inner box actually intersects the
      // rectangle — the rectangle can cross a cell's gutter without
      // touching its image.
      const cellLeft = c * pitch + gutter
      const cellRight = cellLeft + inner
      const cellTop = r * pitch + gutter
      const cellBottom = cellTop + inner
      if (cellRight < xmin || cellLeft > xmax) continue
      if (cellBottom < ymin || cellTop > ymax) continue
      const idx = r * cols + c
      if (idx >= photos.value.length) continue
      next.add(photos.value[idx].Path)
    }
  }
  selectedPaths.value = next
}

function closeViewer() {
  const idx = viewerIndex.value
  viewerIndex.value = -1
  // After the viewer unmounts, give focus back to the cell that
  // opened it so keyboard navigation resumes where the user left off.
  nextTick(() => {
    if (idx < 0) return
    ensureVisible(idx)
    const btn = scroller.value?.querySelector<HTMLElement>(
      `[data-index="${idx}"]`,
    )
    btn?.focus()
  })
}

function onKeydown(e: KeyboardEvent) {
  if (!photos.value.length) return
  if (viewerIndex.value >= 0) return // viewer owns its keys
  // Ignore keystrokes coming from form fields (TagInput in the
  // summary panel, the search pill-input, any future input). Space
  // and Enter in those contexts are for the editor; without this
  // guard, pressing Space to commit a tag would also trigger
  // openAt(focused) and collapse the selection.
  if (isEditableTarget(e.target)) return
  const cols = columns.value
  let next = focused.value
  switch (e.key) {
    case 'ArrowRight': next = Math.min(photos.value.length - 1, next + 1); break
    case 'ArrowLeft':  next = Math.max(0, next - 1); break
    case 'ArrowDown':  next = Math.min(photos.value.length - 1, next + cols); break
    case 'ArrowUp':    next = Math.max(0, next - cols); break
    case 'Enter':
    case ' ':
      if (focused.value >= 0) openAt(focused.value)
      e.preventDefault()
      return
    default:
      return
  }
  e.preventDefault()
  focused.value = next
  ensureVisible(next)

  // Shift+arrow extends the selection from the anchor to the new
  // focused photo, matching the standard Finder / File Explorer
  // contract. A bare arrow leaves the current selection alone — it's
  // only moving the keyboard cursor.
  if (e.shiftKey && next >= 0 && next < photos.value.length) {
    const path = photos.value[next].Path
    if (anchorPath.value === null) anchorPath.value = path
    selectRange(path, photos.value.map((p) => p.Path))
    viewerIndex.value = -1
  }
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
}

function ensureVisible(index: number) {
  const el = scroller.value
  if (!el) return
  const pitch = cellSize.value
  const row = Math.floor(index / columns.value)
  const top = row * pitch
  const bottom = top + pitch
  if (top < el.scrollTop) el.scrollTop = top
  else if (bottom > el.scrollTop + el.clientHeight) el.scrollTop = bottom - el.clientHeight
}

let resizeObserver: ResizeObserver | null = null
let unsubThumb: (() => void) | null = null
let unsubLibrary: (() => void) | null = null
let unsubScanStart: (() => void) | null = null
let unsubScanDone: (() => void) | null = null
let unsubFileOpsDone: (() => void) | null = null
let unsubFileOpsUndone: (() => void) | null = null

// The scroller only exists while the v-else branch is rendered (i.e.
// there are photos to show). Watching the template ref means we hook
// up measurements and the ResizeObserver the moment Vue inserts the
// element — and tear down when it's removed. Doing this in onMounted
// alone misses the case where the first load arrives photo-less and
// the scroller is mounted later by the v-else switch.
watch(scroller, (el) => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (!el) return
  updateMetrics()
  resizeObserver = new ResizeObserver(updateMetrics)
  resizeObserver.observe(el)
})

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('keydown', onCtxKey, true)
  window.addEventListener('blur', closeCtxMenu)
  unsubThumb = onThumbnailReady((path) => {
    touchedPaths.add(path)
    thumbVersion.value++
  })
  // A scan anywhere in the app should show up here too, even if the
  // user isn't the one who triggered it (future: multi-window).
  unsubLibrary = onEvent('library:updated', () => loadPhotos())
  unsubScanStart = onEvent('scan:started', () => {
    scanning.value = true
    cancelling.value = false
  })
  unsubScanDone = onEvent('scan:done', () => {
    scanning.value = false
    cancelling.value = false
  })
  // File ops mutate the library server-side; reloading is the
  // simplest path to a correct grid. A future optimisation could
  // patch in place using the `results` payload to avoid the full
  // refetch on small batches.
  unsubFileOpsDone = onEvent('fileops:done', () => loadPhotos())
  unsubFileOpsUndone = onEvent('fileops:undone', () => loadPhotos())
  // Catch up after a page refresh that landed mid-scan — the events
  // above only cover future transitions.
  probeScanStatus()
  loadPhotos()
})

async function probeScanStatus() {
  try {
    const s = await apiGet<{ running: boolean; id: string; root: string }>('/api/scan/status')
    scanning.value = s.running
  } catch {
    // Non-fatal — the UI just won't pre-populate, which is fine.
  }
}

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('keydown', onCtxKey, true)
  window.removeEventListener('blur', closeCtxMenu)
  unsubThumb?.()
  unsubLibrary?.()
  unsubScanStart?.()
  unsubScanDone?.()
  unsubFileOpsDone?.()
  unsubFileOpsUndone?.()
})

// loadPhotos runs on any server-shaping param change. Running it on
// mount too restores the view after a restart — the library is
// already persisted, so there's nothing to wait on.
watch([sortKey, sortOrder, searchDebounced, searchMode, selectedFolder], () => {
  loadPhotos()
})

watch(sortKey, (v) => {
  apiPut('/api/settings', { sort_key: v }).catch((err) => {
    console.warn('persisting sort key failed', err)
  })
})
watch(sortOrder, (v) => {
  apiPut('/api/settings', { sort_order: v }).catch((err) => {
    console.warn('persisting sort order failed', err)
  })
})

// When the user drags the size slider, anchor the top-visible photo so
// resizing doesn't warp them to a different part of the library. We
// snapshot the first on-screen photo index from the *old* pitch, then
// scroll to that same photo's new row after computeds re-derive.
watch(cellSize, (_newSize, oldSize) => {
  const el = scroller.value
  if (!el || !oldSize) return
  const oldCols = Math.max(1, Math.floor(viewportWidth.value / oldSize))
  const topRow = Math.floor(el.scrollTop / oldSize)
  const anchorIndex = topRow * oldCols
  nextTick(() => {
    const pitch = cellSize.value
    const cols = columns.value
    const newRow = Math.floor(anchorIndex / cols)
    el.scrollTop = newRow * pitch
    updateMetrics()
  })
})
</script>

<template>
  <section class="flex h-full min-h-0 flex-col gap-3">
    <header class="flex flex-col gap-2 shrink-0">
      <div class="flex flex-wrap items-center gap-2">
        <label class="input input-sm input-bordered flex flex-1 min-w-72 items-center gap-2 font-mono">
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true" class="opacity-60">
            <path d="M1.5 3C1.5 2.4 1.9 2 2.5 2H4.5L5.5 3H9.5C10.1 3 10.5 3.4 10.5 4V9C10.5 9.6 10.1 10 9.5 10H2.5C1.9 10 1.5 9.6 1.5 9V3Z" stroke="currentColor" stroke-width="1" stroke-linejoin="round"/>
          </svg>
          <input
            v-model="folder"
            type="text"
            class="grow"
            placeholder="/absolute/path/to/photos"
            @keydown.enter="scan"
          />
        </label>
        <button type="button" class="btn btn-sm btn-ghost" :disabled="scanning" @click="openPicker">Browse</button>
        <button
          v-if="!scanning"
          type="button"
          class="btn btn-sm btn-primary"
          :disabled="!folder"
          @click="scan"
        >Scan</button>
        <button
          v-else
          type="button"
          class="btn btn-sm btn-error btn-outline"
          :disabled="cancelling"
          @click="cancelScan"
        >
          {{ cancelling ? 'Cancelling…' : 'Cancel' }}
        </button>
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          :disabled="resyncing || scanning"
          title="Re-scan the selected folder (or every watched root) for new, changed, and deleted files"
          @click="runResync"
        >
          {{ resyncing ? 'Re-scanning…' : 'Re-scan' }}
        </button>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <TagInput
          v-model="searchTokens"
          suggestions="search"
          class="flex-1 min-w-60"
          placeholder="Search name or tag…"
          aria-label="Search photos by name or tag"
        />
        <div class="join" role="group" aria-label="Match mode">
          <button
            type="button"
            :class="['btn btn-sm join-item', searchMode === 'all' ? 'btn-primary' : 'btn-ghost']"
            @click="searchMode = 'all'"
          >All</button>
          <button
            type="button"
            :class="['btn btn-sm join-item', searchMode === 'any' ? 'btn-primary' : 'btn-ghost']"
            @click="searchMode = 'any'"
          >Any</button>
        </div>
        <select class="select select-sm select-bordered" v-model="sortKey" aria-label="Sort by">
          <option value="name">Name</option>
          <option value="date">Date taken</option>
          <option value="size">Size</option>
        </select>
        <button
          type="button"
          class="btn btn-sm btn-square btn-ghost"
          :aria-label="sortOrder === 'asc' ? 'Ascending' : 'Descending'"
          @click="sortOrder = sortOrder === 'asc' ? 'desc' : 'asc'"
        >
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
            <path v-if="sortOrder === 'asc'" d="M6 9.5V2.5M3 5.5L6 2.5L9 5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
            <path v-else d="M6 2.5V9.5M3 6.5L6 9.5L9 6.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
      </div>
    </header>

    <div class="flex flex-1 min-h-0 gap-3">
      <div v-if="error" role="alert" class="alert alert-error flex-1">{{ error }}</div>

      <div
        v-else-if="loading && photos.length === 0"
        class="grid flex-1 min-w-0 gap-0.5 overflow-hidden p-0"
        style="grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));"
        aria-label="Loading photos"
        aria-busy="true"
      >
        <div
          v-for="n in 18"
          :key="n"
          class="skeleton aspect-square rounded"
        />
      </div>

      <div
        v-else-if="!loading && photos.length === 0"
        class="flex flex-1 flex-col items-center justify-center gap-3 p-12 text-center text-base-content/50"
      >
        <svg width="48" height="48" viewBox="0 0 48 48" fill="none" aria-hidden="true" class="opacity-50">
          <rect x="6" y="10" width="36" height="28" rx="3" stroke="currentColor" stroke-width="1.5"/>
          <circle cx="17" cy="21" r="3" stroke="currentColor" stroke-width="1.5"/>
          <path d="M6 32L18 22L28 30L36 24L42 28" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>
        </svg>
        <h3 class="m-0 text-lg font-semibold text-base-content/70">
          {{ searchDebounced ? 'No matches' : 'No photos yet' }}
        </h3>
        <p class="m-0 max-w-sm text-sm">
          {{ searchDebounced
              ? `Nothing matches ${searchMode === 'all' ? 'all' : 'any'} of: ${searchTokens.join(', ')}.`
              : 'Point Kestrel at a folder and scan to build your library.' }}
        </p>
      </div>

      <div
        v-else
        ref="scroller"
        class="flex-1 min-w-0 overflow-auto bg-base-200 border border-base-300 rounded-box outline-none"
        tabindex="0"
        @scroll.passive="onScroll"
        @mousedown="onScrollerMouseDown"
      >
        <div
          class="photo-grid__spacer relative w-full"
          :style="{ height: totalHeight + 'px', '--cell-size': cellSize + 'px' }"
        >
          <button
            v-for="cell in visibleCells"
            :key="cell.photo.Path"
            class="photo-grid__cell"
            :class="{
              'photo-grid__cell--focused': cell.index === focused,
              'photo-grid__cell--selected': selectedPaths.has(cell.photo.Path),
            }"
            :style="{ transform: `translate(${cell.x}px, ${cell.y}px)` }"
            :data-index="cell.index"
            @click="onCellClick(cell.index, $event)"
            @dblclick.stop="onCellDblClick(cell.index)"
            @contextmenu="openCtxMenu(cell.index, $event)"
            @focus="focused = cell.index"
          >
            <img
              class="h-full w-full object-cover"
              :src="imgSrc(cell.photo.Path)"
              :alt="cell.photo.Name"
              loading="lazy"
              decoding="async"
            />
            <span
              v-if="selectedPaths.has(cell.photo.Path)"
              class="photo-grid__select-badge"
              aria-label="Selected"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
                <path d="M3 7.5 L6 10.5 L11 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </span>
            <span
              v-if="isVideo(cell.photo)"
              class="photo-grid__video-badge"
              aria-label="Video"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
                <path d="M4 3 L11 7 L4 11 Z" fill="currentColor" />
              </svg>
            </span>
          </button>
          <div
            v-if="marqueeBox"
            class="pointer-events-none absolute rounded border border-primary bg-primary/15"
            :style="{
              left: marqueeBox.left + 'px',
              top: marqueeBox.top + 'px',
              width: marqueeBox.width + 'px',
              height: marqueeBox.height + 'px',
            }"
            aria-hidden="true"
          />
        </div>
      </div>

      <SelectionSummary
        v-if="multiSelection.length >= 2"
        :photos="multiSelection"
        @clear="clearAllSelection"
      />
      <PhotoViewer
        v-else
        ref="viewerRef"
        :photo="viewerPhoto"
        @close="closeViewer"
        @prev="openAt(viewerIndex - 1)"
        @next="openAt(viewerIndex + 1)"
      />
    </div>

    <Teleport to="body">
      <FolderPicker
        v-if="pickerOpen"
        :initial-path="folder"
        @choose="onPickerChoose"
        @close="closePicker"
      />
    </Teleport>

    <Teleport to="body">
      <div
        v-if="ctxOpen"
        class="fixed inset-0 z-[999]"
        @click="closeCtxMenu"
        @contextmenu.prevent="closeCtxMenu"
      />
      <ul
        v-if="ctxOpen"
        class="menu menu-sm bg-base-200 rounded-box fixed z-[1000] w-56 p-2 shadow-xl"
        :style="{ left: ctxX + 'px', top: ctxY + 'px' }"
        role="menu"
        @click.stop
      >
        <li v-if="ctxTargets.length > 1" class="menu-title">
          <span>{{ ctxTargets.length }} photos selected</span>
        </li>
        <li :class="ctxSingle ? '' : 'disabled'">
          <button type="button" role="menuitem" :disabled="!ctxSingle" @click="ctxPreview">Preview</button>
        </li>
        <li :class="(ctxSingle && !ctxIsVideo) ? '' : 'disabled'">
          <button type="button" role="menuitem" :disabled="!ctxSingle || ctxIsVideo" @click="ctxCopy">Copy image</button>
        </li>
        <li><button type="button" role="menuitem" @click="ctxMove">Move…</button></li>
        <li><button type="button" role="menuitem" class="text-error" @click="ctxDelete">Delete</button></li>
        <li v-if="ctxCopyError" class="px-2 pt-1">
          <span class="text-error text-xs" role="alert">{{ ctxCopyError }}</span>
        </li>
      </ul>
    </Teleport>
  </section>
</template>

<!--
  The cell layout uses absolute positioning + CSS-var-driven sizing so
  virtualization can place thousands of thumbnails by transform alone.
  These two selectors can't be expressed as Tailwind utilities because
  the size is dynamic (driven by the user's cell-size slider) and the
  focus/selection rings need to overlay the image via ::after.
-->
<style>
.photo-grid__cell {
  position: absolute;
  top: 0;
  left: 0;
  width: calc(var(--cell-size, 280px) - 6px);
  height: calc(var(--cell-size, 280px) - 6px);
  margin: 3px;
  padding: 0;
  border: none;
  border-radius: 4px;
  overflow: hidden;
  cursor: pointer;
  will-change: transform;
  background: var(--color-base-300);
}
.photo-grid__cell::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  box-shadow: inset 0 0 0 1px color-mix(in oklch, var(--color-base-content) 12%, transparent);
  pointer-events: none;
  transition: box-shadow 120ms;
}
.photo-grid__cell:hover::after {
  box-shadow: inset 0 0 0 1px color-mix(in oklch, var(--color-base-content) 25%, transparent);
}
.photo-grid__cell--focused::after {
  box-shadow: inset 0 0 0 2px color-mix(in oklch, var(--color-primary) 60%, transparent);
}
.photo-grid__cell--selected::after {
  box-shadow:
    inset 0 0 0 4px var(--color-primary),
    inset 0 0 0 5px color-mix(in oklch, var(--color-base-100) 90%, transparent);
}
.photo-grid__select-badge {
  position: absolute;
  top: 6px;
  left: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 9999px;
  background: var(--color-primary);
  color: var(--color-primary-content);
  pointer-events: none;
  box-shadow:
    0 0 0 2px var(--color-base-100),
    0 1px 3px rgba(0, 0, 0, 0.4);
}
.photo-grid__video-badge {
  position: absolute;
  right: 6px;
  bottom: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 9999px;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  pointer-events: none;
}
</style>
