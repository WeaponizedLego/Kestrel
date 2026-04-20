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
const working = computed(() => scanRunning.value || resyncing.value)
const flashOk = ref(false)
const flashDurationMs = 1200
let flashTimer: number | null = null

const scanPct = computed(() => {
  if (!scanRunning.value || scanTotal.value <= 0) return null
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
    onEvent('scan:progress', (payload) => {
      const p = payload as ScanProgress
      scanRunning.value = true
      scanProcessed.value = p.processed
      scanTotal.value = p.total
      if (p.total < 0) {
        baseMessage.value = `Scanning — ${p.processed.toLocaleString()} found`
      } else {
        baseMessage.value = `Scanning`
      }
    }),
  )
  unsubs.push(
    onEvent('scan:done', (payload) => {
      const p = payload as ScanDone
      scanRunning.value = false
      scanProcessed.value = 0
      scanTotal.value = 0
      if (p.error) {
        baseMessage.value = `Scan failed — ${p.error}`
      } else if (p.cancelled) {
        baseMessage.value = `Scan cancelled — ${p.added.toLocaleString()} photos saved`
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
      'status-bar--flash-ok': flashOk && !working,
    }"
    aria-live="polite"
  >
    <span class="status-bar__dot" aria-hidden="true" />
    <span class="status-bar__message">{{ message }}</span>

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
</style>
