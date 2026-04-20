<script setup lang="ts">
import { computed, defineAsyncComponent, ref } from 'vue'
import { apiPost, friendlyError } from '../transport/api'
import type { Photo } from '../types'

// SelectionSummary is the side panel's multi-selection view: it shows
// aggregate metadata for the currently selected photos and lets the
// user apply tags to all of them in one shot. Single-photo details
// still live in PhotoViewer; PhotoGrid picks which to mount based on
// selection size.
const TagInput = defineAsyncComponent(() => import('./TagInput.vue'))

const props = defineProps<{
  photos: Photo[]
}>()
const emit = defineEmits<{
  (e: 'clear'): void
}>()

const count = computed(() => props.photos.length)
const totalBytes = computed(() =>
  props.photos.reduce((sum, p) => sum + (p.SizeBytes || 0), 0),
)
const totalSizeLabel = computed(() => formatBytes(totalBytes.value))

// Tags the whole selection already has in common. Useful info, but
// also the starting point users expect when adding more: seeing
// "vacation" already there means they won't re-type it.
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
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
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
    const res = await apiPost<{ updated: number }>(
      '/api/tags/bulk',
      { paths, tags },
    )
    lastApplied.value = res.updated === 0
      ? 'Tags already applied.'
      : `Added to ${res.updated} photo${res.updated === 1 ? '' : 's'}.`
    // library:updated refires the /api/photos call, which brings the
    // canonical merged tags back in. Clear the TagInput so the user
    // can follow up with another batch without manually wiping it.
    pendingTags.value = []
  } catch (err) {
    actionError.value = friendlyError(err)
  } finally {
    applying.value = false
  }
}
</script>

<template>
  <aside class="summary" aria-label="Selection summary">
    <header class="summary__head">
      <button class="summary__close" aria-label="Clear selection" @click="emit('clear')">✕</button>
      <h2 class="summary__title">{{ count }} selected</h2>
    </header>

    <dl class="summary__meta">
      <div class="summary__row">
        <dt>Photos</dt>
        <dd>{{ count }}</dd>
      </div>
      <div class="summary__row">
        <dt>Total size</dt>
        <dd>{{ totalSizeLabel }}</dd>
      </div>
    </dl>

    <section class="summary__section" aria-label="Common tags">
      <h3>Shared tags</h3>
      <p v-if="commonTags.length === 0" class="summary__muted">No tags in common.</p>
      <ul v-else class="summary__pills">
        <li v-for="tag in commonTags" :key="tag" class="summary__pill">{{ tag }}</li>
      </ul>
    </section>

    <section class="summary__section" aria-label="Add tags to selection">
      <h3>Add tags to all</h3>
      <TagInput
        :model-value="pendingTags"
        placeholder="Add tag…"
        aria-label="Tags to apply to the selection"
        @update:model-value="applyTags"
      />
      <p v-if="applying" class="summary__muted">Applying…</p>
      <p v-else-if="actionError" class="summary__muted summary__error" role="alert">
        {{ actionError }}
      </p>
      <p v-else-if="lastApplied" class="summary__muted" role="status">{{ lastApplied }}</p>
    </section>
  </aside>
</template>

<style scoped>
.summary {
  width: 320px;
  flex-shrink: 0;
  background: var(--surface-raised);
  color: var(--text-primary);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  font-size: var(--fs-small);
}
.summary__head {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.summary__close {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  background: transparent;
  color: var(--text-muted);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.summary__close:hover {
  color: var(--text-primary);
  border-color: var(--border-strong);
  background: var(--surface-hover);
}
.summary__title {
  margin: 0;
  font: var(--fw-semibold) var(--fs-subhead) / var(--lh-tight) var(--font-sans);
  letter-spacing: var(--tracking-tight);
}

.summary__meta {
  margin: 0;
  padding: var(--space-5);
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  border-bottom: 1px solid var(--border-subtle);
}
.summary__row {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: var(--space-3);
}
.summary__row dt {
  color: var(--text-muted);
  font-size: var(--fs-micro);
  font-weight: var(--fw-semibold);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  margin: 0;
}
.summary__row dd {
  margin: 0;
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-small);
  font-variant-numeric: tabular-nums;
}

.summary__section {
  padding: var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.summary__section:last-child { border-bottom: none; }
.summary__section h3 {
  margin: 0;
  font-size: var(--fs-micro);
  font-weight: var(--fw-semibold);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  color: var(--text-muted);
}
.summary__muted {
  color: var(--text-muted);
  font-size: var(--fs-small);
  margin: 0;
}
.summary__error { color: var(--danger); }

.summary__pills {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.summary__pill {
  background: var(--accent-wash);
  color: var(--accent);
  border-radius: var(--radius-xs);
  padding: 0 var(--space-3);
  height: 18px;
  display: inline-flex;
  align-items: center;
  font-size: var(--fs-micro);
  font-weight: var(--fw-medium);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  line-height: 1;
}
</style>
