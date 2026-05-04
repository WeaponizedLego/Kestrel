<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onEvent } from '../transport/events'

interface ScanStarted { id: string; root: string; intensity?: 'normal' | 'low' }
interface ScanProgress { processed: number; total: number; root: string }
interface ScanDone { id: string; root: string; added: number; cancelled: boolean; intensity?: 'normal' | 'low' }
interface DirsFound { root: string; paths: string[]; total: number }
interface WorkerStatus { id: number; current: string; kind: 'idle' | 'walking' | 'hashing' }

const open = ref(false)
const root = ref('')
const processed = ref(0)
const total = ref(-1)
const dirsFound = ref(0)
const recentDirs = ref<string[]>([])
const workers = ref<WorkerStatus[]>([])
const done = ref(false)
const added = ref(0)
const cancelled = ref(false)

const RECENT_LIMIT = 200

const percent = computed(() => {
  if (done.value) return 100
  if (total.value <= 0) return -1
  return Math.min(100, Math.round((processed.value / total.value) * 100))
})

const heading = computed(() => {
  if (done.value) return cancelled.value ? 'Scan cancelled' : 'Scan complete'
  return 'Scanning'
})

const intensity = ref<'normal' | 'low'>('normal')

function reset() {
  processed.value = 0
  total.value = -1
  dirsFound.value = 0
  recentDirs.value = []
  workers.value = []
  done.value = false
  added.value = 0
  cancelled.value = false
}

const unsubs: Array<() => void> = []

onMounted(() => {
  unsubs.push(onEvent('scan:started', (raw) => {
    const ev = raw as ScanStarted
    intensity.value = ev.intensity ?? 'normal'
    // Only surface the modal for full-intensity user-triggered scans —
    // background watcher rescans should be invisible.
    if (intensity.value !== 'normal') return
    reset()
    root.value = ev.root
    open.value = true
  }))
  unsubs.push(onEvent('scan:progress', (raw) => {
    const ev = raw as ScanProgress
    processed.value = ev.processed
    total.value = ev.total
  }))
  unsubs.push(onEvent('scan:dirs-found', (raw) => {
    const ev = raw as DirsFound
    if (!Array.isArray(ev?.paths)) return
    dirsFound.value += ev.paths.length
    // Keep a sliding window of the most recent paths.
    const next = recentDirs.value.concat(ev.paths)
    if (next.length > RECENT_LIMIT) next.splice(0, next.length - RECENT_LIMIT)
    recentDirs.value = next
  }))
  unsubs.push(onEvent('scan:workers', (raw) => {
    if (Array.isArray(raw)) workers.value = raw as WorkerStatus[]
  }))
  unsubs.push(onEvent('scan:done', (raw) => {
    const ev = raw as ScanDone
    if (ev.root && ev.root !== root.value) return
    done.value = true
    added.value = ev.added ?? 0
    cancelled.value = !!ev.cancelled
    // If the scan reported a final processed=total burst we may have
    // missed it; clamp processed to total so the bar reads 100%.
    if (total.value > 0) processed.value = total.value
    workers.value = workers.value.map((w) => ({ ...w, current: '', kind: 'idle' }))
  }))
})

onBeforeUnmount(() => {
  for (const u of unsubs) u()
})

function close() {
  open.value = false
}

function shortPath(p: string): string {
  if (p.length <= 64) return p
  return '…' + p.slice(p.length - 63)
}
</script>

<template>
  <dialog :class="['modal', { 'modal-open': open }]">
    <div class="modal-box max-w-3xl">
      <h3 class="text-lg font-bold">{{ heading }}</h3>
      <p class="mt-1 text-xs opacity-70 break-all">{{ root }}</p>
      <p v-if="done" class="mt-1 text-xs opacity-70">
        {{ added.toLocaleString() }} files added{{ cancelled ? ' (cancelled)' : '' }}.
      </p>

      <div class="stats stats-horizontal mt-4 w-full shadow">
        <div class="stat">
          <div class="stat-title text-xs">Folders found</div>
          <div class="stat-value text-2xl">{{ dirsFound.toLocaleString() }}</div>
        </div>
        <div class="stat">
          <div class="stat-title text-xs">Files processed</div>
          <div class="stat-value text-2xl">{{ processed.toLocaleString() }}</div>
        </div>
        <div class="stat">
          <div class="stat-title text-xs">Files discovered</div>
          <div class="stat-value text-2xl">{{ total >= 0 ? total.toLocaleString() : '…' }}</div>
        </div>
      </div>

      <div class="mt-4">
        <progress
          class="progress progress-primary w-full"
          :value="percent >= 0 ? percent : undefined"
          max="100"
        ></progress>
      </div>

      <div class="mt-4">
        <div class="text-xs font-semibold uppercase opacity-70">Workers</div>
        <div class="mt-2 grid gap-2 sm:grid-cols-2">
          <div
            v-for="w in workers"
            :key="w.id"
            class="card card-compact bg-base-200"
          >
            <div class="card-body !py-2">
              <div class="flex items-center gap-2">
                <span class="badge badge-sm" :class="w.kind === 'idle' ? 'badge-ghost' : 'badge-primary'">
                  #{{ w.id }}
                </span>
                <span class="text-xs opacity-70">{{ w.kind }}</span>
              </div>
              <div class="truncate text-xs" :title="w.current">
                {{ w.current ? shortPath(w.current) : '—' }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="mt-4">
        <div class="text-xs font-semibold uppercase opacity-70">Recently discovered folders</div>
        <ul class="mt-2 max-h-40 overflow-auto rounded bg-base-200 p-2 text-xs">
          <li v-for="(p, i) in recentDirs.slice().reverse().slice(0, 50)" :key="i" class="truncate" :title="p">
            {{ shortPath(p) }}
          </li>
          <li v-if="recentDirs.length === 0" class="opacity-50">Waiting for the walker…</li>
        </ul>
      </div>

      <div class="modal-action">
        <button class="btn" :class="done ? 'btn-primary' : ''" @click="close">
          {{ done ? 'Done' : 'Close' }}
        </button>
      </div>
    </div>
    <form method="dialog" class="modal-backdrop">
      <button @click="close">close</button>
    </form>
  </dialog>
</template>
