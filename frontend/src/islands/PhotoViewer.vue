<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { photoSrc } from '../transport/api'
import type { Photo } from '../types'

const props = defineProps<{ photo: Photo }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'prev'): void
  (e: 'next'): void
}>()

const src = computed(() => photoSrc(props.photo.Path))

const dims = computed(() =>
  props.photo.Width && props.photo.Height
    ? `${props.photo.Width} × ${props.photo.Height}`
    : '—',
)
const sizeLabel = computed(() => formatBytes(props.photo.SizeBytes))
const takenLabel = computed(() => formatDate(props.photo.TakenAt))
const modifiedLabel = computed(() => formatDate(props.photo.ModTime))

function formatBytes(n: number): string {
  const units = ['B', 'KB', 'MB', 'GB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

function formatDate(raw: string | undefined): string {
  // Zero-value time.Time from Go encodes as "0001-01-01T00:00:00Z".
  // Show an em-dash instead of a fake year-1 date.
  if (!raw || raw.startsWith('0001-')) return '—'
  const d = new Date(raw)
  return isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

function onKey(e: KeyboardEvent) {
  switch (e.key) {
    case 'Escape':
      emit('close')
      break
    case 'ArrowLeft':
      emit('prev')
      break
    case 'ArrowRight':
      emit('next')
      break
  }
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <aside class="panel" aria-label="Photo details">
    <header class="panel__head">
      <button class="panel__close" aria-label="Close details" @click="emit('close')">✕</button>
      <h2 class="panel__name" :title="photo.Name">{{ photo.Name }}</h2>
    </header>

    <div class="panel__preview">
      <img :src="src" :alt="photo.Name" />
    </div>

    <nav class="panel__nav">
      <button class="panel__nav-btn" aria-label="Previous photo" @click="emit('prev')">‹ Prev</button>
      <button class="panel__nav-btn" aria-label="Next photo" @click="emit('next')">Next ›</button>
    </nav>

    <dl class="panel__meta">
      <div class="panel__row">
        <dt>Dimensions</dt>
        <dd>{{ dims }}</dd>
      </div>
      <div class="panel__row">
        <dt>Size</dt>
        <dd>{{ sizeLabel }}</dd>
      </div>
      <div class="panel__row">
        <dt>Taken</dt>
        <dd>{{ takenLabel }}</dd>
      </div>
      <div class="panel__row">
        <dt>Modified</dt>
        <dd>{{ modifiedLabel }}</dd>
      </div>
      <div v-if="photo.CameraMake" class="panel__row">
        <dt>Camera</dt>
        <dd>{{ photo.CameraMake }}</dd>
      </div>
      <div class="panel__row panel__row--path">
        <dt>Path</dt>
        <dd :title="photo.Path">{{ photo.Path }}</dd>
      </div>
    </dl>

    <section class="panel__tags" aria-label="Tags">
      <h3>Tags</h3>
      <p class="panel__muted">Tag editing coming soon.</p>
    </section>
  </aside>
</template>

<style scoped>
.panel {
  width: 360px;
  min-width: 320px;
  max-width: 40vw;
  background: var(--surface-raised);
  color: var(--text-primary);
  box-shadow: var(--elev-raised);
  border-radius: var(--radius-md);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  flex-shrink: 0;
}

.panel__head {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--surface-inset);
  box-shadow: var(--elev-inset);
}
.panel__close {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  background: transparent;
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  cursor: pointer;
  font-size: var(--fs-body);
}
.panel__close:hover { border-color: var(--accent); }
.panel__name {
  margin: 0;
  font: var(--fw-medium) var(--fs-default) / 1.2 var(--font-sans);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.panel__preview {
  background: var(--surface-inset);
  padding: var(--space-3);
  display: flex;
  justify-content: center;
  align-items: center;
  max-height: 45vh;
  min-height: 200px;
}
.panel__preview img {
  max-width: 100%;
  max-height: 45vh;
  object-fit: contain;
}

.panel__nav {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border-bottom: var(--border-thin) solid var(--border-subtle);
}
.panel__nav-btn {
  flex: 1;
  background: transparent;
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-2);
  cursor: pointer;
  font: inherit;
}
.panel__nav-btn:hover { border-color: var(--accent); background: var(--surface-inset); }

.panel__meta {
  margin: 0;
  padding: var(--space-3) var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  overflow-y: auto;
}
.panel__row {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
  font-size: var(--fs-body);
}
.panel__row dt { color: var(--text-muted); font-weight: var(--fw-medium); flex-shrink: 0; }
.panel__row dd {
  margin: 0;
  color: var(--text-primary);
  text-align: right;
  overflow-wrap: anywhere;
}
.panel__row--path dd {
  font-family: var(--font-mono, monospace);
  font-size: calc(var(--fs-body) * 0.9);
  color: var(--text-secondary);
}

.panel__tags {
  padding: var(--space-3) var(--space-4);
  border-top: var(--border-thin) solid var(--border-subtle);
}
.panel__tags h3 {
  margin: 0 0 var(--space-2);
  font: var(--fw-medium) var(--fs-body) / 1 var(--font-sans);
}
.panel__muted { color: var(--text-muted); font-size: var(--fs-body); margin: 0; }
</style>
