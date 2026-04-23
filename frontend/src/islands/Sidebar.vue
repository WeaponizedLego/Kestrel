<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { apiGet, apiPost, friendlyError } from '../transport/api'
import { onEvent } from '../transport/events'
import { selectedFolder } from '../transport/selection'

const TagInput = defineAsyncComponent(() => import('../components/TagInput.vue'))

interface FolderNode {
  path: string
  parent: string
  name: string
  count: number
  total: number
}
interface Tree extends FolderNode {
  children: Tree[]
}

const nodes = ref<FolderNode[]>([])
const error = ref<string | null>(null)
const loading = ref(false)
const expanded = ref<Set<string>>(new Set())

async function load() {
  loading.value = true
  error.value = null
  try {
    nodes.value = await apiGet<FolderNode[]>('/api/folders')
    if (expanded.value.size === 0 && nodes.value.length > 0) {
      const next = new Set<string>()
      for (const n of nodes.value) if (n.parent === '') next.add(n.path)
      expanded.value = next
    }
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    loading.value = false
  }
}

const roots = computed<Tree[]>(() => {
  const byPath = new Map<string, Tree>()
  for (const n of nodes.value) byPath.set(n.path, { ...n, children: [] })
  const out: Tree[] = []
  for (const t of byPath.values()) {
    const parent = byPath.get(t.parent)
    if (parent) parent.children.push(t)
    else out.push(t)
  }
  const sortTree = (t: Tree) => {
    t.children.sort((a, b) => a.name.localeCompare(b.name))
    t.children.forEach(sortTree)
  }
  out.sort((a, b) => a.path.localeCompare(b.path))
  out.forEach(sortTree)
  return out
})

interface Row { node: Tree; depth: number }

const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const walk = (ts: Tree[], depth: number) => {
    for (const t of ts) {
      out.push({ node: t, depth })
      if (t.children.length > 0 && expanded.value.has(t.path)) {
        walk(t.children, depth + 1)
      }
    }
  }
  walk(roots.value, 0)
  return out
})

