<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onEvent } from '../transport/events'
import { resyncing, resyncNotice } from '../transport/resync'
import { undoToast, runUndoToast, clearUndoToast } from '../transport/undo'

interface ScanProgress {
  processed: number
  total: number
  root: string
}

interface ScanDone {
  added: number
  cancelled: boolean
  error?: string
  intensity?: ScanIntensity
}

interface ScanStarted {
  id: string
  root: string
  intensity?: ScanIntensity
}

type ScanIntensity = 'normal' | 'low'

// baseMessage holds the last event-driven status line (scans, library
// counts). The resync layer can briefly override it without losing
// context — when the notice fades we fall right back to whatever the
// base was.
const baseMessage = ref('Idle')
const photoCount = ref<number | null>(null)
const scanProcessed = ref(0)
const scanTotal = ref(0)

// Resync takes priority over the base message because it's the most
// immediate feedback for a user-triggered action. An active resync
// ("Syncing…") beats a stale notice; a fresh notice beats the base.
const message = computed(() => {
  if (resyncing.value) return 'Syncing library…'
  if (resyncNotice.value) return resyncNotice.value
  return baseMessage.value
})

// working drives the accent state: any long-lived disk operation the
// user kicked off. flashOk is a short pulse of green right after one
// finishes successfully, independent of working so CSS cross-fades
// cleanly.
const scanRunning = ref(false)
// scanIntensity distinguishes a user-kicked foreground scan from the
// ambient background rescan. The ambient case must feel reassuring
// (not "loading"), so we suppress the progress bar and pulsing dot
// and swap in a quieter message.
const scanIntensity = ref<ScanIntensity>('normal')
const isLowIntensity = computed(() => scanRunning.value && scanIntensity.value === 'low')
const working = computed(
  () => (scanRunning.value && scanIntensity.value === 'normal') || resyncing.value,
)
const flashOk = ref(false)
const flashDurationMs = 1200
let flashTimer: number | null = null

const scanPct = computed(() => {
  if (!scanRunning.value || scanTotal.value <= 0) return null
  if (scanIntensity.value === 'low') return null
  return Math.min(100, Math.round((scanProcessed.value / scanTotal.value) * 100))
})

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
  return !text.startsWith('Synced') && !text.startsWith('Library')
}

const unsubs: Array<() => void> = []

onMounted(() => {
  unsubs.push(
    onEvent('scan:started', (payload) => {
      const p = payload as ScanStarted
      scanIntensity.value = p.intensity === 'low' ? 'low' : 'normal'
    }),
  )
  unsubs.push(
    onEvent('scan:progress', (payload) => {
      const p = payload as ScanProgress
      scanRunning.value = true
      scanProcessed.value = p.processed
      scanTotal.value = p.total
      if (scanIntensity.value === 'low') {
        baseMessage.value = `Syncing in background — ${p.processed.toLocaleString()} checked`
      } else if (p.total < 0) {
        baseMessage.value = `Scanning — ${p.processed.toLocaleString()} found`
      } else {
        baseMessage.value = `Scanning`
      }
    }),
  )
  unsubs.push(
    onEvent('scan:done', (payload) => {
      const p = payload as ScanDone
      const wasLow = scanIntensity.value === 'low'
      scanRunning.value = false
      scanProcessed.value = 0
      scanTotal.value = 0
      scanIntensity.value = 'normal'
      if (p.error) {
        baseMessage.value = `Scan failed — ${p.error}`
      } else if (p.cancelled) {
        // A cancelled low-intensity sweep is expected (user-activity
        // preemption) and shouldn't look like a failure. Quietly fall
        // back to whatever we were showing before.
        if (wasLow) {
          baseMessage.value = 'Ready'
        } else {
          baseMessage.value = `Scan cancelled — ${p.added.toLocaleString()} photos saved`
        }
      } else if (wasLow) {
        // Background sweep finished — no celebratory pulse. Just note
        // any new arrivals briefly, then the next library:updated
        // event returns us to 'Ready'.
        if (p.added > 0) {
          baseMessage.value = `Background sync — ${p.added.toLocaleString()} new`
        } else {
          baseMessage.value = 'Ready'
        }
      } else {
        baseMessage.value = `Scan complete — ${p.added.toLocaleString()} photos`
        pulseFlashOk()
      }
    }),
  )
  unsubs.push(
    onEvent('library:updated', (payload) => {
      const { count } = (payload as { count: number }) ?? { count: 0 }
      photoCount.value = count
      if (!baseMessage.value.startsWith('Scan')) {
        baseMessage.value = 'Ready'
      }
    }),
  )
})

onBeforeUnmount(() => {
  for (const u of unsubs) u()
})
</script>

