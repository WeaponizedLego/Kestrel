<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { photoSrc } from '../transport/api'
import type { Photo } from '../types'
import { isVideo } from '../util/media'

const props = defineProps<{ photo: Photo }>()
const emit = defineEmits<{ (e: 'close'): void }>()

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    emit('close')
  }
}

onMounted(() => window.addEventListener('keydown', onKey, true))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey, true))
</script>

<template>
  <div
    class="modal modal-open"
    role="dialog"
    :aria-label="`Preview ${photo.Name}`"
    @click.self="emit('close')"
  >
    <div
      class="modal-box relative flex max-h-[95vh] max-w-[95vw] items-center justify-center bg-base-100 p-0"
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
      <video
        v-if="isVideo(photo)"
        :src="photoSrc(photo.Path)"
        controls
        autoplay
        preload="metadata"
        class="max-h-[92vh] max-w-[95vw] rounded"
      />
      <img
        v-else
        :src="photoSrc(photo.Path)"
        :alt="photo.Name"
        class="max-h-[92vh] max-w-[95vw] object-contain rounded"
      />
    </div>
  </div>
</template>
