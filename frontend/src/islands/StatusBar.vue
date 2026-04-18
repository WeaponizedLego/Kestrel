<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onEvent } from '../transport/events'
import { resyncing, resyncNotice } from '../transport/resync'

interface ScanProgress {
  processed: number
  total: number
  root: string
}

interface ScanDone {
  added: number
  cancelled: boolean
  error?: string
}

// baseMessage holds the last event-driven status line (scans, library
// counts). The resync layer can briefly override it without losing
// context — when the notice fades we fall right back to whatever the
// base was.
const baseMessage = ref('Idle')

// Resync takes priority over the base message because it's the most
// immediate feedback for a user-triggered action. An active resync
// ("Syncing…") beats a stale notice; a fresh notice beats the base.
const message = computed(() => {
  if (resyncing.value) return 'Syncing…'
  if (resyncNotice.value) return resyncNotice.value
  return baseMessage.value
})

// working drives the "orange glow" state: any long-lived disk
// operation the user kicked off. flashOk is a short pulse of green
// right after one finishes successfully, independent of working so
// the CSS transition can cross-fade cleanly.
const scanRunning = ref(false)
const working = computed(() => scanRunning.value || resyncing.value)
const flashOk = ref(false)
const flashDurationMs = 1200
let flashTimer: number | null = null

function pulseFlashOk() {
  flashOk.value = true
  if (flashTimer !== null) window.clearTimeout(flashTimer)
  flashTimer = window.setTimeout(() => {
    flashOk.value = false
    flashTimer = null
  }, flashDurationMs)
}

// Resync: when the in-flight flag clears, we just finished. The
// notice string tells us success vs. error — only flash on success.
watch(resyncing, (now, prev) => {
  if (prev && !now && !isErrorNotice(resyncNotice.value)) pulseFlashOk()
})

function isErrorNotice(text: string | null): boolean {
  if (!text) return false
  // Our own announce() outputs either "Library in sync." / "Synced —
  // N removed." on success, or the friendly error string on failure.
  // Anything that isn't one of the two success shapes is an error.
  return !text.startsWith('Synced') && !text.startsWith('Library')
}

const unsubs: Array<() => void> = []

onMounted(() => {
  unsubs.push(
    onEvent('scan:progress', (payload) => {
      const p = payload as ScanProgress
      if (p.total < 0) {
        baseMessage.value = `Scanning… ${p.processed}`
        scanRunning.value = true
      }
      // Terminal scan:progress events (total ≥ 0) are shadowed by
      // scan:done below, which knows whether the scan was cancelled.
    }),
  )
  unsubs.push(
    onEvent('scan:done', (payload) => {
      const p = payload as ScanDone
      scanRunning.value = false
      if (p.error) {
        baseMessage.value = `Scan failed: ${p.error}`
      } else if (p.cancelled) {
        baseMessage.value = `Scan cancelled — ${p.added} photos saved`
      } else {
        baseMessage.value = `Scan complete — ${p.added} photos`
        pulseFlashOk()
      }
    }),
  )
  unsubs.push(
    onEvent('library:updated', (payload) => {
      const { count } = (payload as { count: number }) ?? { count: 0 }
      // Only override the status line when we don't have a more
      // specific scan message already showing.
      if (!baseMessage.value.startsWith('Scan')) {
        baseMessage.value = `Library — ${count} photos`
      }
    }),
  )
})

onBeforeUnmount(() => {
  for (const u of unsubs) u()
})
</script>

<template>
  <footer
    class="status-bar"
    :class="{
      'status-bar--working': working,
      'status-bar--flash-ok': flashOk && !working,
    }"
    aria-live="polite"
  >{{ message }}</footer>
</template>

<style scoped>
.status-bar {
  color: var(--text-secondary);
  font-size: var(--fs-body);
  transition:
    color var(--dur-base) var(--ease-out),
    text-shadow var(--dur-base) var(--ease-out);
}
.status-bar--working {
  color: var(--accent);
  text-shadow: 0 0 10px var(--accent-glow);
}
.status-bar--flash-ok {
  color: var(--success);
  text-shadow: 0 0 10px rgba(75, 178, 110, 0.5);
}
</style>
