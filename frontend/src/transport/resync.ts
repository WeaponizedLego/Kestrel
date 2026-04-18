// Cross-island state for the disk re-sync operation. PhotoGrid owns
// the trigger button; StatusBar shows progress and the result. Both
// import the same refs so there's no event bus to keep in sync —
// Vue's reactivity handles the fan-out.
//
// Like selection.ts, modules shared across island entries resolve to
// a single runtime instance under Vite's multi-entry build, so these
// refs are effectively singletons.

import { ref } from 'vue'
import { apiPost, friendlyError } from './api'

// True while a /api/resync call is in flight. Disables the trigger
// button and drives the "Syncing…" status line.
export const resyncing = ref(false)

// Human-readable result of the last completed resync. Cleared after
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

// runResync kicks off a backend resync and updates the shared refs.
// Callers don't need the return value; the result surfaces through
// resyncNotice. Guarded so double-clicks coalesce.
export async function runResync(): Promise<void> {
  if (resyncing.value) return
  resyncing.value = true
  try {
    const res = await apiPost<{ removed: number }>('/api/resync', {})
    announce(
      res.removed === 0
        ? 'Library in sync.'
        : `Synced — ${res.removed} removed.`,
    )
  } catch (err) {
    announce(friendlyError(err))
  } finally {
    resyncing.value = false
  }
}
