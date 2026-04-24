// Cross-island state for the "Re-scan" action. PhotoGrid owns the
// trigger button; StatusBar shows progress and the result. Both
// import the same refs so there's no event bus to keep in sync —
// Vue's reactivity handles the fan-out.
//
// Like selection.ts, modules shared across island entries resolve to
// a single runtime instance under Vite's multi-entry build, so these
// refs are effectively singletons.

import { ref } from 'vue'
import { apiPost, friendlyError } from './api'
import { onEvent } from './events'
import { selectedFolder } from './selection'

// True while a rescan sweep is in flight (request sent, not yet
// finished on the server). Drives the "Syncing…" status line and
// disables the trigger button. Cleared when the trailing rescan:done
// event arrives over the WebSocket.
export const resyncing = ref(false)

// Human-readable result of the last completed rescan. Cleared after
// noticeDurationMs so the status bar quietly returns to its default
// library message. null means "nothing to announce".
export const resyncNotice = ref<string | null>(null)

const noticeDurationMs = 3000
let noticeTimer: number | null = null

function announce(message: string) {
  resyncNotice.value = message
  if (noticeTimer !== null) window.clearTimeout(noticeTimer)
  noticeTimer = window.setTimeout(() => {
    resyncNotice.value = null
    noticeTimer = null
  }, noticeDurationMs)
}

interface RescanDone {
  roots: number
  pruned: number
  requested: number
}

// Subscribe once — listeners set up here persist for the lifetime of
// the page, which is exactly the lifetime of the resyncing ref they
// mutate.
onEvent('rescan:done', (payload) => {
  if (!resyncing.value) return
  resyncing.value = false
  const p = payload as RescanDone | null
  if (!p) {
    announce('Re-scan complete.')
    return
  }
  if (p.roots === 0) {
    announce('No folders to re-scan.')
    return
  }
  const rootsWord = p.roots === 1 ? 'folder' : 'folders'
  if (p.pruned === 0) {
    announce(`Re-scanned ${p.roots} ${rootsWord}.`)
  } else {
    announce(`Re-scanned ${p.roots} ${rootsWord} — ${p.pruned} removed.`)
  }
})

interface RescanResponse {
  roots: string[]
}

// runResync kicks off a high-priority backend rescan of the currently
// selected folder — or every watched root when no folder is selected.
// Guarded so double-clicks coalesce; the trailing rescan:done event
// flips resyncing back to false.
export async function runResync(): Promise<void> {
  if (resyncing.value) return
  resyncing.value = true
  try {
    const res = await apiPost<RescanResponse>('/api/rescan', {
      folder: selectedFolder.value ?? '',
    })
    if (res.roots.length === 0) {
      resyncing.value = false
      announce('No watched folders to re-scan.')
    }
  } catch (err) {
    resyncing.value = false
    announce(friendlyError(err))
  }
}
