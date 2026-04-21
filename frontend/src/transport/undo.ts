// Transient undo-toast state. Mirrors resync.ts's pattern of a
// singleton ref that multiple islands read from, but extends it with
// an action callback so the toast can actually trigger an undo.
//
// The StatusBar island renders the toast; any code that mutates user
// data can call showUndoToast() to offer one-click reversal for a
// short window.

import { ref } from 'vue'

export interface UndoToast {
  message: string
  action: () => Promise<void> | void
  // Set while the undo action is running so the button can show a
  // spinner / be disabled.
  busy: boolean
}

export const undoToast = ref<UndoToast | null>(null)

// The window is generous enough for the user to read the summary and
// reach the button without being punished for a beat of hesitation.
const durationMs = 6500
let dismissTimer: number | null = null

export function showUndoToast(message: string, action: () => Promise<void> | void): void {
  if (dismissTimer !== null) window.clearTimeout(dismissTimer)
  undoToast.value = { message, action, busy: false }
  dismissTimer = window.setTimeout(() => {
    undoToast.value = null
    dismissTimer = null
  }, durationMs)
}

export function clearUndoToast(): void {
  if (dismissTimer !== null) window.clearTimeout(dismissTimer)
  dismissTimer = null
  undoToast.value = null
}

export async function runUndoToast(): Promise<void> {
  const t = undoToast.value
  if (!t || t.busy) return
  t.busy = true
  try {
    await t.action()
  } finally {
    clearUndoToast()
  }
}
