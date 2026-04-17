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
import { selectedFolder } from '../transport/selection'
import type { Photo } from '../types'

// Lazy-loaded so the viewer JS/CSS only downloads when a user opens
// a photo — matches TASKS.md Phase 8 "PhotoViewer lazy-loaded via
// dynamic import".
const PhotoViewer = defineAsyncComponent(() => import('./PhotoViewer.vue'))
const FolderPicker = defineAsyncComponent(() => import('./FolderPicker.vue'))

const cellSize = 280 // 256 thumb + 24 padding; matches docs/ui-design
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
  return Math.max(1, Math.floor(viewportWidth.value / cellSize))
})
const totalRows = computed(() => Math.ceil(photos.value.length / columns.value))
const totalHeight = computed(() => totalRows.value * cellSize)

interface Cell {
  photo: Photo
  index: number
  x: number
  y: number
}

const visibleCells = computed<Cell[]>(() => {
  if (!photos.value.length || !viewportHeight.value) return []
  const cols = columns.value
  const firstRow = Math.max(0, Math.floor(scrollTop.value / cellSize) - overscanRows)
  const lastRow = Math.min(
    totalRows.value,
    Math.ceil((scrollTop.value + viewportHeight.value) / cellSize) + overscanRows,
  )
  const cells: Cell[] = []
  for (let row = firstRow; row < lastRow; row++) {
    for (let col = 0; col < cols; col++) {
      const index = row * cols + col
      if (index >= photos.value.length) break
      cells.push({
        photo: photos.value[index],
        index,
        x: col * cellSize,
        y: row * cellSize,
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
const search = ref('')
const searchDebounced = ref('')
const searchDebounceMs = 250

let searchTimer: number | null = null
watch(search, (value) => {
  if (searchTimer !== null) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    searchDebounced.value = value.trim()
  }, searchDebounceMs)
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
  try {
    const q = new URLSearchParams({ sort: sortKey.value, order: sortOrder.value })
    if (searchDebounced.value) q.set('q', searchDebounced.value)
    if (selectedFolder.value) q.set('folder', selectedFolder.value)
    photos.value = await apiGet<Photo[]>(`/api/photos?${q.toString()}`)
    focused.value = photos.value.length ? 0 : -1
  } catch (err) {
    error.value = friendlyError(err)
    photos.value = []
  } finally {
    loading.value = false
  }
}

function updateMetrics() {
  const el = scroller.value
  if (!el) return
  scrollTop.value = el.scrollTop
  viewportHeight.value = el.clientHeight
  viewportWidth.value = el.clientWidth
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
  viewerIndex.value = index
  focused.value = index
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
}

function ensureVisible(index: number) {
  const el = scroller.value
  if (!el) return
  const row = Math.floor(index / columns.value)
  const top = row * cellSize
  const bottom = top + cellSize
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
watch([sortKey, sortOrder, searchDebounced, selectedFolder], () => {
  loadPhotos()
})
</script>

<template>
  <section class="photo-grid">
    <header class="photo-grid__bar">
      <input
        v-model="folder"
        class="photo-grid__field"
        type="text"
        placeholder="/absolute/path/to/photos"
        @keydown.enter="scan"
      />
      <button
        class="photo-grid__btn photo-grid__btn--secondary"
        type="button"
        :disabled="scanning"
        @click="openPicker"
      >
        Browse…
      </button>
      <button
        v-if="!scanning"
        class="photo-grid__btn"
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
        {{ cancelling ? 'Cancelling…' : 'Cancel scan' }}
      </button>
      <select class="photo-grid__select" v-model="sortKey">
        <option value="name">Name</option>
        <option value="date">Date taken</option>
        <option value="size">Size</option>
      </select>
      <select class="photo-grid__select" v-model="sortOrder">
        <option value="asc">Asc</option>
        <option value="desc">Desc</option>
      </select>
      <input
        v-model="search"
        class="photo-grid__search"
        type="search"
        placeholder="Search by name…"
        aria-label="Search photos by name"
      />
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

      <p
        v-else-if="!loading && photos.length === 0"
        class="photo-grid__empty"
      >
        {{ searchDebounced
            ? `No photos match “${searchDebounced}”.`
            : 'No photos yet — point Kestrel at a folder and scan.' }}
      </p>

      <div
        v-else
        ref="scroller"
        class="photo-grid__scroller"
        tabindex="0"
        @scroll.passive="onScroll"
      >
      <div
        class="photo-grid__spacer"
        :style="{ height: totalHeight + 'px' }"
      >
        <button
          v-for="cell in visibleCells"
          :key="cell.photo.Path"
          class="photo-grid__cell"
          :class="{ 'photo-grid__cell--focused': cell.index === focused }"
          :style="{ transform: `translate(${cell.x}px, ${cell.y}px)` }"
          :data-index="cell.index"
          @click="openAt(cell.index)"
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
      </div>
    </div>

      <PhotoViewer
        v-if="viewerPhoto"
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
  </section>
</template>

<style scoped>
.photo-grid {
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.photo-grid__bar {
  display: flex;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
  flex-wrap: wrap;
}
.photo-grid__field {
  flex: 1;
  min-width: 280px;
  background: var(--surface-inset);
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4);
  box-shadow: var(--elev-inset);
  font: var(--fw-regular) var(--fs-default) / 1.2 var(--font-sans);
}
.photo-grid__field:focus {
  border-color: var(--accent);
  outline: none;
}
.photo-grid__btn {
  background: var(--accent);
  color: #fff;
  border: none;
  border-radius: var(--radius-full);
  padding: var(--space-3) var(--space-6);
  font: var(--fw-medium) var(--fs-default) / 1 var(--font-sans);
  box-shadow: var(--elev-raised);
  cursor: pointer;
}
.photo-grid__btn:hover:not(:disabled) { background: var(--accent-hover); }
.photo-grid__btn--secondary {
  background: transparent;
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  box-shadow: none;
}
.photo-grid__btn--secondary:hover:not(:disabled) {
  background: var(--surface-inset);
  border-color: var(--accent);
}
.photo-grid__btn--danger { background: var(--danger); }
.photo-grid__btn--danger:hover:not(:disabled) { background: var(--danger); filter: brightness(1.1); }
.photo-grid__btn:disabled {
  background: #4A3A30;
  color: var(--text-muted);
  box-shadow: none;
  cursor: not-allowed;
}
.photo-grid__select,
.photo-grid__search {
  background: var(--surface-inset);
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  font: inherit;
}
.photo-grid__search { min-width: 220px; }
.photo-grid__search:focus,
.photo-grid__select:focus { border-color: var(--accent); outline: none; }

.photo-grid__error {
  color: var(--danger);
  background: var(--surface-inset);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-md);
  border: var(--border-thin) solid var(--danger);
}
.photo-grid__empty {
  color: var(--text-muted);
  text-align: center;
  padding: var(--space-6);
}

.photo-grid__skeleton {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(256px, 1fr));
  gap: var(--space-4);
  padding: var(--space-4);
  background: var(--surface-inset);
  box-shadow: var(--elev-inset);
  border-radius: var(--radius-md);
}
.photo-grid__skeleton-cell {
  aspect-ratio: 1 / 1;
  border-radius: var(--radius-md);
  background: linear-gradient(
    90deg,
    var(--surface-raised) 0%,
    var(--surface-inset) 50%,
    var(--surface-raised) 100%
  );
  background-size: 200% 100%;
  animation: photo-grid-shimmer 1.4s ease-in-out infinite;
}
@keyframes photo-grid-shimmer {
  0%   { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

.photo-grid__content {
  flex: 1;
  display: flex;
  gap: var(--space-4);
  min-height: 0;
}

.photo-grid__scroller {
  flex: 1;
  min-width: 0;
  overflow: auto;
  background: var(--surface-inset);
  box-shadow: var(--elev-inset);
  border-radius: var(--radius-md);
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
  width: 256px;
  height: 256px;
  margin: 12px;
  padding: 0;
  border: 2px solid transparent;
  border-radius: var(--radius-md);
  background: var(--surface-raised);
  overflow: hidden;
  cursor: pointer;
  will-change: transform;
}
.photo-grid__cell:hover { border-color: var(--border-subtle); }
.photo-grid__cell--focused { border-color: var(--accent); }
.photo-grid__thumb {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
</style>
