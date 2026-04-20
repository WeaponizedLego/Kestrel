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
import { apiGet, apiPost, friendlyError } from '../transport/api'
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
import type { Photo } from '../types'

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

// Sort + search controls. Server does the heavy lifting (see
// CLAUDE.md: "No JS-side sorting or filtering"); the grid just
// composes query params and debounces the search typing.
const sortKey = ref<'name' | 'date' | 'size'>('name')
const sortOrder = ref<'asc' | 'desc'>('asc')
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
  const viewerWasClosed = viewerIndex.value < 0
  // When the viewer panel first mounts, the scroller shrinks and the
  // column count drops. updateMetrics' default behavior is to pin the
  // top-visible photo, which can push the photo the user just clicked
  // below the fold. Flag the next metrics tick to anchor on the opened
  // photo instead.
  if (viewerWasClosed) pendingScrollToIndex = index
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
  unsubThumb?.()
  unsubLibrary?.()
  unsubScanStart?.()
  unsubScanDone?.()
})

// loadPhotos runs on any server-shaping param change. Running it on
// mount too restores the view after a restart — the library is
// already persisted, so there's nothing to wait on.
watch([sortKey, sortOrder, searchDebounced, searchMode, selectedFolder], () => {
  loadPhotos()
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
  <section class="photo-grid">
    <header class="photo-grid__bar">
      <div class="photo-grid__row photo-grid__row--primary">
        <div class="photo-grid__field-wrap">
          <svg class="photo-grid__field-icon" width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
            <path d="M1.5 3C1.5 2.4 1.9 2 2.5 2H4.5L5.5 3H9.5C10.1 3 10.5 3.4 10.5 4V9C10.5 9.6 10.1 10 9.5 10H2.5C1.9 10 1.5 9.6 1.5 9V3Z" stroke="currentColor" stroke-width="1" stroke-linejoin="round"/>
          </svg>
          <input
            v-model="folder"
            class="photo-grid__field"
            type="text"
            placeholder="/absolute/path/to/photos"
            @keydown.enter="scan"
          />
        </div>
        <button
          class="photo-grid__btn photo-grid__btn--ghost"
          type="button"
          :disabled="scanning"
          @click="openPicker"
        >
          Browse
        </button>
        <button
          v-if="!scanning"
          class="photo-grid__btn photo-grid__btn--primary"
          type="button"
          :disabled="!folder"
          @click="scan"
        >
          Scan
        </button>
        <button
          v-else
          class="photo-grid__btn photo-grid__btn--danger"
          type="button"
          :disabled="cancelling"
          @click="cancelScan"
        >
          {{ cancelling ? 'Cancelling…' : 'Cancel' }}
        </button>
        <button
          class="photo-grid__btn photo-grid__btn--ghost"
          type="button"
          :disabled="resyncing || scanning"
          title="Check disk for deleted photos and drop missing entries"
          @click="runResync"
        >
          {{ resyncing ? 'Syncing…' : 'Re-sync' }}
        </button>
      </div>

      <div class="photo-grid__row photo-grid__row--filters">
        <TagInput
          v-model="searchTokens"
          class="photo-grid__search"
          placeholder="Search name or tag…"
          aria-label="Search photos by name or tag"
        />
        <div class="photo-grid__segmented" role="group" aria-label="Match mode">
          <button
            type="button"
            class="photo-grid__seg-btn"
            :class="{ 'photo-grid__seg-btn--active': searchMode === 'all' }"
            @click="searchMode = 'all'"
          >All</button>
          <button
            type="button"
            class="photo-grid__seg-btn"
            :class="{ 'photo-grid__seg-btn--active': searchMode === 'any' }"
            @click="searchMode = 'any'"
          >Any</button>
        </div>
        <div class="photo-grid__select-wrap">
          <select class="photo-grid__select" v-model="sortKey" aria-label="Sort by">
            <option value="name">Name</option>
            <option value="date">Date taken</option>
            <option value="size">Size</option>
          </select>
          <svg class="photo-grid__select-chev" width="8" height="8" viewBox="0 0 8 8" fill="none" aria-hidden="true">
            <path d="M1.5 3L4 5.5L6.5 3" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
        <button
          type="button"
          class="photo-grid__order"
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

    <div class="photo-grid__content">
      <p v-if="error" class="photo-grid__error" role="alert">{{ error }}</p>

      <div
        v-else-if="loading && photos.length === 0"
        class="photo-grid__skeleton"
        aria-label="Loading photos"
        aria-busy="true"
      >
        <div v-for="n in 18" :key="n" class="photo-grid__skeleton-cell" />
      </div>

      <div
        v-else-if="!loading && photos.length === 0"
        class="photo-grid__empty"
      >
        <svg class="photo-grid__empty-icon" width="48" height="48" viewBox="0 0 48 48" fill="none" aria-hidden="true">
          <rect x="6" y="10" width="36" height="28" rx="3" stroke="currentColor" stroke-width="1.5"/>
          <circle cx="17" cy="21" r="3" stroke="currentColor" stroke-width="1.5"/>
          <path d="M6 32L18 22L28 30L36 24L42 28" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>
        </svg>
        <h3 class="photo-grid__empty-title">
          {{ searchDebounced ? 'No matches' : 'No photos yet' }}
        </h3>
        <p class="photo-grid__empty-text">
          {{ searchDebounced
              ? `Nothing matches ${searchMode === 'all' ? 'all' : 'any'} of: ${searchTokens.join(', ')}.`
              : 'Point Kestrel at a folder and scan to build your library.' }}
        </p>
      </div>

      <div
        v-else
        ref="scroller"
        class="photo-grid__scroller"
        tabindex="0"
        @scroll.passive="onScroll"
        @mousedown="onScrollerMouseDown"
      >
      <div
        class="photo-grid__spacer"
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
          @focus="focused = cell.index"
        >
          <img
            class="photo-grid__thumb"
            :src="imgSrc(cell.photo.Path)"
            :alt="cell.photo.Name"
            loading="lazy"
            decoding="async"
          />
        </button>
        <div
          v-if="marqueeBox"
          class="photo-grid__marquee"
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
        v-else-if="viewerPhoto"
        :photo="viewerPhoto"
        @close="closeViewer"
        @prev="openAt(viewerIndex - 1)"
        @next="openAt(viewerIndex + 1)"
      />
      <aside v-else class="photo-grid__panel-empty" aria-hidden="true">
        <p>Select a photo to see its details.</p>
      </aside>
    </div>

    <Teleport to="body">
      <FolderPicker
        v-if="pickerOpen"
        :initial-path="folder"
        @choose="onPickerChoose"
        @close="closePicker"
      />
    </Teleport>
  </section>
</template>

<style scoped>
.photo-grid {
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  gap: var(--space-5);
}

/* ── Bar (two rows, dense) ──────────────────────────────────── */
.photo-grid__bar {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  flex-shrink: 0;
}
.photo-grid__row {
  display: flex;
  gap: var(--space-3);
  align-items: center;
  flex-wrap: wrap;
}

.photo-grid__field-wrap {
  flex: 1;
  min-width: 280px;
  position: relative;
  display: flex;
  align-items: center;
}
.photo-grid__field-icon {
  position: absolute;
  left: var(--space-4);
  color: var(--text-muted);
  pointer-events: none;
}
.photo-grid__field {
  width: 100%;
  background: var(--surface-raised);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: 0 var(--space-4) 0 28px;
  height: 28px;
  font: var(--fw-regular) var(--fs-small) / 1.2 var(--font-mono);
  letter-spacing: 0;
  transition: background var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out);
}
.photo-grid__field::placeholder {
  color: var(--text-muted);
  font-family: var(--font-mono);
}
.photo-grid__field:hover { border-color: var(--border-strong); }
.photo-grid__field:focus {
  border-color: var(--accent);
  background: var(--surface-hover);
  outline: none;
}

.photo-grid__btn {
  height: 28px;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: 0 var(--space-5);
  font: var(--fw-medium) var(--fs-small) / 1 var(--font-sans);
  letter-spacing: var(--tracking-tight);
  cursor: pointer;
  flex-shrink: 0;
  transition: background var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out),
              color var(--dur-fast) var(--ease-out);
}
.photo-grid__btn--ghost {
  background: transparent;
  color: var(--text-secondary);
}
.photo-grid__btn--ghost:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--surface-hover);
  border-color: var(--border-strong);
}
.photo-grid__btn--primary {
  background: var(--accent);
  color: #0A0A0B;
  border-color: var(--accent);
}
.photo-grid__btn--primary:hover:not(:disabled) {
  background: var(--accent-hover);
  border-color: var(--accent-hover);
}
.photo-grid__btn--danger {
  background: transparent;
  color: var(--danger);
  border-color: var(--danger);
}
.photo-grid__btn--danger:hover:not(:disabled) {
  background: var(--danger-wash);
}
.photo-grid__btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

