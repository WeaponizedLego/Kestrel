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
    <div class="picker__panel" @click.stop>
      <header class="picker__head">
        <h2 class="picker__title">Choose folder</h2>
        <button
          class="picker__close"
          aria-label="Close"
          @click="emit('close')"
        >✕</button>
      </header>

      <div class="picker__crumb">
        <button
          class="picker__up"
          :disabled="!current?.parent"
          :title="current?.parent || 'Already at root'"
          @click="current?.parent && navigate(current.parent)"
          aria-label="Go up"
        >
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
            <path d="M6 9.5V2.5M3 5.5L6 2.5L9 5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <span class="picker__path">{{ current?.path ?? '…' }}</span>
      </div>

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
              <svg class="picker__item-icon" width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
                <path d="M1.5 3.5C1.5 2.9 1.9 2.5 2.5 2.5H5.5L7 4H11.5C12.1 4 12.5 4.4 12.5 5V10.5C12.5 11.1 12.1 11.5 11.5 11.5H2.5C1.9 11.5 1.5 11.1 1.5 10.5V3.5Z" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round"/>
              </svg>
              <span class="picker__item-name">{{ entry.name }}</span>
            </button>
          </li>
        </ul>
      </div>

      <footer class="picker__foot">
        <button class="picker__btn picker__btn--ghost" @click="emit('close')">Cancel</button>
        <button
          class="picker__btn picker__btn--primary"
          :disabled="!current?.path"
          @click="current && emit('choose', current.path)"
        >
          Choose
        </button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.picker {
  position: fixed;
  inset: 0;
  background: rgba(5, 5, 7, 0.68);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 900;
  animation: picker-fade var(--dur-base) var(--ease-out);
}
@keyframes picker-fade {
  from { opacity: 0; }
  to   { opacity: 1; }
}
.picker__panel {
  width: min(520px, 92vw);
  max-height: 72vh;
  background: var(--surface-elevated);
  color: var(--text-primary);
  border-radius: var(--radius-lg);
  box-shadow: var(--elev-modal);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  animation: picker-rise var(--dur-slow) var(--ease-out);
}
@keyframes picker-rise {
  from { transform: translateY(8px); opacity: 0; }
  to   { transform: translateY(0); opacity: 1; }
}

.picker__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  height: 40px;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.picker__title {
  margin: 0;
  font-size: var(--fs-emphasis);
  font-weight: var(--fw-semibold);
  letter-spacing: var(--tracking-tight);
  color: var(--text-primary);
}
.picker__close {
  width: 24px;
  height: 24px;
  background: transparent;
  color: var(--text-muted);
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.picker__close:hover {
  color: var(--text-primary);
  background: var(--surface-hover);
}

.picker__crumb {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  background: var(--surface-inset);
  border-bottom: 1px solid var(--border-subtle);
}
.picker__up {
  width: 24px;
  height: 24px;
  background: transparent;
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: color var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.picker__up:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: var(--border-strong);
  background: var(--surface-hover);
}
.picker__up:disabled { opacity: 0.3; cursor: not-allowed; }
.picker__path {
  font: var(--fw-regular) var(--fs-caption) / 1.3 var(--font-mono);
  color: var(--text-secondary);
  overflow-wrap: anywhere;
  min-width: 0;
  flex: 1;
}

.picker__body {
  flex: 1;
  overflow: auto;
  padding: var(--space-3);
  min-height: 200px;
}
.picker__error {
  color: var(--danger);
  background: var(--danger-wash);
  padding: var(--space-4);
  margin: var(--space-3);
  border-radius: var(--radius-sm);
  font-size: var(--fs-small);
}
.picker__muted {
  color: var(--text-muted);
  padding: var(--space-6);
  text-align: center;
  font-size: var(--fs-small);
}
.picker__list { list-style: none; margin: 0; padding: 0; }
.picker__item-btn {
  width: 100%;
  text-align: left;
  background: transparent;
  color: var(--text-primary);
  border: none;
  padding: 0 var(--space-4);
  height: 28px;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  font: inherit;
  font-size: var(--fs-small);
  cursor: pointer;
  border-radius: var(--radius-sm);
  transition: background var(--dur-fast) var(--ease-out);
}
.picker__item-btn:hover { background: var(--surface-hover); }
.picker__item-icon {
  color: var(--text-muted);
  flex-shrink: 0;
  transition: color var(--dur-fast) var(--ease-out);
}
.picker__item-btn:hover .picker__item-icon { color: var(--accent); }
.picker__item-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.picker__foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-top: 1px solid var(--border-subtle);
}
.picker__btn {
  height: 28px;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: 0 var(--space-5);
  font: var(--fw-medium) var(--fs-small) / 1 var(--font-sans);
  cursor: pointer;
  transition: background var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out),
              color var(--dur-fast) var(--ease-out);
}
.picker__btn--ghost {
  background: transparent;
  color: var(--text-secondary);
}
.picker__btn--ghost:hover {
  color: var(--text-primary);
  background: var(--surface-hover);
  border-color: var(--border-strong);
}
.picker__btn--primary {
  background: var(--accent);
  color: #0A0A0B;
  border-color: var(--accent);
}
.picker__btn--primary:hover:not(:disabled) {
  background: var(--accent-hover);
  border-color: var(--accent-hover);
}
.picker__btn--primary:disabled {
  background: var(--accent);
  border-color: var(--accent);
  color: #0A0A0B;
  opacity: 0.35;
  cursor: not-allowed;
}
</style>