<template>
  <div
    class="status-bar"
    :class="{
      'status-bar--working': working,
      'status-bar--ambient': isLowIntensity,
      'status-bar--flash-ok': flashOk && !working,
    }"
    aria-live="polite"
  >
    <span class="status-bar__dot" aria-hidden="true" />
    <span
      class="status-bar__message"
      :title="isLowIntensity ? 'Kestrel checks your folders for new or removed photos when you’re idle. It runs at low priority and won’t block anything you do — you can close Kestrel any time.' : undefined"
    >{{ message }}</span>
    <span
      v-if="isLowIntensity"
      class="status-bar__hint"
      aria-label="Background sync information"
    >· safe to close or keep using Kestrel</span>

    <span v-if="scanPct !== null" class="status-bar__progress" aria-hidden="true">
      <span class="status-bar__progress-track">
        <span
          class="status-bar__progress-fill"
          :style="{ width: scanPct + '%' }"
        />
      </span>
      <span class="status-bar__progress-label">{{ scanPct }}%</span>
    </span>

    <span class="status-bar__spacer" aria-hidden="true" />

    <span v-if="photoCount !== null" class="status-bar__count">
      {{ photoCount.toLocaleString() }}<span class="status-bar__count-unit">&nbsp;photos</span>
    </span>

    <span v-if="undoToast" class="status-bar__toast" role="status">
      <span class="status-bar__toast-msg">{{ undoToast.message }}</span>
      <button
        class="status-bar__toast-undo"
        type="button"
        :disabled="undoToast.busy"
        @click="runUndoToast"
      >
        {{ undoToast.busy ? '…' : 'Undo' }}
      </button>
      <button
        class="status-bar__toast-dismiss"
        type="button"
        aria-label="Dismiss"
        @click="clearUndoToast"
      >✕</button>
    </span>
  </div>
</template>

<style scoped>
.status-bar {
  width: 100%;
  display: flex;
  align-items: center;
  gap: var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-caption);
  font-family: var(--font-mono);
  letter-spacing: 0;
  transition: color var(--dur-base) var(--ease-out);
}

.status-bar__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-full);
  background: var(--text-faint);
  flex-shrink: 0;
  transition: background var(--dur-base) var(--ease-out),
              box-shadow var(--dur-base) var(--ease-out);
}

.status-bar--working .status-bar__dot {
  background: var(--accent);
  box-shadow: 0 0 0 2px var(--accent-wash),
              0 0 8px var(--accent-glow);
  animation: status-pulse 1.6s ease-in-out infinite;
}
.status-bar--flash-ok .status-bar__dot {
  background: var(--success);
  box-shadow: 0 0 0 2px rgba(75, 178, 110, 0.18);
}

/* Ambient background-sync state. Deliberately understated: no pulse,
   no accent glow — just a soft dot and muted copy that says "I'm
   doing something, but you're not waiting on me." */
.status-bar--ambient .status-bar__dot {
  background: var(--text-faint);
  box-shadow: 0 0 0 2px var(--surface-active);
  animation: none;
}
.status-bar--ambient .status-bar__message {
  color: var(--text-secondary);
}
.status-bar__hint {
  color: var(--text-muted);
  font-family: var(--font-sans);
  font-size: var(--fs-caption);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@keyframes status-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.55; }
}

.status-bar__message {
  font-family: var(--font-sans);
  font-size: var(--fs-small);
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.status-bar--working .status-bar__message { color: var(--text-primary); }
.status-bar--flash-ok .status-bar__message { color: var(--success); }

.status-bar__progress {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  flex-shrink: 0;
}
.status-bar__progress-track {
  width: 120px;
  height: 2px;
  background: var(--surface-active);
  border-radius: var(--radius-full);
  overflow: hidden;
  position: relative;
}
.status-bar__progress-fill {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: var(--radius-full);
  transition: width var(--dur-slow) var(--ease-out);
}
.status-bar__progress-label {
  color: var(--accent);
  font-size: var(--fs-micro);
  font-variant-numeric: tabular-nums;
  min-width: 28px;
  text-align: right;
}

.status-bar__spacer { flex: 1; }

.status-bar__count {
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-caption);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}
.status-bar__count-unit {
  color: var(--text-muted);
  font-family: var(--font-sans);
}

.status-bar__toast {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  margin-left: var(--space-4);
  padding: 2px var(--space-3);
  background: var(--surface-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  box-shadow: var(--elev-popover, 0 4px 12px rgba(0,0,0,0.35));
  font-family: var(--font-sans);
  font-size: var(--fs-caption);
  color: var(--text-primary);
  max-width: 420px;
  animation: toast-rise var(--dur-base) var(--ease-out);
}
@keyframes toast-rise {
  from { transform: translateY(4px); opacity: 0; }
  to   { transform: translateY(0); opacity: 1; }
}
.status-bar__toast-msg {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status-bar__toast-undo {
  height: 20px;
  padding: 0 var(--space-3);
  background: var(--accent);
  color: #0A0A0B;
  border: none;
  border-radius: var(--radius-sm);
  font: var(--fw-semibold) var(--fs-micro) / 1 var(--font-sans);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  cursor: pointer;
  flex-shrink: 0;
  transition: background var(--dur-fast) var(--ease-out);
}
.status-bar__toast-undo:hover:not(:disabled) { background: var(--accent-hover); }
.status-bar__toast-undo:disabled { opacity: 0.5; cursor: wait; }
.status-bar__toast-dismiss {
  width: 18px;
  height: 18px;
  padding: 0;
  background: transparent;
  color: var(--text-muted);
  border: none;
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-size: 10px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color var(--dur-fast) var(--ease-out);
}
.status-bar__toast-dismiss:hover { color: var(--text-primary); }
</style>
