<script setup lang="ts">
// FileOps orchestrates the destructive endpoints. The Toolbar fires
// requestMove/Delete/Undo events; this island listens, drives the
// folder picker (for move) and a confirmation modal (for delete),
// calls the API, and surfaces an undo toast on success.
//
// Design note: keeping all three flows in one island rather than
// split-per-action means we only register one set of event
// listeners and the confirmation modal + folder picker can be
// shared/swapped cleanly.

import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import FolderPicker from './FolderPicker.vue'
import DeleteConfirmation from './DeleteConfirmation.vue'
import { friendlyError } from '../transport/api'
import {
  apiDelete,
  apiMove,
  apiUndo,
  onRequestDelete,
  onRequestMove,
  onRequestUndo,
} from '../transport/fileops'
import { clearSelection } from '../transport/selection'
import { showUndoToast } from '../transport/undo'

const moveOpen = ref(false)
const movePaths = ref<string[]>([])
const moveBusy = ref(false)

const deleteOpen = ref(false)
const deletePaths = ref<string[]>([])
const deleteBusy = ref(false)

let unsubMove: (() => void) | null = null
let unsubDelete: (() => void) | null = null
let unsubUndo: (() => void) | null = null

onMounted(() => {
  unsubMove = onRequestMove(({ paths }) => {
    if (!paths.length) return
    movePaths.value = [...paths]
    moveOpen.value = true
  })
  unsubDelete = onRequestDelete(({ paths }) => {
    if (!paths.length) return
    deletePaths.value = [...paths]
    deleteOpen.value = true
  })
  unsubUndo = onRequestUndo(async () => {
    try {
      const res = await apiUndo()
      announce(`Undid ${res.results.length} item${res.results.length === 1 ? '' : 's'}.`, null)
    } catch (err) {
      announce(friendlyError(err), null)
    }
  })
})

onBeforeUnmount(() => {
  unsubMove?.()
  unsubDelete?.()
  unsubUndo?.()
})

async function onChooseMoveDestination(dest: string) {
  if (moveBusy.value) return
  moveBusy.value = true
  const paths = movePaths.value
  try {
    // Same-FS moves are near-instant and atomic; verify=false keeps
    // the cost low. The cross-FS path turns on verification on its
    // own regardless of this flag, so the user's data is always
    // protected when it's at risk.
    const res = await apiMove(paths, dest, false)
    if (res.moved > 0) {
      showUndoToast(
        `Moved ${res.moved} photo${res.moved === 1 ? '' : 's'}.`,
        async () => {
          await apiUndo()
        },
      )
      clearSelection()
    }
    if (res.failed > 0) {
      announce(`${res.failed} failed to move.`, firstError(res.results))
    }
  } catch (err) {
    announce(friendlyError(err), null)
  } finally {
    moveBusy.value = false
    moveOpen.value = false
    movePaths.value = []
    // Let the modal unmount cleanly before anything else reacts.
    await nextTick()
  }
}

async function onConfirmDelete(permanent: boolean) {
  if (deleteBusy.value) return
  deleteBusy.value = true
  const paths = deletePaths.value
  try {
    const res = await apiDelete(paths, permanent)
    if (res.deleted > 0) {
      if (!permanent) {
        showUndoToast(
          `Moved ${res.deleted} photo${res.deleted === 1 ? '' : 's'} to trash.`,
          async () => {
            await apiUndo()
          },
        )
      } else {
        announce(`Permanently deleted ${res.deleted} photo${res.deleted === 1 ? '' : 's'}.`, null)
      }
      clearSelection()
    }
    if (res.failed > 0) {
      announce(`${res.failed} failed to delete.`, firstError(res.results))
    }
  } catch (err) {
    announce(friendlyError(err), null)
  } finally {
    deleteBusy.value = false
    deleteOpen.value = false
    deletePaths.value = []
    await nextTick()
  }
}

function firstError(results: { path: string; success: boolean; error?: string }[]): string | null {
  for (const r of results) {
    if (!r.success && r.error) return r.error
  }
  return null
}

// announce is a minimal inline toast for non-undoable results. For
// the undo flows we use showUndoToast; for plain status we reuse
// the same showUndoToast shape with a no-op action so StatusBar
// only needs one renderer.
function announce(message: string, detail: string | null) {
  const full = detail ? `${message} (${detail})` : message
  showUndoToast(full, () => {
    /* plain informational toast — no-op undo */
  })
}
</script>

<template>
  <div>
    <FolderPicker
      v-if="moveOpen"
      @choose="onChooseMoveDestination"
      @close="moveOpen = false"
    />
    <DeleteConfirmation
      v-if="deleteOpen"
      :count="deletePaths.length"
      :sample-paths="deletePaths.slice(0, 3)"
      :busy="deleteBusy"
      @confirm="onConfirmDelete"
      @close="deleteOpen = false"
    />
  </div>
</template>
