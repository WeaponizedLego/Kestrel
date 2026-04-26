<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { apiGet, friendlyError } from '../transport/api'

interface BrowseEntry { name: string; path: string; has_children: boolean }
interface BrowseResponse { path: string; parent: string; entries: BrowseEntry[] }

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

function onKey(e: KeyboardEvent) { if (e.key === 'Escape') emit('close') }

onMounted(() => {
  window.addEventListener('keydown', onKey)
  navigate(props.initialPath ?? '')
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

watch(() => props.initialPath, (v) => { if (v) navigate(v) })
</script>

<template>
  <div class="modal modal-open" role="dialog" aria-modal="true" @click.self="emit('close')">
    <div class="modal-box max-w-xl p-0 flex flex-col max-h-[72vh]" @click.stop>
      <div class="flex items-center justify-between border-b border-base-300 px-4 h-11">
        <h2 class="text-base font-semibold">Choose folder</h2>
        <button type="button" class="btn btn-ghost btn-sm btn-square" aria-label="Close" @click="emit('close')">✕</button>
      </div>

      <div class="flex items-center gap-2 bg-base-200 border-b border-base-300 px-4 py-2">
        <button
          type="button"
          class="btn btn-xs btn-square btn-ghost"
          :disabled="!current?.parent"
          :title="current?.parent || 'Already at root'"
          aria-label="Go up"
          @click="current?.parent && navigate(current.parent)"
        >
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
            <path d="M6 9.5V2.5M3 5.5L6 2.5L9 5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <span class="font-mono text-xs break-all text-base-content/70">{{ current?.path ?? '…' }}</span>
      </div>

      <div class="flex-1 overflow-auto p-2 min-h-48">
        <div v-if="error" class="alert alert-error alert-soft m-2 text-xs" role="alert">{{ error }}</div>
        <p v-else-if="loading" class="p-6 text-center text-sm text-base-content/60">Loading…</p>
        <p
          v-else-if="current && current.entries.length === 0"
          class="p-6 text-center text-sm text-base-content/60"
        >
          No sub-folders here.
        </p>
        <ul v-else class="menu menu-sm w-full">
          <li v-for="entry in current?.entries ?? []" :key="entry.path">
            <button type="button" @click="navigate(entry.path)" @dblclick="navigate(entry.path)">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true" class="shrink-0 opacity-70">
                <path d="M1.5 3.5C1.5 2.9 1.9 2.5 2.5 2.5H5.5L7 4H11.5C12.1 4 12.5 4.4 12.5 5V10.5C12.5 11.1 12.1 11.5 11.5 11.5H2.5C1.9 11.5 1.5 11.1 1.5 10.5V3.5Z" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round"/>
              </svg>
              <span class="truncate">{{ entry.name }}</span>
              <svg
                v-if="entry.has_children"
                width="8" height="8" viewBox="0 0 8 8" fill="none" aria-hidden="true"
                class="ms-auto shrink-0 opacity-50"
              >
                <path d="M2.5 1.5L5.5 4L2.5 6.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </button>
          </li>
        </ul>
      </div>

      <div class="flex justify-end gap-2 border-t border-base-300 px-4 py-3">
        <button type="button" class="btn btn-sm btn-ghost" @click="emit('close')">Cancel</button>
        <button
          type="button"
          class="btn btn-sm btn-primary"
          :disabled="!current?.path"
          @click="current && emit('choose', current.path)"
        >
          Choose
        </button>
      </div>
    </div>
  </div>
</template>
