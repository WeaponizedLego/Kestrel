<script setup lang="ts">
import {
  cellSize,
  CELL_SIZE_MIN,
  CELL_SIZE_MAX,
  CELL_SIZE_STEP,
  selectedPaths,
} from '../transport/selection'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { openTaggingQueue, openSimilarityReview, openTagManager } from '../transport/tagging'
import {
  apiUndoDepth,
  requestDelete,
  requestMove,
  requestUndo,
} from '../transport/fileops'
import { onEvent } from '../transport/events'
import ThemeController from '../components/ThemeController.vue'

const hasSelection = computed(() => selectedPaths.value.size > 0)
const selectionLabel = computed(() => `${selectedPaths.value.size} selected`)

const undoDepth = ref(0)
async function refreshUndoDepth() {
  try {
    const res = await apiUndoDepth()
    undoDepth.value = res.depth
  } catch {
    undoDepth.value = 0
  }
}

let unsubDone: (() => void) | null = null
let unsubUndone: (() => void) | null = null

onMounted(() => {
  refreshUndoDepth()
  unsubDone = onEvent('fileops:done', refreshUndoDepth)
  unsubUndone = onEvent('fileops:undone', refreshUndoDepth)
})
onBeforeUnmount(() => {
  unsubDone?.()
  unsubUndone?.()
})

function onMove() {
  if (!hasSelection.value) return
  requestMove([...selectedPaths.value])
}
function onDelete() {
  if (!hasSelection.value) return
  requestDelete([...selectedPaths.value])
}
function onUndo() {
  if (undoDepth.value === 0) return
  requestUndo()
}
</script>

<template>
  <div class="flex w-full items-center justify-end gap-2">
    <span
      v-if="hasSelection"
      class="badge badge-primary badge-sm font-mono"
      aria-live="polite"
    >
      {{ selectionLabel }}
    </span>

    <div class="join">
      <button
        class="btn btn-sm btn-ghost join-item"
        type="button"
        :disabled="!hasSelection"
        @click="onMove"
        title="Move selected photos to a folder"
      >
        Move
      </button>
      <button
        class="btn btn-sm btn-ghost join-item text-error"
        type="button"
        :disabled="!hasSelection"
        @click="onDelete"
        title="Delete selected photos"
      >
        Delete
      </button>
      <button
        class="btn btn-sm btn-ghost join-item"
        type="button"
        :disabled="undoDepth === 0"
        @click="onUndo"
        :title="undoDepth > 0 ? `Undo last operation (${undoDepth} undoable)` : 'Nothing to undo'"
      >
        Undo
        <span v-if="undoDepth > 0" class="badge badge-ghost badge-xs">{{ undoDepth }}</span>
      </button>
    </div>

    <div class="join">
      <button
        class="btn btn-sm join-item"
        type="button"
        @click="() => openSimilarityReview()"
        title="Review near-identical duplicates and visually similar photos"
      >
        Similar
      </button>
      <button
        class="btn btn-sm join-item"
        type="button"
        @click="openTaggingQueue"
        title="Tag photos that have no tags yet, grouped by folder"
      >
        Tag queue
      </button>
      <button
        class="btn btn-sm join-item"
        type="button"
        @click="openTagManager"
        title="Rename, merge, or delete tags"
      >
        Manage
      </button>
    </div>

    <label class="flex items-center gap-2">
      <span class="text-xs uppercase tracking-wider text-base-content/60">Size</span>
      <input
        class="range range-xs range-primary w-40"
        type="range"
        :min="CELL_SIZE_MIN"
        :max="CELL_SIZE_MAX"
        :step="CELL_SIZE_STEP"
        v-model.number="cellSize"
        aria-label="Thumbnail size"
      />
      <span class="font-mono text-xs tabular-nums w-10 text-right">{{ cellSize }}px</span>
    </label>

    <ThemeController />
  </div>
</template>
