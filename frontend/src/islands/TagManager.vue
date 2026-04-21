<script setup lang="ts">
// TagManager is the vocabulary-management surface for user tags. It
// lists every distinct Photo.Tags value with counts, and lets the
// user rename a tag (fix a typo), merge one tag into another, delete
// a tag entirely, or click a tag to jump back to the grid filtered
// by it. AutoTags are intentionally excluded — they regenerate on
// every scan, so surfacing them here would be misleading.
//
// Architectural notes:
//   - Mounts full-viewport; hidden until openTagManager() fires the
//     window event (see transport/tagging.ts).
//   - All mutations go through /api/tags/{rename,merge,delete} and
//     rely on the auto-save ticker for persistence.
//   - "Filter in grid" writes to the shared requestedSearchTokens
//     ref; PhotoGrid watches that ref (transport/search.ts).

import { computed, onMounted, onUnmounted, ref } from 'vue'
import { apiGet, apiPost, friendlyError } from '../transport/api'
import { onEvent } from '../transport/events'
import { onOpenTagManager } from '../transport/tagging'
import { requestSearchTokens } from '../transport/search'

interface TagStat { name: string; count: number }

type Pending =
  | { kind: 'idle' }
  | { kind: 'rename'; from: string; to: string }
  | { kind: 'merge'; source: string; target: string }
  | { kind: 'confirm-delete'; name: string }

const open = ref(false)
const tags = ref<TagStat[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const status = ref<string | null>(null)
const filter = ref('')
const pending = ref<Pending>({ kind: 'idle' })

const filtered = computed<TagStat[]>(() => {
  const q = filter.value.trim().toLowerCase()
  if (q === '') return tags.value
  return tags.value.filter((t) => t.name.includes(q))
})

async function refresh(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    tags.value = await apiGet<TagStat[]>('/api/tags/list')
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    loading.value = false
  }
}

function close(): void {
  open.value = false
  pending.value = { kind: 'idle' }
  status.value = null
}

function startRename(name: string): void {
  pending.value = { kind: 'rename', from: name, to: name }
}
function startMerge(name: string): void {
  pending.value = { kind: 'merge', source: name, target: '' }
}
function startDelete(name: string): void {
  pending.value = { kind: 'confirm-delete', name }
}
function cancelPending(): void {
  pending.value = { kind: 'idle' }
}

async function commitRename(): Promise<void> {
  if (pending.value.kind !== 'rename') return
  const { from, to } = pending.value
  if (!to.trim() || to.trim().toLowerCase() === from.toLowerCase()) {
    cancelPending()
    return
  }
  try {
    const out = await apiPost<{ renamed: number; absorbed: number }>(
      '/api/tags/rename',
      { from, to: to.trim() },
    )
    status.value = describeCounts('Renamed', out.renamed, out.absorbed)
    pending.value = { kind: 'idle' }
    await refresh()
  } catch (err) {
    error.value = friendlyError(err)
  }
}

async function commitMerge(): Promise<void> {
  if (pending.value.kind !== 'merge') return
  const { source, target } = pending.value
  const trg = target.trim()
  if (!trg || trg.toLowerCase() === source.toLowerCase()) {
    cancelPending()
    return
  }
  try {
    const out = await apiPost<{ renamed: number; absorbed: number }>(
      '/api/tags/merge',
      { source, target: trg },
    )
    status.value = describeCounts('Merged', out.renamed, out.absorbed)
    pending.value = { kind: 'idle' }
    await refresh()
  } catch (err) {
    error.value = friendlyError(err)
  }
}

async function commitDelete(): Promise<void> {
  if (pending.value.kind !== 'confirm-delete') return
  const name = pending.value.name
  try {
    const out = await apiPost<{ affected: number }>('/api/tags/delete', { name })
    status.value = `Deleted "${name}" from ${out.affected} photo${out.affected === 1 ? '' : 's'}.`
    pending.value = { kind: 'idle' }
    await refresh()
  } catch (err) {
    error.value = friendlyError(err)
  }
}

function filterByTag(name: string): void {
  requestSearchTokens([name])
  close()
}

function describeCounts(verb: string, renamed: number, absorbed: number): string {
  if (renamed === 0 && absorbed === 0) return `${verb}, but no photos matched.`
  const parts: string[] = []
  if (renamed > 0) parts.push(`${renamed} photo${renamed === 1 ? '' : 's'}`)
  if (absorbed > 0) parts.push(`${absorbed} already had the target`)
  return `${verb}: ${parts.join(', ')}.`
}

