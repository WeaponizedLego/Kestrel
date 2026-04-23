<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed, reactive } from 'vue'
import { apiGet, apiPost, friendlyError } from '../transport/api'
import { onEvent } from '../transport/events'
import { thumbSrc } from '../transport/thumbs'
import { onOpenTaggingQueue } from '../transport/tagging'
import TagInput from '../components/TagInput.vue'
import type { TaggingProgress, UntaggedFolder } from '../types'

const open = ref(false)
const folders = ref<UntaggedFolder[]>([])
const progress = ref<TaggingProgress | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

const collapsed = reactive(new Map<string, boolean>())
const selected = reactive(new Set<string>())

const pendingTags = ref<string[]>([])
const applying = ref(false)

const totalUntaggedShown = computed(() =>
  folders.value.reduce((sum, f) => sum + f.count, 0),
)

const progressPct = computed(() => {
  const p = progress.value
  if (!p || p.total === 0) return 0
  return Math.round((p.tagged / p.total) * 100)
})

async function refresh(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const [u, prog] = await Promise.all([
      apiGet<{ folders: UntaggedFolder[]; total: number }>('/api/untagged'),
      apiGet<TaggingProgress>('/api/tagging/progress'),
    ])
    folders.value = u.folders ?? []
    progress.value = prog
    const alive = new Set<string>()
    for (const f of folders.value) for (const p of f.photos) alive.add(p.path)
    for (const path of [...selected]) if (!alive.has(path)) selected.delete(path)
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    loading.value = false
  }
}

function toggleCollapsed(folder: string): void { collapsed.set(folder, !collapsed.get(folder)) }
function isCollapsed(folder: string): boolean { return collapsed.get(folder) === true }

function togglePhoto(path: string): void {
  if (selected.has(path)) selected.delete(path)
  else selected.add(path)
}

function selectAllInFolder(folder: UntaggedFolder): void {
  const allSelected = folder.photos.every((p) => selected.has(p.path))
  if (allSelected) for (const p of folder.photos) selected.delete(p.path)
  else for (const p of folder.photos) selected.add(p.path)
}

function folderSelectionState(folder: UntaggedFolder): 'none' | 'some' | 'all' {
  let count = 0
  for (const p of folder.photos) if (selected.has(p.path)) count++
  if (count === 0) return 'none'
  if (count === folder.photos.length) return 'all'
  return 'some'
}

async function applyTags(): Promise<void> {
  const tags = pendingTags.value
  if (tags.length === 0 || selected.size === 0 || applying.value) return
  applying.value = true
  error.value = null
  const members = [...selected]
  try {
    await apiPost<{ updated: number }>('/api/tagging/apply', { members, tags })
    pendingTags.value = []
    for (const p of members) selected.delete(p)
    const tagged = new Set(members)
    folders.value = folders.value
      .map((f) => {
        const photos = f.photos.filter((p) => !tagged.has(p.path))
        return { ...f, photos, count: photos.length }
      })
      .filter((f) => f.photos.length > 0)
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    applying.value = false
  }
}

function clearSelection(): void { selected.clear() }
function close(): void { open.value = false }

function onKey(e: KeyboardEvent): void {
  if (!open.value) return
  if (e.key === 'Escape') close()
}

let disposeOpen: (() => void) | null = null
let disposeLibrary: (() => void) | null = null