/* ── Filters row ────────────────────────────────────────────── */
.photo-grid__search {
  flex: 1;
  min-width: 240px;
  min-height: 28px;
  height: 28px;
  padding: var(--space-1) var(--space-3);
}

.photo-grid__segmented {
  display: inline-flex;
  background: var(--surface-raised);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: 2px;
  height: 28px;
  flex-shrink: 0;
}
.photo-grid__seg-btn {
  background: transparent;
  color: var(--text-muted);
  border: none;
  border-radius: var(--radius-xs);
  padding: 0 var(--space-4);
  height: 100%;
  font: var(--fw-semibold) var(--fs-micro) / 1 var(--font-sans);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  cursor: pointer;
  transition: background var(--dur-fast) var(--ease-out),
              color var(--dur-fast) var(--ease-out);
}
.photo-grid__seg-btn:hover { color: var(--text-primary); }
.photo-grid__seg-btn--active {
  background: var(--accent-wash);
  color: var(--accent);
}

.photo-grid__select-wrap {
  position: relative;
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
}
.photo-grid__select {
  background: var(--surface-raised);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: 0 28px 0 var(--space-4);
  height: 28px;
  font: var(--fw-medium) var(--fs-small) / 1 var(--font-sans);
  cursor: pointer;
  -webkit-appearance: none;
  appearance: none;
  transition: border-color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.photo-grid__select:hover {
  border-color: var(--border-strong);
  background: var(--surface-hover);
}
.photo-grid__select:focus { outline: none; border-color: var(--accent); }
.photo-grid__select-chev {
  position: absolute;
  right: var(--space-3);
  color: var(--text-muted);
  pointer-events: none;
}

.photo-grid__order {
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--surface-raised);
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  cursor: pointer;
  flex-shrink: 0;
  transition: color var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.photo-grid__order:hover {
  color: var(--text-primary);
  border-color: var(--border-strong);
  background: var(--surface-hover);
}

/* ── States ─────────────────────────────────────────────────── */
.photo-grid__error {
  color: var(--danger);
  background: var(--danger-wash);
  padding: var(--space-4) var(--space-5);
  border-radius: var(--radius-md);
  border: 1px solid var(--danger);
  font-size: var(--fs-small);
}
.photo-grid__empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  color: var(--text-muted);
  text-align: center;
  padding: var(--space-9);
}
.photo-grid__empty-icon {
  color: var(--text-faint);
  margin-bottom: var(--space-2);
}
.photo-grid__empty-title {
  margin: 0;
  font: var(--fw-semibold) var(--fs-heading) / var(--lh-tight) var(--font-sans);
  letter-spacing: var(--tracking-tight);
  color: var(--text-secondary);
}
.photo-grid__empty-text {
  margin: 0;
  max-width: 340px;
  font-size: var(--fs-small);
  line-height: var(--lh-body);
}

