<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onEvent } from '../transport/events'
import { resyncing, resyncNotice } from '../transport/resync'
import { undoToast, runUndoToast, clearUndoToast } from '../transport/undo'

interface ScanProgress { processed: number; total: number; root: string }
interface ScanDone { added: number; cancelled: boolean; error?: string; intensity?: ScanIntensity }
interface ScanStarted { id: string; root: string; intensity?: ScanIntensity }
type ScanIntensity = 'normal' | 'low'

const baseMessage = ref('Idle')
const photoCount = ref<number | null>(null)
const scanProcessed = ref(0)
const scanTotal = ref(0)

const message = computed(() => {
  if (resyncing.value) return 'Syncing library…'
  if (resyncNotice.value) return resyncNotice.value
  return baseMessage.value
})

const scanRunning = ref(false)
const scanIntensity = ref<ScanIntensity>('normal')
const isLowIntensity = computed(() => scanRunning.value && scanIntensity.value === 'low')
const working = computed(
  () => (scanRunning.value && scanIntensity.value === 'normal') || resyncing.value,
)
const flashOk = ref(false)
let flashTimer: number | null = null

const scanPct = computed(() => {
  if (!scanRunning.value || scanTotal.value <= 0) return null
  if (scanIntensity.value === 'low') return null
  return Math.min(100, Math.round((scanProcessed.value / scanTotal.value) * 100))
})

const dotClass = computed(() => {
  if (flashOk.value && !working.value) return 'bg-success'
  if (working.value) return 'bg-primary animate-pulse'
  if (isLowIntensity.value) return 'bg-base-content/30'
  return 'bg-base-content/30'
})

function pulseFlashOk() {
  flashOk.value = true
  if (flashTimer !== null) window.clearTimeout(flashTimer)
  flashTimer = window.setTimeout(() => {
    flashOk.value = false
    flashTimer = null
  }, 1200)
}

watch(resyncing, (now, prev) => {
  if (prev && !now && !isErrorNotice(resyncNotice.value)) pulseFlashOk()
})

function isErrorNotice(text: string | null): boolean {
  if (!text) return false
  return !text.startsWith('Synced') && !text.startsWith('Library')
}

const unsubs: Array<() => void> = []

onMounted(() => {
  unsubs.push(onEvent('scan:started', (payload) => {
    const p = payload as ScanStarted
    scanIntensity.value = p.intensity === 'low' ? 'low' : 'normal'
  }))
  unsubs.push(onEvent('scan:progress', (payload) => {
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
  }))
  unsubs.push(onEvent('scan:done', (payload) => {
    const p = payload as ScanDone
    const wasLow = scanIntensity.value === 'low'
    scanRunning.value = false
    scanProcessed.value = 0
    scanTotal.value = 0
    scanIntensity.value = 'normal'
    if (p.error) {
      baseMessage.value = `Scan failed — ${p.error}`
    } else if (p.cancelled) {
      baseMessage.value = wasLow ? 'Ready' : `Scan cancelled — ${p.added.toLocaleString()} photos saved`
    } else if (wasLow) {
      baseMessage.value = p.added > 0 ? `Background sync — ${p.added.toLocaleString()} new` : 'Ready'
    } else {
      baseMessage.value = `Scan complete — ${p.added.toLocaleString()} photos`
      pulseFlashOk()
    }
  }))
  unsubs.push(onEvent('library:updated', (payload) => {
    const { count } = (payload as { count: number }) ?? { count: 0 }
    photoCount.value = count
    if (!baseMessage.value.startsWith('Scan')) baseMessage.value = 'Ready'
  }))
})

onBeforeUnmount(() => { for (const u of unsubs) u() })
</script>

<template>
  <div class="flex w-full items-center gap-3 font-mono text-xs" aria-live="polite">
    <span :class="['inline-block h-2 w-2 shrink-0 rounded-full', dotClass]" aria-hidden="true" />
    <span
      class="truncate font-sans text-xs"
      :class="{ 'text-success': flashOk && !working, 'text-base-content': working }"
      :title="isLowIntensity ? 'Kestrel checks your folders for new or removed photos when you’re idle. It runs at low priority and won’t block anything you do — you can close Kestrel any time.' : undefined"
    >{{ message }}</span>
    <span v-if="isLowIntensity" class="truncate font-sans text-base-content/50">
      · safe to close or keep using Kestrel
    </span>

    <div v-if="scanPct !== null" class="flex items-center gap-2">
      <progress class="progress progress-primary w-32 h-1" :value="scanPct" max="100" />
      <span class="text-primary tabular-nums w-8 text-right">{{ scanPct }}%</span>
    </div>

    <span class="flex-1" aria-hidden="true" />

    <span v-if="photoCount !== null" class="tabular-nums">
      {{ photoCount.toLocaleString() }}<span class="font-sans text-base-content/50">&nbsp;photos</span>
    </span>

    <div v-if="undoToast" class="alert alert-sm ml-2 py-1" role="status">
      <span class="truncate font-sans max-w-80">{{ undoToast.message }}</span>
      <button
        type="button"
        class="btn btn-primary btn-xs"
        :disabled="undoToast.busy"
        @click="runUndoToast"
      >
        {{ undoToast.busy ? '…' : 'Undo' }}
      </button>
      <button
        type="button"
        class="btn btn-ghost btn-xs btn-square"
        aria-label="Dismiss"
        @click="clearUndoToast"
      >✕</button>
    </div>
  </div>
</template>
