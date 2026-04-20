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
// Using a ref<Set> means mutations don't re-trigger reactivity; we
// replace the set on every toggle so Vue sees a fresh reference.
const expanded = ref<Set<string>>(new Set())

async function load() {
  loading.value = true
  error.value = null
  try {
    nodes.value = await apiGet<FolderNode[]>('/api/folders')
    // On first successful load expand every root so the tree isn't a
    // single collapsed row — feels broken otherwise.
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

// roots rebuilds the nested tree from the flat response. Stable order:
// roots by path, children by name. The server already returns a
// sorted flat list, so this is just O(N) bucketing into children.
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

// Flatten the tree to rows currently visible (respecting expand
// state). Iterative render via v-for is simpler than a self-recursive
// component and avoids the Vue SFC footgun around name-based recursion.
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
  // Scans fire library:updated — reload the tree so new folders
  // appear without a manual refresh.
  unsubUpdate = onEvent('library:updated', () => load())
  window.addEventListener('click', closeMenu)
  window.addEventListener('keydown', onEscape)
})
onBeforeUnmount(() => {
  unsubUpdate?.()
  window.removeEventListener('click', closeMenu)
  window.removeEventListener('keydown', onEscape)
})

// Right-click context menu state. Two phases: a tiny menu with one
// action, then a tag-input popover when the user picks "Add tag". Both
// anchor at the click coordinates so the popover drops where the
// user's attention already is.
const menuFolder = ref<string | null>(null)
const menuX = ref(0)
const menuY = ref(0)
const tagDraft = ref<string[]>([])
const tagError = ref<string | null>(null)
const tagBusy = ref(false)
const tagPopoverOpen = ref(false)
const tagInputRef = ref<InstanceType<typeof TagInput> | null>(null)

function openMenu(e: MouseEvent, path: string) {
  e.preventDefault()
  menuFolder.value = path
  menuX.value = e.clientX
  menuY.value = e.clientY
  tagPopoverOpen.value = false
}

function closeMenu() {
  menuFolder.value = null
  tagPopoverOpen.value = false
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

async function applyTags() {
  if (!menuFolder.value || tagDraft.value.length === 0) {
    closeMenu()
    return
  }
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
  <nav class="sidebar">
    <h2 class="sidebar__title">Library</h2>

    <button
      type="button"
      class="sidebar__all"
      :class="{ 'sidebar__all--active': selectedFolder === null }"
      @click="select(null)"
    >
      All photos
    </button>

    <p v-if="error" class="sidebar__error" role="alert">{{ error }}</p>
    <p v-else-if="loading && nodes.length === 0" class="sidebar__muted">Loading…</p>
    <p
      v-else-if="nodes.length === 0"
      class="sidebar__muted"
    >
      Scan a folder to see it here.
    </p>

    <ul v-else class="sidebar__tree">
      <li
        v-for="{ node, depth } in rows"
        :key="node.path"
        class="sidebar__row"
        :class="{ 'sidebar__row--active': selectedFolder === node.path }"
        :style="{ paddingLeft: depth * 12 + 'px' }"
        @contextmenu="openMenu($event, node.path)"
      >
        <button
          type="button"
          class="sidebar__chev"
          :class="{
            'sidebar__chev--hidden': node.children.length === 0,
            'sidebar__chev--open': expanded.has(node.path),
          }"
          :aria-label="expanded.has(node.path) ? 'Collapse' : 'Expand'"
          @click="toggle(node.path)"
        >
          <svg width="8" height="8" viewBox="0 0 8 8" fill="none" aria-hidden="true">
            <path d="M2.5 1.5L5.5 4L2.5 6.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <button
          type="button"
          class="sidebar__label"
          :title="node.path"
          @click="select(node.path)"
        >
          <span class="sidebar__name">{{ node.name }}</span>
          <span class="sidebar__count">{{ node.total }}</span>
        </button>
      </li>
    </ul>

    <Teleport to="body">
      <div
        v-if="menuFolder && !tagPopoverOpen"
        class="sidebar__menu"
        :style="{ left: menuX + 'px', top: menuY + 'px' }"
        role="menu"
        @click.stop
      >
        <button
          type="button"
          class="sidebar__menu-item"
          role="menuitem"
          @click="startTagEntry"
        >
          Add tag to all photos in folder…
        </button>
      </div>

      <div
        v-if="menuFolder && tagPopoverOpen"
        class="sidebar__popover"
        :style="{ left: menuX + 'px', top: menuY + 'px' }"
        role="dialog"
        aria-label="Apply tags to folder"
        @click.stop
        @keydown.enter="applyTags"
      >
        <p class="sidebar__popover-title" :title="menuFolder">
          Tag all photos in <span class="sidebar__popover-path">{{ menuFolder }}</span>
        </p>
        <TagInput
          ref="tagInputRef"
          v-model="tagDraft"
          placeholder="Add tag…"
          aria-label="Tags to apply"
        />
        <p v-if="tagError" class="sidebar__popover-error" role="alert">{{ tagError }}</p>
        <div class="sidebar__popover-actions">
          <button
            type="button"
            class="sidebar__popover-btn"
            @click="closeMenu"
            :disabled="tagBusy"
          >Cancel</button>
          <button
            type="button"
            class="sidebar__popover-btn sidebar__popover-btn--primary"
            @click="applyTags"
            :disabled="tagBusy || tagDraft.length === 0"
          >{{ tagBusy ? 'Applying…' : 'Apply' }}</button>
        </div>
      </div>
    </Teleport>
  </nav>
</template>

<style scoped>
.sidebar {
  padding: var(--space-5) var(--space-3) var(--space-5);
  color: var(--text-secondary);
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  font-size: var(--fs-small);
}

.sidebar__title {
  margin: 0 0 var(--space-3);
  padding: 0 var(--space-3);
  font-size: var(--fs-micro);
  font-weight: var(--fw-semibold);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  color: var(--text-muted);
}

.sidebar__all {
  position: relative;
  display: flex;
  align-items: center;
  width: 100%;
  height: 24px;
  text-align: left;
  background: transparent;
  color: var(--text-secondary);
  border: none;
  padding: 0 var(--space-3);
  border-radius: var(--radius-sm);
  cursor: pointer;
  font: inherit;
  font-size: var(--fs-small);
  margin-bottom: var(--space-5);
  transition: background var(--dur-fast) var(--ease-out),
              color var(--dur-fast) var(--ease-out);
}
.sidebar__all:hover {
  background: var(--surface-hover);
  color: var(--text-primary);
}
.sidebar__all--active {
  background: var(--accent-wash);
  color: var(--text-primary);
  font-weight: var(--fw-medium);
}
.sidebar__all--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 4px;
  bottom: 4px;
  width: 2px;
  background: var(--accent);
  border-radius: 0 var(--radius-xs) var(--radius-xs) 0;
}

