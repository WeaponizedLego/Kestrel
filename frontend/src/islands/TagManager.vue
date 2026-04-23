<script setup lang="ts">
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

function startRename(name: string): void { pending.value = { kind: 'rename', from: name, to: name } }
function startMerge(name: string): void { pending.value = { kind: 'merge', source: name, target: '' } }
function startDelete(name: string): void { pending.value = { kind: 'confirm-delete', name } }
function cancelPending(): void { pending.value = { kind: 'idle' } }

async function commitRename(): Promise<void> {
  if (pending.value.kind !== 'rename') return
  const { from, to } = pending.value
  if (!to.trim() || to.trim().toLowerCase() === from.toLowerCase()) { cancelPending(); return }
  try {
    const out = await apiPost<{ renamed: number; absorbed: number }>('/api/tags/rename', { from, to: to.trim() })
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
  if (!trg || trg.toLowerCase() === source.toLowerCase()) { cancelPending(); return }
  try {
    const out = await apiPost<{ renamed: number; absorbed: number }>('/api/tags/merge', { source, target: trg })
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
  disposeLibrary = onEvent('library:updated', () => { if (open.value) refresh() })
  window.addEventListener('keydown', onKey)
})

onUnmounted(() => {
  disposeOpen?.()
  disposeLibrary?.()
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div v-if="open" class="modal modal-open" role="dialog" aria-label="Tag manager">
    <div class="modal-box flex max-w-4xl flex-col p-0 max-h-[84vh]">
      <div class="flex items-center gap-3 border-b border-base-300 px-4 py-3">
        <h2 class="m-0 text-lg font-semibold">Tags</h2>
        <input
          v-model="filter"
          type="search"
          class="input input-sm input-bordered max-w-xs flex-1"
          placeholder="Filter tags…"
          aria-label="Filter tags"
        />
        <button type="button" class="btn btn-ghost btn-sm btn-square ml-auto" @click="close" aria-label="Close">✕</button>
      </div>

      <div v-if="status" class="alert alert-success alert-soft rounded-none text-xs">{{ status }}</div>
      <div v-if="error" class="alert alert-error alert-soft rounded-none text-xs" role="alert">{{ error }}</div>

      <div class="flex-1 overflow-y-auto">
        <div v-if="loading && tags.length === 0" class="p-4 text-sm text-base-content/60">Loading tags…</div>
        <div v-else-if="!loading && tags.length === 0" class="p-4 text-sm text-base-content/60">
          No user tags yet. Tag some photos to build your vocabulary.
        </div>
        <div v-else-if="filtered.length === 0" class="p-4 text-sm text-base-content/60">
          No tags match "{{ filter }}".
        </div>

        <ul v-else class="divide-y divide-base-300">
          <li
            v-for="t in filtered"
            :key="t.name"
            class="grid items-center gap-3 px-4 py-2 hover:bg-base-200"
            style="grid-template-columns: 1fr auto auto;"
          >
            <template v-if="pending.kind === 'rename' && pending.from === t.name">
              <span class="truncate text-sm text-base-content/70">{{ t.name }} →</span>
              <input
                class="input input-sm input-bordered input-primary min-w-40"
                v-model="pending.to"
                @keydown.enter="commitRename"
                @keydown.escape="cancelPending"
                autofocus
                aria-label="New tag name"
              />
              <div class="flex gap-2">
                <button type="button" class="btn btn-xs btn-primary" @click="commitRename">Save</button>
                <button type="button" class="btn btn-xs btn-ghost" @click="cancelPending">Cancel</button>
              </div>
            </template>

            <template v-else-if="pending.kind === 'merge' && pending.source === t.name">
              <span class="truncate text-sm text-base-content/70">Merge {{ t.name }} into</span>
              <input
                class="input input-sm input-bordered input-primary min-w-40"
                v-model="pending.target"
                @keydown.enter="commitMerge"
                @keydown.escape="cancelPending"
                list="tm-tag-options"
                autofocus
                placeholder="target tag"
                aria-label="Target tag"
              />
              <div class="flex gap-2">
                <button type="button" class="btn btn-xs btn-primary" @click="commitMerge">Merge</button>
                <button type="button" class="btn btn-xs btn-ghost" @click="cancelPending">Cancel</button>
              </div>
            </template>

            <template v-else-if="pending.kind === 'confirm-delete' && pending.name === t.name">
              <span class="truncate text-sm text-error">
                Delete "{{ t.name }}" from all {{ t.count }} photos?
              </span>
              <span />
              <div class="flex gap-2">
                <button type="button" class="btn btn-xs btn-error" @click="commitDelete">Confirm delete</button>
                <button type="button" class="btn btn-xs btn-ghost" @click="cancelPending">Cancel</button>
              </div>
            </template>

            <template v-else>
              <button
                type="button"
                class="truncate text-left font-medium hover:text-primary hover:underline"
                @click="filterByTag(t.name)"
                :title="`Filter the grid by ${t.name}`"
              >
                {{ t.name }}
              </button>
              <span class="text-[10px] uppercase tracking-wider tabular-nums text-base-content/50">
                {{ t.count }} photo{{ t.count === 1 ? '' : 's' }}
              </span>
              <div class="flex gap-1">
                <button type="button" class="btn btn-xs btn-ghost" @click="startRename(t.name)">Rename</button>
                <button type="button" class="btn btn-xs btn-ghost" @click="startMerge(t.name)">Merge…</button>
                <button type="button" class="btn btn-xs btn-ghost text-error" @click="startDelete(t.name)">Delete</button>
              </div>
            </template>
          </li>
        </ul>

        <datalist id="tm-tag-options">
          <option
            v-for="t in tags"
            :key="t.name"
            :value="t.name"
            v-show="pending.kind !== 'merge' || pending.source !== t.name"
          />
        </datalist>
      </div>
    </div>
  </div>
</template>