onMounted(() => {
  disposeOpen = onOpenTaggingQueue(async () => {
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
  <div v-if="open" class="modal modal-open" role="dialog" aria-label="Tagging Queue">
    <div class="modal-box flex flex-col p-0 w-full max-w-6xl max-h-[92vh]">
      <div class="flex items-center gap-3 border-b border-base-300 px-4 py-3">
        <h2 class="m-0 text-lg font-semibold">Tagging Queue</h2>
        <button type="button" class="btn btn-ghost btn-sm btn-square ml-auto" @click="close" aria-label="Close">✕</button>
      </div>

      <p class="border-b border-base-300 px-4 py-2 text-xs text-base-content/70">
        Photos with no tags yet, grouped by folder. Select photos in a folder and apply one or more tags — work through a trip or shoot in one pass.
      </p>

      <div v-if="progress" class="flex flex-col gap-1 border-b border-base-300 px-4 py-2">
        <progress class="progress progress-primary h-1.5" :value="progressPct" max="100" />
        <div class="text-xs tabular-nums text-base-content/70">
          <strong>{{ progress.tagged.toLocaleString() }}</strong>
          /
          {{ progress.total.toLocaleString() }} tagged
          <span class="mx-1 text-base-content/40">·</span>
          {{ totalUntaggedShown.toLocaleString() }} untagged across
          {{ folders.length }} folder{{ folders.length === 1 ? '' : 's' }}
        </div>
      </div>

      <div v-if="error" class="alert alert-error alert-soft rounded-none text-xs" role="alert">{{ error }}</div>

      <div class="flex-1 min-h-0 overflow-y-auto">
        <div v-if="loading && folders.length === 0" class="p-4 text-sm text-base-content/60">Loading untagged photos…</div>
        <div v-else-if="!loading && folders.length === 0" class="p-4 text-sm text-base-content/60">Everything is tagged. Nice.</div>
        <ul v-else class="divide-y divide-base-300">
          <li v-for="f in folders" :key="f.folder">
            <div
              class="flex cursor-pointer items-center gap-2 px-4 py-2 hover:bg-base-200 select-none"
              @click="toggleCollapsed(f.folder)"
            >
              <span
                class="text-base-content/50 text-xs transition-transform w-3"
                :style="{ transform: !isCollapsed(f.folder) ? 'rotate(90deg)' : 'none' }"
              >▸</span>
              <span class="flex-1 truncate font-mono text-xs" :title="f.folder">{{ f.folder }}</span>
              <span class="badge badge-ghost badge-sm tabular-nums">{{ f.count }}</span>
              <button
                type="button"
                :class="[
                  'btn btn-xs',
                  folderSelectionState(f) === 'all' ? 'btn-primary' :
                  folderSelectionState(f) === 'some' ? 'btn-outline btn-primary' : 'btn-ghost',
                ]"
                @click.stop="selectAllInFolder(f)"
              >
                {{ folderSelectionState(f) === 'all' ? 'Clear' : 'Select all' }}
              </button>
            </div>
            <div
              v-if="!isCollapsed(f.folder)"
              class="grid gap-1 px-4 pb-3"
              style="grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));"
            >
              <button
                v-for="p in f.photos"
                :key="p.path"
                type="button"
                :class="[
                  'relative aspect-square overflow-hidden rounded border-2 p-0 bg-base-300',
                  selected.has(p.path) ? 'border-primary' : 'border-transparent hover:border-base-content/30',
                ]"
                @click="togglePhoto(p.path)"
                :title="p.name"
              >
                <img class="h-full w-full object-cover" :src="thumbSrc(p.path)" alt="" />
                <span
                  v-if="selected.has(p.path)"
                  class="badge badge-primary badge-sm absolute right-1 top-1"
                  aria-hidden="true"
                >✓</span>
              </button>
            </div>
          </li>
        </ul>
      </div>

      <div v-if="folders.length > 0" class="flex items-center gap-3 border-t border-base-300 px-4 py-3">
        <div class="flex items-center gap-1 text-sm tabular-nums min-w-28">
          <strong>{{ selected.size }}</strong> selected
          <button
            v-if="selected.size > 0"
            type="button"
            class="btn btn-xs btn-ghost"
            @click="clearSelection"
          >Clear</button>
        </div>
        <TagInput
          v-model="pendingTags"
          placeholder="Type a tag, press space to add"
          aria-label="Tags to apply"
          class="flex-1"
        />
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="applying || pendingTags.length === 0 || selected.size === 0"
          @click="applyTags"
        >
          {{ applying ? 'Applying…' : `Apply to ${selected.size || 0}` }}
        </button>
      </div>
    </div>
  </div>
</template>