.sidebar__muted {
  color: var(--text-muted);
  padding: var(--space-3) var(--space-3);
  font-size: var(--fs-small);
  margin: 0;
}
.sidebar__error {
  color: var(--danger);
  padding: var(--space-3);
  margin: 0;
  font-size: var(--fs-small);
}

.sidebar__tree { list-style: none; margin: 0; padding: 0; }

.sidebar__row {
  position: relative;
  display: flex;
  align-items: center;
  height: 24px;
  border-radius: var(--radius-sm);
  transition: background var(--dur-fast) var(--ease-out);
}
.sidebar__row:hover { background: var(--surface-hover); }
.sidebar__row--active { background: var(--accent-wash); }
.sidebar__row--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 4px;
  bottom: 4px;
  width: 2px;
  background: var(--accent);
  border-radius: 0 var(--radius-xs) var(--radius-xs) 0;
}

.sidebar__chev {
  flex-shrink: 0;
  width: 16px;
  height: 24px;
  background: transparent;
  color: var(--text-muted);
  border: none;
  cursor: pointer;
  padding: 0;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color var(--dur-fast) var(--ease-out),
              transform var(--dur-fast) var(--ease-out);
  font: inherit;
  font-size: 9px;
}
.sidebar__chev:hover { color: var(--text-primary); }
.sidebar__chev--hidden { visibility: hidden; pointer-events: none; }
.sidebar__chev svg { transition: transform var(--dur-fast) var(--ease-out); }
.sidebar__chev--open svg { transform: rotate(90deg); }

.sidebar__label {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  background: transparent;
  color: var(--text-secondary);
  border: none;
  padding: 0 var(--space-3) 0 var(--space-1);
  height: 100%;
  cursor: pointer;
  font: inherit;
  font-size: var(--fs-small);
  text-align: left;
  transition: color var(--dur-fast) var(--ease-out);
}
.sidebar__row:hover .sidebar__label { color: var(--text-primary); }
.sidebar__row--active .sidebar__label {
  color: var(--text-primary);
  font-weight: var(--fw-medium);
}
.sidebar__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar__count {
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: var(--fs-micro);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
  letter-spacing: 0;
}
.sidebar__row:hover .sidebar__count { color: var(--text-muted); }
.sidebar__row--active .sidebar__count { color: var(--accent); }

.sidebar__menu {
  position: fixed;
  z-index: 1000;
  min-width: 240px;
  background: var(--surface-elevated);
  border-radius: var(--radius-md);
  box-shadow: var(--elev-popover);
  padding: var(--space-2);
}
.sidebar__menu-item {
  width: 100%;
  text-align: left;
  background: transparent;
  color: var(--text-primary);
  border: none;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-sm);
  cursor: pointer;
  font: inherit;
  font-size: var(--fs-small);
  transition: background var(--dur-fast) var(--ease-out);
}
.sidebar__menu-item:hover {
  background: var(--accent-wash);
  color: var(--text-primary);
}

.sidebar__popover {
  position: fixed;
  z-index: 1000;
  width: 320px;
  max-width: calc(100vw - 24px);
  background: var(--surface-elevated);
  border-radius: var(--radius-md);
  box-shadow: var(--elev-popover);
  padding: var(--space-5);
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}
.sidebar__popover-title {
  margin: 0;
  color: var(--text-secondary);
  font-size: var(--fs-small);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar__popover-path {
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-caption);
}
.sidebar__popover-error { color: var(--danger); margin: 0; font-size: var(--fs-small); }
.sidebar__popover-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  margin-top: var(--space-2);
}
.sidebar__popover-btn {
  background: transparent;
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: 0 var(--space-5);
  height: 28px;
  cursor: pointer;
  font: inherit;
  font-size: var(--fs-small);
  font-weight: var(--fw-medium);
  transition: background var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out);
}
.sidebar__popover-btn:hover:not(:disabled) {
  background: var(--surface-hover);
  border-color: var(--border-strong);
}
.sidebar__popover-btn:disabled { color: var(--text-muted); cursor: not-allowed; opacity: 0.5; }
.sidebar__popover-btn--primary {
  background: var(--accent);
  color: #0A0A0B;
  border-color: var(--accent);
}
.sidebar__popover-btn--primary:hover:not(:disabled) {
  background: var(--accent-hover);
  border-color: var(--accent-hover);
}
.sidebar__popover-btn--primary:disabled {
  background: var(--accent);
  border-color: var(--accent);
  color: #0A0A0B;
  opacity: 0.35;
}
</style>
