<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { apiPost, friendlyError, photoSrc } from '../transport/api'
import { useCapabilities } from '../transport/capabilities'
import { copyImageToClipboard } from '../transport/clipboard'
import { requestDelete, requestMove } from '../transport/fileops'
import type { Photo } from '../types'
import { isVideo } from '../util/media'

const TagInput = defineAsyncComponent(() => import('../components/TagInput.vue'))
const LightboxModal = defineAsyncComponent(() => import('../components/LightboxModal.vue'))

const props = defineProps<{ photo: Photo | null }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'prev'): void
  (e: 'next'): void
}>()

const src = computed(() => (props.photo ? photoSrc(props.photo.Path) : ''))
const video = computed(() => (props.photo ? isVideo(props.photo) : false))
const capabilities = useCapabilities()

const dims = computed(() =>
  props.photo && props.photo.Width && props.photo.Height
    ? `${props.photo.Width} × ${props.photo.Height}`
    : '—',
)
const sizeLabel = computed(() => (props.photo ? formatBytes(props.photo.SizeBytes) : '—'))
const takenLabel = computed(() => formatDate(props.photo?.TakenAt))
const modifiedLabel = computed(() => formatDate(props.photo?.ModTime))

function formatBytes(n: number): string {
  const units = ['B', 'KB', 'MB', 'GB']
  let v = n, i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

function formatDate(raw: string | undefined): string {
  if (!raw || raw.startsWith('0001-')) return '—'
  const d = new Date(raw)
  return isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

const previewOpen = ref(false)
function openPreview() { previewOpen.value = true }
defineExpose({ openPreview })

function onKey(e: KeyboardEvent) {
  if (previewOpen.value) return
  if (!props.photo) return
  switch (e.key) {
    case 'Escape': emit('close'); break
    case 'ArrowLeft': emit('prev'); break
    case 'ArrowRight': emit('next'); break
  }
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

const revealError = ref<string | null>(null)
async function revealInFolder() {
  if (!props.photo) return
  revealError.value = null
  try {
    await apiPost<{ revealed: boolean }>('/api/reveal', { path: props.photo.Path })
  } catch (err) {
    revealError.value = friendlyError(err)
  }
}

const copyState = ref<'idle' | 'copying' | 'copied' | 'error'>('idle')
const copyError = ref<string | null>(null)
let copyResetTimer: number | null = null

async function copyImage() {
  if (!props.photo) return
  if (copyState.value === 'copying') return
  copyError.value = null
  copyState.value = 'copying'
  try {
    await copyImageToClipboard(props.photo.Path)
    flashCopyState('copied')
  } catch (err) {
    copyError.value = friendlyError(err)
    flashCopyState('error')
  }
}

function onMove() {
  if (!props.photo) return
  requestMove([props.photo.Path])
}

function onDelete() {
  if (!props.photo) return
  requestDelete([props.photo.Path])
}

function flashCopyState(next: 'copied' | 'error') {
  copyState.value = next
  if (copyResetTimer !== null) window.clearTimeout(copyResetTimer)
  copyResetTimer = window.setTimeout(() => {
    copyState.value = 'idle'
    copyResetTimer = null
  }, 1800)
}

const tagDraft = ref<string[]>(props.photo?.Tags ?? [])
const tagError = ref<string | null>(null)
watch(() => props.photo?.Path, () => { tagDraft.value = props.photo?.Tags ?? [] })

async function commitTags(next: string[]) {
  if (!props.photo) return
  tagError.value = null
  tagDraft.value = next
  try {
    const res = await apiPost<{ tags: string[] | null }>('/api/tags', {
      path: props.photo.Path,
      tags: next,
    })
    const canonical = res.tags ?? []
    tagDraft.value = canonical
    props.photo.Tags = canonical
  } catch (err) {
    tagError.value = friendlyError(err)
  }
}
</script>

<template>
  <aside class="card bg-base-200 border border-base-300 w-80 shrink-0 flex-col overflow-hidden text-sm flex" aria-label="Photo details">
    <div class="flex items-center gap-2 border-b border-base-300 px-4 py-3">
      <button
        v-if="photo"
        type="button"
        class="btn btn-xs btn-square btn-ghost"
        aria-label="Close details"
        @click="emit('close')"
      >✕</button>
      <h2
        v-if="photo"
        class="truncate text-base font-semibold"
        :title="photo.Name"
      >{{ photo.Name }}</h2>
      <h2
        v-else
        class="truncate text-base font-semibold text-base-content/50"
      >No photo selected</h2>
    </div>

    <div class="flex max-h-[40vh] min-h-48 items-center justify-center border-b border-base-300 bg-base-300/30 p-4">
      <template v-if="photo">
        <video
          v-if="video"
          :src="src"
          controls
          preload="metadata"
          class="max-h-full max-w-full rounded"
        />
        <img
          v-else
          :src="src"
          :alt="photo.Name"
          class="max-h-full max-w-full object-contain rounded"
        />
      </template>
      <svg
        v-else
        width="48"
        height="48"
        viewBox="0 0 48 48"
        fill="none"
        aria-hidden="true"
        class="text-base-content/20"
      >
        <rect x="6" y="10" width="36" height="28" rx="3" stroke="currentColor" stroke-width="1.5"/>
        <circle cx="17" cy="21" r="3" stroke="currentColor" stroke-width="1.5"/>
        <path d="M6 32L18 22L28 30L36 24L42 28" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>
      </svg>
    </div>

    <div
      v-if="photo && video && !capabilities.ffmpeg"
      role="alert"
      class="alert alert-warning alert-soft mx-4 mt-3 text-xs"
    >
      ffmpeg not installed — thumbnails for videos are placeholders. Install ffmpeg and rescan to generate real previews.
    </div>

    <div v-if="photo && !video" class="flex flex-col gap-2 px-4 pt-3 pb-2">
      <button
        type="button"
        :class="[
          'btn btn-sm',
          copyState === 'copied' ? 'btn-success' : '',
          copyState === 'error' ? 'btn-error' : 'btn-outline',
        ]"
        :disabled="copyState === 'copying'"
        @click="copyImage"
      >
        {{
          copyState === 'copying' ? 'Copying…' :
          copyState === 'copied'  ? 'Copied ✓' :
          copyState === 'error'   ? 'Copy failed' :
          'Copy image'
        }}
      </button>
      <p v-if="copyError && copyState === 'error'" class="text-error text-xs" role="alert">{{ copyError }}</p>
    </div>

    <div v-if="photo" class="join px-4 pb-3">
      <button type="button" class="btn btn-sm btn-outline join-item flex-1" aria-label="Previous photo" @click="emit('prev')">← Prev</button>
      <button type="button" class="btn btn-sm btn-outline join-item flex-1" aria-label="Next photo" @click="emit('next')">Next →</button>
    </div>

    <section v-if="photo" class="border-b border-base-300 px-4 py-3">
      <h3 class="mb-2 text-[10px] font-semibold uppercase tracking-wider text-base-content/50">Tags</h3>
      <TagInput
        :model-value="tagDraft"
        placeholder="Add tag…"
        aria-label="Edit photo tags"
        @update:model-value="commitTags"
      />
      <p v-if="tagError" class="text-error text-xs mt-1" role="alert">{{ tagError }}</p>
    </section>

    <div v-if="photo" class="flex flex-col gap-2 px-4 py-3">
      <button type="button" class="btn btn-sm btn-outline" @click="previewOpen = true">Preview</button>
      <button type="button" class="btn btn-sm btn-outline" @click="revealInFolder">Show in folder</button>
      <button type="button" class="btn btn-sm btn-outline" @click="onMove">Move…</button>
      <button type="button" class="btn btn-sm btn-outline btn-error" @click="onDelete">Delete</button>
      <p v-if="revealError" class="text-error text-xs" role="alert">{{ revealError }}</p>
    </div>

    <dl class="flex flex-col gap-2 overflow-y-auto border-t border-base-300 px-4 py-3 text-xs">
      <div class="flex items-baseline justify-between gap-3">
        <dt class="uppercase tracking-wider text-base-content/50 text-[10px] font-semibold">Dimensions</dt>
        <dd class="m-0 font-mono tabular-nums">{{ dims }}</dd>
      </div>
      <div class="flex items-baseline justify-between gap-3">
        <dt class="uppercase tracking-wider text-base-content/50 text-[10px] font-semibold">Size</dt>
        <dd class="m-0 font-mono tabular-nums">{{ sizeLabel }}</dd>
      </div>
      <div class="flex items-baseline justify-between gap-3">
        <dt class="uppercase tracking-wider text-base-content/50 text-[10px] font-semibold">Taken</dt>
        <dd class="m-0 font-mono tabular-nums">{{ takenLabel }}</dd>
      </div>
      <div class="flex items-baseline justify-between gap-3">
        <dt class="uppercase tracking-wider text-base-content/50 text-[10px] font-semibold">Modified</dt>
        <dd class="m-0 font-mono tabular-nums">{{ modifiedLabel }}</dd>
      </div>
      <div v-if="photo?.CameraMake" class="flex items-baseline justify-between gap-3">
        <dt class="uppercase tracking-wider text-base-content/50 text-[10px] font-semibold">Camera</dt>
        <dd class="m-0 font-mono tabular-nums">{{ photo.CameraMake }}</dd>
      </div>
      <div class="flex items-baseline justify-between gap-3">
        <dt class="uppercase tracking-wider text-base-content/50 text-[10px] font-semibold">Path</dt>
        <dd class="m-0 break-all font-mono text-[11px] text-base-content/70" :title="photo?.Path ?? ''">{{ photo?.Path ?? '—' }}</dd>
      </div>
    </dl>

    <LightboxModal
      v-if="previewOpen && photo"
      :photo="photo"
      @close="previewOpen = false"
      @prev="emit('prev')"
      @next="emit('next')"
    />
  </aside>
</template>
