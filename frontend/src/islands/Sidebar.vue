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
        :style="{ paddingLeft: depth * 14 + 'px' }"
        @contextmenu="openMenu($event, node.path)"
      >
        <button
          type="button"
          class="sidebar__chev"
          :class="{ 'sidebar__chev--hidden': node.children.length === 0 }"
          :aria-label="expanded.has(node.path) ? 'Collapse' : 'Expand'"
          @click="toggle(node.path)"
        >
          {{ expanded.has(node.path) ? '▾' : '▸' }}
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
  padding: var(--space-3);
  color: var(--text-secondary);
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
  overflow: auto;
}
.sidebar__title {
  margin: 0 0 var(--space-2);
  padding: var(--space-2) var(--space-3);
  font-size: var(--fs-caption);
  font-weight: var(--fw-medium);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--text-muted);
}

.sidebar__all {
  display: block;
  width: 100%;
  text-align: left;
  background: transparent;
  color: var(--text-primary);
  border: none;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  cursor: pointer;
  font: inherit;
  margin-bottom: var(--space-2);
}
.sidebar__all:hover { background: var(--surface-raised); }
.sidebar__all--active { background: var(--surface-raised); color: var(--accent); }

.sidebar__muted {
  color: var(--text-muted);
  padding: var(--space-3);
  font-size: var(--fs-body);
  margin: 0;
}
.sidebar__error {
  color: var(--danger);
  padding: var(--space-3);
  margin: 0;
}

.sidebar__tree { list-style: none; margin: 0; padding: 0; }
.sidebar__row {
  display: flex;
  align-items: center;
  border-radius: var(--radius-sm);
}
.sidebar__row:hover { background: var(--surface-raised); }
.sidebar__row--active { background: var(--surface-raised); color: var(--accent); }

.sidebar__chev {
  flex-shrink: 0;
  width: 20px;
  height: 24px;
  background: transparent;
  color: var(--text-muted);
  border: none;
  cursor: pointer;
  font-size: 10px;
  padding: 0;
  line-height: 1;
}
.sidebar__chev--hidden { visibility: hidden; pointer-events: none; }

.sidebar__label {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  background: transparent;
  color: inherit;
  border: none;
  padding: var(--space-2);
  cursor: pointer;
  font: inherit;
  text-align: left;
}
.sidebar__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar__count {
  color: var(--text-muted);
  font-size: var(--fs-body);
  flex-shrink: 0;
}
.sidebar__row--active .sidebar__count { color: var(--accent); }

.sidebar__menu {
  position: fixed;
  z-index: 1000;
  min-width: 220px;
  background: var(--surface-raised);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  box-shadow: var(--elev-overlay);
  padding: var(--space-2);
}
.sidebar__menu-item {
  width: 100%;
  text-align: left;
  background: transparent;
  color: var(--text-primary);
  border: none;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  cursor: pointer;
  font: inherit;
}
.sidebar__menu-item:hover { background: var(--surface-inset); color: var(--accent); }

.sidebar__popover {
  position: fixed;
  z-index: 1000;
  width: 320px;
  max-width: calc(100vw - 24px);
  background: var(--surface-raised);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  box-shadow: var(--elev-overlay);
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.sidebar__popover-title {
  margin: 0;
  color: var(--text-secondary);
  font-size: var(--fs-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar__popover-path { color: var(--text-primary); }
.sidebar__popover-error { color: var(--danger); margin: 0; font-size: var(--fs-body); }
.sidebar__popover-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
.sidebar__popover-btn {
  background: transparent;
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
  font: inherit;
}
.sidebar__popover-btn:hover:not(:disabled) { border-color: var(--accent); }
.sidebar__popover-btn:disabled { color: var(--text-muted); cursor: not-allowed; }
.sidebar__popover-btn--primary {
  background: var(--accent);
  color: #fff;
  border-color: transparent;
  box-shadow: var(--elev-raised);
}
.sidebar__popover-btn--primary:hover:not(:disabled) { background: var(--accent-hover); }
.sidebar__popover-btn--primary:disabled { background: #4A3A30; box-shadow: none; }
</style>
