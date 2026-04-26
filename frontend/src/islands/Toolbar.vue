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
import { fetchDebugInfo, type DebugInfo } from '../transport/debug'
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

const debugOpen = ref(false)
const debugInfo = ref<DebugInfo | null>(null)
const debugLoading = ref(false)
const debugError = ref('')
const debugCopied = ref<'path' | 'lines' | ''>('')
const logTail = ref<HTMLElement | null>(null)

async function loadDebugInfo() {
  debugLoading.value = true
  debugError.value = ''
  try {
    debugInfo.value = await fetchDebugInfo(500)
    // Defer scroll until the DOM has rendered the new lines.
    requestAnimationFrame(() => {
      if (logTail.value) logTail.value.scrollTop = logTail.value.scrollHeight
    })
  } catch (err) {
    debugError.value = err instanceof Error ? err.message : String(err)
  } finally {
    debugLoading.value = false
  }
}

function openDebug() {
  debugOpen.value = true
  if (!debugInfo.value) loadDebugInfo()
}
function closeDebug() {
  debugOpen.value = false
}

async function copyToClipboard(value: string, kind: 'path' | 'lines') {
  try {
    await navigator.clipboard.writeText(value)
    debugCopied.value = kind
    setTimeout(() => {
      if (debugCopied.value === kind) debugCopied.value = ''
    }, 1500)
  } catch {
    debugError.value = 'Clipboard copy failed — select and copy manually.'
  }
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '?'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`
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

    <button
      class="btn btn-sm btn-ghost btn-square"
      type="button"
      title="Show log path and recent log entries"
      aria-label="Open debug panel"
      @click="openDebug"
    >
      <span aria-hidden="true">ⓘ</span>
    </button>
  </div>

  <div
    v-if="debugOpen"
    class="modal modal-open"
    role="dialog"
    aria-modal="true"
    aria-labelledby="debug-modal-title"
    @click.self="closeDebug"
  >
    <div class="modal-box max-w-4xl" @click.stop>
      <div class="flex items-center justify-between">
        <h2 id="debug-modal-title" class="text-lg font-semibold">Debug</h2>
        <button
          type="button"
          class="btn btn-ghost btn-sm btn-square"
          aria-label="Close"
          @click="closeDebug"
        >✕</button>
      </div>

      <div class="mt-4 flex flex-col gap-4">
        <div class="flex flex-col gap-1">
          <span class="text-xs font-semibold uppercase tracking-wider text-base-content/60">
            Log file
          </span>
          <div class="join">
            <input
              class="input input-sm input-bordered join-item flex-1 font-mono"
              type="text"
              readonly
              :value="debugInfo?.log_path || '(file logging disabled)'"
              aria-label="Log file path"
            />
            <button
              class="btn btn-sm join-item"
              type="button"
              :disabled="!debugInfo?.log_path"
              @click="copyToClipboard(debugInfo!.log_path, 'path')"
            >
              {{ debugCopied === 'path' ? 'Copied' : 'Copy path' }}
            </button>
          </div>
          <span v-if="debugInfo" class="text-xs text-base-content/60">
            Size: {{ formatBytes(debugInfo.file_size) }} ·
            Backup on rotation: <span class="font-mono">{{ debugInfo.log_path_backup }}</span>
          </span>
        </div>

        <div class="flex flex-col gap-1">
          <div class="flex items-center justify-between">
            <span class="text-xs font-semibold uppercase tracking-wider text-base-content/60">
              Recent entries
              <span v-if="debugInfo" class="ml-1 font-normal normal-case tracking-normal text-base-content/50">
                ({{ debugInfo.lines_returned }}<span v-if="debugInfo.truncated"> of many</span>)
              </span>
            </span>
            <div class="join">
              <button
                class="btn btn-xs join-item"
                type="button"
                :disabled="debugLoading || !debugInfo?.lines.length"
                @click="copyToClipboard(debugInfo!.lines.join('\n'), 'lines')"
              >
                {{ debugCopied === 'lines' ? 'Copied' : 'Copy lines' }}
              </button>
              <button
                class="btn btn-xs join-item"
                type="button"
                :disabled="debugLoading"
                @click="loadDebugInfo"
              >
                {{ debugLoading ? 'Loading…' : 'Refresh' }}
              </button>
            </div>
          </div>

          <div
            ref="logTail"
            class="bg-base-200 rounded-box max-h-96 overflow-auto p-2 font-mono text-xs leading-relaxed"
            role="log"
            aria-live="polite"
          >
            <div v-if="debugError" class="text-error">{{ debugError }}</div>
            <div v-else-if="debugLoading && !debugInfo" class="text-base-content/50">Loading…</div>
            <div v-else-if="!debugInfo?.lines.length" class="text-base-content/50">
              No log entries yet.
            </div>
            <pre
              v-else
              class="whitespace-pre-wrap break-all"
            >{{ debugInfo.lines.join('\n') }}</pre>
          </div>
        </div>
      </div>

      <div class="modal-action">
        <button type="button" class="btn btn-sm" @click="closeDebug">Close</button>
      </div>
    </div>
  </div>
</template>