function toggle(path: string) {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

function select(path: string | null) {
  selectedFolder.value = path
}

let unsubUpdate: (() => void) | null = null
onMounted(() => {
  load()
  unsubUpdate = onEvent('library:updated', () => load())
  window.addEventListener('click', closeMenu)
  window.addEventListener('keydown', onEscape)
})
onBeforeUnmount(() => {
  unsubUpdate?.()
  window.removeEventListener('click', closeMenu)
  window.removeEventListener('keydown', onEscape)
})

const menuFolder = ref<string | null>(null)
const menuX = ref(0)
const menuY = ref(0)
const tagDraft = ref<string[]>([])
const tagError = ref<string | null>(null)
const tagBusy = ref(false)
const tagPopoverOpen = ref(false)
const tagInputRef = ref<InstanceType<typeof TagInput> | null>(null)
const removePopoverOpen = ref(false)
const removeBusy = ref(false)
const removeError = ref<string | null>(null)

function openMenu(e: MouseEvent, path: string) {
  e.preventDefault()
  menuFolder.value = path
  menuX.value = e.clientX
  menuY.value = e.clientY
  tagPopoverOpen.value = false
  removePopoverOpen.value = false
  removeError.value = null
}

function closeMenu() {
  menuFolder.value = null
  tagPopoverOpen.value = false
  removePopoverOpen.value = false
  removeError.value = null
}

function onEscape(e: KeyboardEvent) {
  if (e.key === 'Escape') closeMenu()
}

function startTagEntry() {
  tagDraft.value = []
  tagError.value = null
  tagPopoverOpen.value = true
  nextTick(() => tagInputRef.value?.focus())
}

function startRemoveConfirm() {
  removeError.value = null
  removePopoverOpen.value = true
}

async function applyRemove() {
  if (!menuFolder.value) { closeMenu(); return }
  removeBusy.value = true
  removeError.value = null
  try {
    await apiPost<{ removed: number }>('/api/folder/remove', { folder: menuFolder.value })
    closeMenu()
  } catch (err) {
    removeError.value = friendlyError(err)
  } finally {
    removeBusy.value = false
  }
}

async function applyTags() {
  if (!menuFolder.value || tagDraft.value.length === 0) { closeMenu(); return }
  tagBusy.value = true
  tagError.value = null
  try {
    await apiPost<{ updated: number }>('/api/folder-tags', {
      folder: menuFolder.value,
      tags: tagDraft.value,
    })
    closeMenu()
  } catch (err) {
    tagError.value = friendlyError(err)
  } finally {
    tagBusy.value = false
  }
}
</script>

<template>
  <nav class="flex h-full min-h-0 flex-col overflow-y-auto overflow-x-hidden p-2 text-sm">
    <div class="px-2 pb-2 text-xs font-semibold uppercase tracking-wider text-base-content/60">
      Library
    </div>

    <ul class="menu menu-sm w-full px-0">
      <li>
        <button
          type="button"
          :class="['justify-start', selectedFolder === null ? 'menu-active' : '']"
          @click="select(null)"
        >
          All photos
        </button>
      </li>
    </ul>

    <div v-if="error" role="alert" class="alert alert-error alert-soft mt-2 text-xs">{{ error }}</div>
    <p v-else-if="loading && nodes.length === 0" class="px-3 py-2 text-base-content/60">Loading…</p>
    <p v-else-if="nodes.length === 0" class="px-3 py-2 text-base-content/60">
      Scan a folder to see it here.
    </p>

    <ul v-else class="menu menu-sm w-full px-0 py-0">
      <li
        v-for="{ node, depth } in rows"
        :key="node.path"
        @contextmenu="openMenu($event, node.path)"
      >
        <div
          :class="['flex items-center gap-1 pr-2', selectedFolder === node.path ? 'menu-active' : '']"
          :style="{ paddingLeft: (depth * 12 + 4) + 'px' }"
        >
          <button
            type="button"
            :class="[
              'flex h-5 w-4 shrink-0 items-center justify-center rounded opacity-70 hover:opacity-100',
              node.children.length === 0 ? 'invisible' : '',
            ]"
            :aria-label="expanded.has(node.path) ? 'Collapse' : 'Expand'"
            @click.stop="toggle(node.path)"
          >
            <svg
              width="8" height="8" viewBox="0 0 8 8" fill="none" aria-hidden="true"
              :style="{
                transition: 'transform 120ms',
                transform: expanded.has(node.path) ? 'rotate(90deg)' : 'none',
              }"
            >
              <path d="M2.5 1.5L5.5 4L2.5 6.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </button>
          <button
            type="button"
            class="flex flex-1 items-center justify-between gap-2 truncate text-left"
            :title="node.path"
            @click="select(node.path)"
          >
            <span class="truncate">{{ node.name }}</span>
            <span class="font-mono text-[10px] tabular-nums text-base-content/50">{{ node.total }}</span>
          </button>
        </div>
      </li>
    </ul>

    <Teleport to="body">
      <ul
        v-if="menuFolder && !tagPopoverOpen && !removePopoverOpen"
        class="menu menu-sm bg-base-200 rounded-box fixed z-[1000] w-64 p-2 shadow-xl"
        :style="{ left: menuX + 'px', top: menuY + 'px' }"
        role="menu"
        @click.stop
      >
        <li><button type="button" role="menuitem" @click="startTagEntry">Add tag to all photos in folder…</button></li>
        <li><button type="button" role="menuitem" class="text-error" @click="startRemoveConfirm">Remove folder from index…</button></li>
      </ul>

      <div
        v-if="menuFolder && removePopoverOpen"
        class="card bg-base-200 fixed z-[1000] w-80 shadow-2xl"
        :style="{ left: menuX + 'px', top: menuY + 'px' }"
        role="dialog"
        aria-label="Remove folder from index"
        @click.stop
        @keydown.enter="applyRemove"
      >
        <div class="card-body gap-3 p-4">
          <p class="truncate text-sm" :title="menuFolder">
            Remove <span class="font-mono text-xs">{{ menuFolder }}</span> from the library?
          </p>
          <p class="text-xs text-base-content/60">
            Files on disk are not deleted. Re-scanning the folder will bring them back.
          </p>
          <div v-if="removeError" class="alert alert-error alert-soft text-xs" role="alert">{{ removeError }}</div>
          <div class="card-actions justify-end">
            <button type="button" class="btn btn-sm btn-ghost" @click="closeMenu" :disabled="removeBusy">Cancel</button>
            <button type="button" class="btn btn-sm btn-error" @click="applyRemove" :disabled="removeBusy">
              {{ removeBusy ? 'Removing…' : 'Remove' }}
            </button>
          </div>
        </div>
      </div>

      <div
        v-if="menuFolder && tagPopoverOpen"
        class="card bg-base-200 fixed z-[1000] w-80 shadow-2xl"
        :style="{ left: menuX + 'px', top: menuY + 'px' }"
        role="dialog"
        aria-label="Apply tags to folder"
        @click.stop
        @keydown.enter="applyTags"
      >
        <div class="card-body gap-3 p-4">
          <p class="truncate text-sm" :title="menuFolder">
            Tag all photos in <span class="font-mono text-xs">{{ menuFolder }}</span>
          </p>
          <TagInput
            ref="tagInputRef"
            v-model="tagDraft"
            placeholder="Add tag…"
            aria-label="Tags to apply"
          />
          <div v-if="tagError" class="alert alert-error alert-soft text-xs" role="alert">{{ tagError }}</div>
          <div class="card-actions justify-end">
            <button type="button" class="btn btn-sm btn-ghost" @click="closeMenu" :disabled="tagBusy">Cancel</button>
            <button
              type="button"
              class="btn btn-sm btn-primary"
              @click="applyTags"
              :disabled="tagBusy || tagDraft.length === 0"
            >{{ tagBusy ? 'Applying…' : 'Apply' }}</button>
          </div>
        </div>
      </div>
    </Teleport>
  </nav>
</template>