.photo-grid__skeleton {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 2px;
  padding: 0;
}
.photo-grid__skeleton-cell {
  aspect-ratio: 1 / 1;
  border-radius: var(--radius-xs);
  background: var(--surface-raised);
  animation: photo-grid-pulse 1.6s ease-in-out infinite;
}
@keyframes photo-grid-pulse {
  0%, 100% { opacity: 0.55; }
  50%      { opacity: 1; }
}

/* ── Main content ───────────────────────────────────────────── */
.photo-grid__content {
  flex: 1;
  display: flex;
  gap: var(--space-5);
  min-height: 0;
}

.photo-grid__panel-empty {
  display: none; /* keep DOM but invisible — density first */
}

.photo-grid__scroller {
  flex: 1;
  min-width: 0;
  overflow: auto;
  background: var(--surface-inset);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  outline: none;
}
.photo-grid__spacer {
  position: relative;
  width: 100%;
}

.photo-grid__cell {
  position: absolute;
  top: 0;
  left: 0;
  width: calc(var(--cell-size, 280px) - 6px);
  height: calc(var(--cell-size, 280px) - 6px);
  margin: 3px;
  padding: 0;
  border: none;
  border-radius: var(--radius-xs);
  background: var(--surface-raised);
  overflow: hidden;
  cursor: pointer;
  will-change: transform;
  transition: transform var(--dur-fast) var(--ease-out),
              box-shadow var(--dur-fast) var(--ease-out);
}
.photo-grid__cell::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  box-shadow: inset 0 0 0 1px var(--border-subtle);
  pointer-events: none;
  transition: box-shadow var(--dur-fast) var(--ease-out);
}
.photo-grid__cell:hover::after {
  box-shadow: inset 0 0 0 1px var(--border-strong);
}
.photo-grid__cell:hover {
  z-index: 2;
}
.photo-grid__cell--focused::after {
  box-shadow: inset 0 0 0 2px var(--accent-muted);
}
.photo-grid__cell--selected::after {
  box-shadow: inset 0 0 0 2px var(--accent);
}
.photo-grid__cell--selected {
  z-index: 3;
}
.photo-grid__cell--selected::before {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background: var(--accent-wash);
  pointer-events: none;
  z-index: 1;
}

.photo-grid__thumb {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.photo-grid__marquee {
  position: absolute;
  pointer-events: none;
  background: var(--accent-wash);
  border: 1px solid var(--accent);
  border-radius: var(--radius-xs);
}
</style>
