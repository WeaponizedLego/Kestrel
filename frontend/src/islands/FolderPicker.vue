<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { apiGet, friendlyError } from '../transport/api'

interface BrowseEntry {
  name: string
  path: string
}
interface BrowseResponse {
  path: string
  parent: string
  entries: BrowseEntry[]
}

const props = defineProps<{ initialPath?: string }>()
const emit = defineEmits<{
  (e: 'choose', path: string): void
  (e: 'close'): void
}>()

const current = ref<BrowseResponse | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

async function navigate(path: string) {
  loading.value = true
  error.value = null
  try {
    const q = path ? `?path=${encodeURIComponent(path)}` : ''
    current.value = await apiGet<BrowseResponse>(`/api/browse${q}`)
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    loading.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => {
  window.addEventListener('keydown', onKey)
  navigate(props.initialPath ?? '')
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

watch(
  () => props.initialPath,
  (v) => {
    if (v) navigate(v)
  },
)
</script>

<template>
  <div class="picker" role="dialog" aria-modal="true" @click.self="emit('close')">
    <div class="picker__panel">
      <header class="picker__head">
        <button
          class="picker__up"
          :disabled="!current?.parent"
          :title="current?.parent || 'Already at root'"
          @click="current?.parent && navigate(current.parent)"
        >
          ↑ Up
        </button>
        <span class="picker__path">{{ current?.path ?? '…' }}</span>
      </header>

      <div class="picker__body">
        <p v-if="error" class="picker__error" role="alert">{{ error }}</p>
        <p v-else-if="loading" class="picker__muted">Loading…</p>
        <p
          v-else-if="current && current.entries.length === 0"
          class="picker__muted"
        >
          No sub-folders here.
        </p>
        <ul v-else class="picker__list">
          <li
            v-for="entry in current?.entries ?? []"
            :key="entry.path"
            class="picker__item"
            @dblclick="navigate(entry.path)"
          >
            <button class="picker__item-btn" @click="navigate(entry.path)">
              📁 {{ entry.name }}
            </button>
          </li>
        </ul>
      </div>

      <footer class="picker__foot">
        <button class="picker__cancel" @click="emit('close')">Cancel</button>
        <button
          class="picker__choose"
          :disabled="!current?.path"
          @click="current && emit('choose', current.path)"
        >
          Use this folder
        </button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.picker {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 900;
}
.picker__panel {
  width: min(640px, 90vw);
  max-height: 80vh;
  background: var(--surface-raised);
  color: var(--text-primary);
  border-radius: var(--radius-md);
  box-shadow: var(--elev-raised);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.picker__head {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--surface-inset);
  box-shadow: var(--elev-inset);
}
.picker__path {
  font: var(--fw-regular) var(--fs-body) / 1.2 var(--font-mono, monospace);
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}
.picker__up {
  background: transparent;
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  cursor: pointer;
}
.picker__up:disabled { opacity: 0.4; cursor: not-allowed; }

.picker__body {
  flex: 1;
  overflow: auto;
  padding: var(--space-3);
}
.picker__error { color: var(--danger); }
.picker__muted { color: var(--text-muted); padding: var(--space-4); }
.picker__list { list-style: none; margin: 0; padding: 0; }
.picker__item + .picker__item { border-top: var(--border-thin) solid var(--border-subtle); }
.picker__item-btn {
  width: 100%;
  text-align: left;
  background: transparent;
  color: var(--text-primary);
  border: none;
  padding: var(--space-3) var(--space-4);
  font: inherit;
  cursor: pointer;
  border-radius: var(--radius-sm);
}
.picker__item-btn:hover { background: var(--surface-inset); }

.picker__foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--surface-inset);
  box-shadow: var(--elev-inset);
}
.picker__cancel,
.picker__choose {
  border: none;
  border-radius: var(--radius-full);
  padding: var(--space-2) var(--space-5);
  font: var(--fw-medium) var(--fs-default) / 1 var(--font-sans);
  cursor: pointer;
}
.picker__cancel {
  background: transparent;
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
}
.picker__choose { background: var(--accent); color: #fff; box-shadow: var(--elev-raised); }
.picker__choose:hover:not(:disabled) { background: var(--accent-hover); }
.picker__choose:disabled { background: #4A3A30; color: var(--text-muted); cursor: not-allowed; }
</style>