function onKey(e: KeyboardEvent): void {
  if (!open.value) return
  if (e.key === 'Escape') {
    if (pending.value.kind !== 'idle') cancelPending()
    else close()
  }
}

let disposeOpen: (() => void) | null = null
let disposeLibrary: (() => void) | null = null

onMounted(() => {
  disposeOpen = onOpenTagManager(async () => {
    open.value = true
    await refresh()
  })
  disposeLibrary = onEvent('library:updated', () => {
    if (open.value) refresh()
  })
  window.addEventListener('keydown', onKey)
})

onUnmounted(() => {
  disposeOpen?.()
  disposeLibrary?.()
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div v-if="open" class="tm" role="dialog" aria-label="Tag manager">
    <div class="tm__backdrop" @click="close" />

    <section class="tm__panel">
      <header class="tm__header">
        <h2 class="tm__title">Tags</h2>
        <input
          v-model="filter"
          class="tm__filter"
          type="search"
          placeholder="Filter tags…"
          aria-label="Filter tags"
        />
        <button class="tm__close" @click="close" aria-label="Close">×</button>
      </header>

      <p v-if="status" class="tm__status">{{ status }}</p>
      <p v-if="error" class="tm__error">{{ error }}</p>

      <div class="tm__body">
        <div v-if="loading && tags.length === 0" class="tm__empty">
          Loading tags…
        </div>
        <div v-else-if="!loading && tags.length === 0" class="tm__empty">
          No user tags yet. Tag some photos to build your vocabulary.
        </div>
        <div v-else-if="filtered.length === 0" class="tm__empty">
          No tags match "{{ filter }}".
        </div>

        <ul v-else class="tm__list">
          <li
            v-for="t in filtered"
            :key="t.name"
            class="tm__row"
          >
            <!-- Rename mode -->
            <template
              v-if="pending.kind === 'rename' && pending.from === t.name"
            >
              <span class="tm__row-name tm__row-name--editing">{{ t.name }} →</span>
              <input
                class="tm__edit"
                v-model="pending.to"
                @keydown.enter="commitRename"
                @keydown.escape="cancelPending"
                autofocus
                aria-label="New tag name"
              />
              <span class="tm__row-count">{{ t.count }}</span>
              <div class="tm__row-actions">
                <button class="tm__btn tm__btn--primary" @click="commitRename">Save</button>
                <button class="tm__btn" @click="cancelPending">Cancel</button>
              </div>
            </template>

            <!-- Merge mode -->
            <template
              v-else-if="pending.kind === 'merge' && pending.source === t.name"
            >
              <span class="tm__row-name tm__row-name--editing">Merge {{ t.name }} into</span>
              <input
                class="tm__edit"
                v-model="pending.target"
                @keydown.enter="commitMerge"
                @keydown.escape="cancelPending"
                list="tm-tag-options"
                autofocus
                placeholder="target tag"
                aria-label="Target tag"
              />
              <span class="tm__row-count">{{ t.count }}</span>
              <div class="tm__row-actions">
                <button class="tm__btn tm__btn--primary" @click="commitMerge">Merge</button>
                <button class="tm__btn" @click="cancelPending">Cancel</button>
              </div>
            </template>

            <!-- Delete confirm mode -->
            <template
              v-else-if="pending.kind === 'confirm-delete' && pending.name === t.name"
            >
              <span class="tm__row-name tm__row-name--danger">
                Delete "{{ t.name }}" from all {{ t.count }} photos?
              </span>
              <div class="tm__row-actions">
                <button class="tm__btn tm__btn--danger" @click="commitDelete">
                  Confirm delete
                </button>
                <button class="tm__btn" @click="cancelPending">Cancel</button>
              </div>
            </template>

            <!-- Default row -->
            <template v-else>
              <button
                class="tm__row-name tm__row-name--link"
                @click="filterByTag(t.name)"
                :title="`Filter the grid by ${t.name}`"
              >
                {{ t.name }}
              </button>
              <span class="tm__row-count">
                {{ t.count }} photo{{ t.count === 1 ? '' : 's' }}
              </span>
              <div class="tm__row-actions">
                <button class="tm__btn" @click="startRename(t.name)">Rename</button>
                <button class="tm__btn" @click="startMerge(t.name)">Merge…</button>
                <button class="tm__btn tm__btn--danger-ghost" @click="startDelete(t.name)">
                  Delete
                </button>
              </div>
            </template>
          </li>
        </ul>

        <!-- Autocomplete options for the merge target input. Lists
             every tag except the source of the current merge, so the
             user can quickly pick an existing synonym. -->
        <datalist id="tm-tag-options">
          <option
            v-for="t in tags"
            :key="t.name"
            :value="t.name"
            v-show="pending.kind !== 'merge' || pending.source !== t.name"
          />
        </datalist>
      </div>
    </section>
  </div>
</template>

<style scoped>
.tm {
  position: fixed;
  inset: 0;
  z-index: 90;
  display: flex;
  align-items: stretch;
  justify-content: center;
}
.tm__backdrop {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(3px);
}
.tm__panel {
  position: relative;
  width: min(820px, 92vw);
  max-height: 84vh;
  margin: 6vh auto;
  background: var(--surface-bg);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-3, 10px);
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.45);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.tm__header {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.tm__title {
  margin: 0;
  font-size: var(--fs-medium);
  font-weight: var(--fw-semibold);
  color: var(--text-primary);
}
.tm__filter {
  flex: 1;
  max-width: 280px;
  height: 28px;
  padding: 0 var(--space-3);
  background: var(--surface-active);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-2, 4px);
  color: var(--text-primary);
  font-size: var(--fs-small);
}
.tm__filter:focus {
  outline: none;
  border-color: var(--accent);
}
.tm__close {
  margin-left: auto;
  background: transparent;
  border: none;
  color: var(--text-secondary);
  font-size: 24px;
  line-height: 1;
  cursor: pointer;
}
.tm__status {
  margin: 0;
  padding: var(--space-3) var(--space-5);
  background: rgba(100, 220, 140, 0.08);
  color: var(--accent);
  font-size: var(--fs-small);
  border-bottom: 1px solid var(--border-subtle);
}
.tm__error {
  margin: 0;
  padding: var(--space-3) var(--space-5);
  background: rgba(255, 70, 70, 0.1);
  color: #ff8080;
  font-size: var(--fs-small);
  border-bottom: 1px solid var(--border-subtle);
}
.tm__body {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}
.tm__empty {
  padding: var(--space-5);
  color: var(--text-muted);
  font-size: var(--fs-small);
}
.tm__list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.tm__row {
  display: grid;
  grid-template-columns: 1fr auto auto;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
  transition: background var(--dur-fast) var(--ease-out);
}
.tm__row:hover {
  background: var(--surface-active);
}
.tm__row-name {
  color: var(--text-primary);
  font-size: var(--fs-small);
  font-weight: var(--fw-medium);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tm__row-name--link {
  background: transparent;
  border: none;
  padding: 0;
  text-align: left;
  cursor: pointer;
  color: var(--text-primary);
  font: inherit;
  font-weight: var(--fw-medium);
}
.tm__row-name--link:hover {
  color: var(--accent);
  text-decoration: underline;
}
.tm__row-name--editing {
  color: var(--text-secondary);
}
.tm__row-name--danger {
  color: #ff8080;
}
.tm__row-count {
  color: var(--text-muted);
  font-size: var(--fs-micro);
  font-variant-numeric: tabular-nums;
  text-transform: uppercase;
  letter-spacing: var(--tracking-micro);
}
.tm__row-actions {
  display: inline-flex;
  gap: var(--space-2);
}
.tm__edit {
  height: 26px;
  padding: 0 var(--space-3);
  background: var(--surface-bg);
  border: 1px solid var(--accent);
  border-radius: var(--radius-2, 4px);
  color: var(--text-primary);
  font-size: var(--fs-small);
  min-width: 160px;
}
.tm__edit:focus {
  outline: none;
  box-shadow: 0 0 0 3px var(--accent-glow);
}
.tm__btn {
  height: 24px;
  padding: 0 var(--space-3);
  background: var(--surface-raised, rgba(255, 255, 255, 0.04));
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-2, 4px);
  color: var(--text-primary);
  font-size: var(--fs-micro);
  font-weight: var(--fw-medium);
  cursor: pointer;
  transition: background var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out);
}
.tm__btn:hover {
  background: var(--surface-active);
  border-color: var(--border-strong, var(--border-subtle));
}
.tm__btn:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px var(--accent-glow);
}
.tm__btn--primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--surface-bg);
}
.tm__btn--primary:hover {
  filter: brightness(1.1);
}
.tm__btn--danger {
  background: #cc3333;
  border-color: #cc3333;
  color: #fff;
}
.tm__btn--danger:hover {
  background: #dd4444;
}
.tm__btn--danger-ghost {
  color: #ff8080;
}
.tm__btn--danger-ghost:hover {
  background: rgba(255, 70, 70, 0.1);
  border-color: #ff8080;
}
</style>
