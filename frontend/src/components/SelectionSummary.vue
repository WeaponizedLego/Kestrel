<script setup lang="ts">
import { computed, defineAsyncComponent, ref } from 'vue'
import { apiPost, friendlyError } from '../transport/api'
import type { Photo } from '../types'

const TagInput = defineAsyncComponent(() => import('./TagInput.vue'))

const props = defineProps<{ photos: Photo[] }>()
const emit = defineEmits<{ (e: 'clear'): void }>()

const count = computed(() => props.photos.length)
const totalBytes = computed(() => props.photos.reduce((sum, p) => sum + (p.SizeBytes || 0), 0))
const totalSizeLabel = computed(() => formatBytes(totalBytes.value))

const commonTags = computed<string[]>(() => {
  if (props.photos.length === 0) return []
  const [first, ...rest] = props.photos
  const intersect = new Set(first.Tags ?? [])
  for (const p of rest) {
    const have = new Set(p.Tags ?? [])
    for (const t of intersect) if (!have.has(t)) intersect.delete(t)
    if (intersect.size === 0) break
  }
  return Array.from(intersect).sort()
})

function formatBytes(n: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

const pendingTags = ref<string[]>([])
const applying = ref(false)
const actionError = ref<string | null>(null)
const lastApplied = ref<string | null>(null)

async function applyTags(tags: string[]) {
  pendingTags.value = tags
  if (tags.length === 0 || props.photos.length === 0) return
  applying.value = true
  actionError.value = null
  try {
    const paths = props.photos.map((p) => p.Path)
    const res = await apiPost<{ updated: number }>('/api/tags/bulk', { paths, tags })
    lastApplied.value = res.updated === 0
      ? 'Tags already applied.'
      : `Added to ${res.updated} photo${res.updated === 1 ? '' : 's'}.`
    pendingTags.value = []
  } catch (err) {
    actionError.value = friendlyError(err)
  } finally {
    applying.value = false
  }
}
</script>

<template>
  <aside
    class="card bg-base-200 border border-base-300 w-80 shrink-0 flex-col overflow-hidden text-sm flex"
    aria-label="Selection summary"
  >
    <div class="flex items-center gap-2 border-b border-base-300 px-4 py-3">
      <button type="button" class="btn btn-xs btn-square btn-ghost" aria-label="Clear selection" @click="emit('clear')">✕</button>
      <h2 class="m-0 text-base font-semibold">{{ count }} selected</h2>
    </div>

    <dl class="flex flex-col gap-3 border-b border-base-300 px-4 py-3">
      <div class="flex items-baseline justify-between">
        <dt class="text-[10px] font-semibold uppercase tracking-wider text-base-content/50 m-0">Photos</dt>
        <dd class="m-0 font-mono tabular-nums">{{ count }}</dd>
      </div>
      <div class="flex items-baseline justify-between">
        <dt class="text-[10px] font-semibold uppercase tracking-wider text-base-content/50 m-0">Total size</dt>
        <dd class="m-0 font-mono tabular-nums">{{ totalSizeLabel }}</dd>
      </div>
    </dl>

    <section class="flex flex-col gap-2 border-b border-base-300 px-4 py-3" aria-label="Common tags">
      <h3 class="m-0 text-[10px] font-semibold uppercase tracking-wider text-base-content/50">Shared tags</h3>
      <p v-if="commonTags.length === 0" class="m-0 text-xs text-base-content/50">No tags in common.</p>
      <div v-else class="flex flex-wrap gap-1">
        <span v-for="tag in commonTags" :key="tag" class="badge badge-primary badge-sm">{{ tag }}</span>
      </div>
    </section>

    <section class="flex flex-col gap-2 px-4 py-3" aria-label="Add tags to selection">
      <h3 class="m-0 text-[10px] font-semibold uppercase tracking-wider text-base-content/50">Add tags to all</h3>
      <TagInput
        :model-value="pendingTags"
        placeholder="Add tag…"
        aria-label="Tags to apply to the selection"
        @update:model-value="applyTags"
      />
      <p v-if="applying" class="m-0 text-xs text-base-content/50">Applying…</p>
      <p v-else-if="actionError" class="m-0 text-xs text-error" role="alert">{{ actionError }}</p>
      <p v-else-if="lastApplied" class="m-0 text-xs text-base-content/50" role="status">{{ lastApplied }}</p>
    </section>
  </aside>
</template>
