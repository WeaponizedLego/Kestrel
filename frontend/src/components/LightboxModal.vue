<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { apiPost, friendlyError, photoSrc } from '../transport/api'
import type { Photo } from '../types'
import { isVideo } from '../util/media'
import { copyImageViaCanvas } from '../util/clipboardFallback'

const props = defineProps<{ photo: Photo }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'prev'): void
  (e: 'next'): void
}>()

const src = computed(() => photoSrc(props.photo.Path))
const video = computed(() => isVideo(props.photo))

function onKey(e: KeyboardEvent) {
  switch (e.key) {
    case 'Escape':
      e.stopPropagation()
      emit('close')
      break
    case 'ArrowLeft':
      e.stopPropagation()
      emit('prev')
      break
    case 'ArrowRight':
      e.stopPropagation()
      emit('next')
      break
  }
}

onMounted(() => window.addEventListener('keydown', onKey, true))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey, true))

const revealError = ref<string | null>(null)
async function revealInFolder() {
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
  if (copyState.value === 'copying' || video.value) return
  copyError.value = null
  copyState.value = 'copying'
  try {
    await apiPost<{ copied: boolean }>('/api/clipboard/copy', { path: props.photo.Path })
    flashCopyState('copied')
    return
  } catch (backendErr) {
    try {
      await copyImageViaCanvas(src.value)
      flashCopyState('copied')
      return
    } catch {
      copyError.value = friendlyError(backendErr)
      flashCopyState('error')
    }
  }
}

function flashCopyState(next: 'copied' | 'error') {
  copyState.value = next
  if (copyResetTimer !== null) window.clearTimeout(copyResetTimer)
  copyResetTimer = window.setTimeout(() => {
    copyState.value = 'idle'
    copyResetTimer = null
  }, 1800)
}
</script>

<template>
  <div
    class="modal modal-open"
    role="dialog"
    :aria-label="`Preview ${photo.Name}`"
    @click.self="emit('close')"
  >
    <div
      class="modal-box relative flex h-[95vh] w-[95vw] max-w-[95vw] flex-col items-stretch overflow-hidden bg-base-100 p-0"
      @click.stop
    >
      <button
        type="button"
        class="btn btn-sm btn-circle btn-ghost absolute right-2 top-2 z-10"
        aria-label="Close preview"
        @click="emit('close')"
      >
        ✕
      </button>

      <div class="relative min-h-0 flex-1 p-2">
        <video
          v-if="video"
          :src="src"
          controls
          autoplay
          preload="metadata"
          class="absolute inset-0 m-auto max-h-full max-w-full rounded"
        />
        <img
          v-else
          :src="src"
          :alt="photo.Name"
          class="absolute inset-0 m-auto max-h-full max-w-full object-contain rounded"
        />
      </div>

      <footer class="flex flex-col gap-2 border-t border-base-300 px-4 py-3">
        <div class="flex flex-wrap justify-center gap-2">
          <button type="button" class="btn btn-sm btn-outline" aria-label="Previous photo" @click="emit('prev')">← Prev</button>
          <button type="button" class="btn btn-sm btn-outline" aria-label="Next photo" @click="emit('next')">Next →</button>
          <button
            v-if="!video"
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
          <button type="button" class="btn btn-sm btn-outline" @click="revealInFolder">Show in folder</button>
        </div>
        <p v-if="copyError && copyState === 'error'" class="text-center text-error text-xs" role="alert">{{ copyError }}</p>
        <p v-if="revealError" class="text-center text-error text-xs" role="alert">{{ revealError }}</p>
      </footer>
    </div>
  </div>
</template>
