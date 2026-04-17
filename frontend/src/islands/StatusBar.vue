<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { onEvent } from '../transport/events'

interface ScanProgress {
  processed: number
  total: number
  root: string
}

const message = ref('Idle')
const unsubs: Array<() => void> = []

interface ScanDone {
  added: number
  cancelled: boolean
  error?: string
}

onMounted(() => {
  unsubs.push(
    onEvent('scan:progress', (payload) => {
      const p = payload as ScanProgress
      if (p.total < 0) {
        message.value = `Scanning… ${p.processed}`
      }
      // Terminal scan:progress events (total ≥ 0) are shadowed by
      // scan:done below, which knows whether the scan was cancelled.
    }),
  )
  unsubs.push(
    onEvent('scan:done', (payload) => {
      const p = payload as ScanDone
      if (p.error) {
        message.value = `Scan failed: ${p.error}`
      } else if (p.cancelled) {
        message.value = `Scan cancelled — ${p.added} photos saved`
      } else {
        message.value = `Scan complete — ${p.added} photos`
      }
    }),
  )
  unsubs.push(
    onEvent('library:updated', (payload) => {
      const { count } = (payload as { count: number }) ?? { count: 0 }
      // Only override the status line when we don't have a more
      // specific scan message already showing.
      if (!message.value.startsWith('Scan')) {
        message.value = `Library — ${count} photos`
      }
    }),
  )
})

onBeforeUnmount(() => {
  for (const u of unsubs) u()
})
</script>

<template>
  <footer class="status-bar">{{ message }}</footer>
</template>

<style scoped>
.status-bar {
  color: var(--text-secondary);
  font-size: var(--fs-body);
}
</style>
