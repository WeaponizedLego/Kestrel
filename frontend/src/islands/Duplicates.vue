<script setup lang="ts">
// Duplicates is a review surface for near-identical photos: bursts,
// edits, re-saves of the same shot. Unlike the Tagging Queue, the
// intended action here is deduplication, not tagging — today the
// island just surfaces what exists; a future iteration will let the
// user keep one and remove the rest.
//
// Architecturally this is a sibling of TaggingQueue: it hits the
// same /api/clusters endpoint (kind=duplicate), mounts on load, and
// stays hidden until the Toolbar fires its open event.

import { onMounted, onUnmounted, ref, computed } from 'vue'
import { apiGet, friendlyError } from '../transport/api'
import { onEvent } from '../transport/events'
import { thumbSrc } from '../transport/thumbs'
import { onOpenDuplicates } from '../transport/tagging'
import type { TagCluster } from '../types'

const open = ref(false)
const clusters = ref<TagCluster[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const selectedId = ref<string | null>(null)

const selectedCluster = computed(() =>
  clusters.value.find((c) => c.id === selectedId.value) ?? null,
)

const totalDuplicatePhotos = computed(() =>
  clusters.value.reduce((sum, c) => sum + c.size, 0),
)
const photosToReclaim = computed(() =>
  // If the user kept one per cluster, this many files would go — a
  // rough "potential savings" number that motivates the feature.
  clusters.value.reduce((sum, c) => sum + (c.size - 1), 0),
)

async function refreshClusters(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const list = await apiGet<{ clusters: TagCluster[] }>(
      '/api/clusters?kind=duplicate',
    )
    clusters.value = list.clusters ?? []
    if (
      clusters.value.length > 0 &&
      !clusters.value.some((c) => c.id === selectedId.value)
    ) {
      selectedId.value = clusters.value[0].id
    } else if (clusters.value.length === 0) {
      selectedId.value = null
    }
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    loading.value = false
  }
}

function selectCluster(id: string): void {
  selectedId.value = id
}

function close(): void {
  open.value = false
}

function onKey(e: KeyboardEvent): void {
  if (!open.value) return
  if (e.key === 'Escape') {
    close()
    return
  }
  const tag = (e.target as HTMLElement)?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA') return
  if (e.key === 'j') {
    stepSelection(1)
    e.preventDefault()
  } else if (e.key === 'k') {
    stepSelection(-1)
    e.preventDefault()
  }
}

function stepSelection(delta: number): void {
  if (clusters.value.length === 0) return
  const idx = clusters.value.findIndex((c) => c.id === selectedId.value)
  const nextIdx = Math.max(0, Math.min(clusters.value.length - 1, idx + delta))
  selectedId.value = clusters.value[nextIdx]?.id ?? null
}

let disposeOpen: (() => void) | null = null
let disposeClusters: (() => void) | null = null
let disposeLibrary: (() => void) | null = null

onMounted(() => {
  disposeOpen = onOpenDuplicates(async () => {
    open.value = true
    await refreshClusters()
  })
  disposeClusters = onEvent('clusters:ready', () => {
    if (open.value) refreshClusters()
  })
  disposeLibrary = onEvent('library:updated', () => {
    if (open.value) refreshClusters()
  })
  window.addEventListener('keydown', onKey)
})

onUnmounted(() => {
  disposeOpen?.()
  disposeClusters?.()
  disposeLibrary?.()
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div v-if="open" class="dup" role="dialog" aria-label="Duplicates">
    <div class="dup__backdrop" @click="close" />

    <section class="dup__panel">
      <header class="dup__header">
        <h2 class="dup__title">Duplicates</h2>
        <button class="dup__close" @click="close" aria-label="Close">×</button>
      </header>

      <p class="dup__help">
        Near-identical shots — bursts, edits, or re-saves of the same
        photo. Review each cluster to decide which copies to keep.
        <span class="dup__coming-soon">Deletion is coming soon</span>;
        today you can review groups and reveal originals in your file
        manager.
      </p>

      <div v-if="clusters.length > 0" class="dup__summary">
        <div class="dup__stat">
          <span class="dup__stat-value">{{ clusters.length }}</span>
          <span class="dup__stat-label">duplicate clusters</span>
        </div>
        <div class="dup__stat">
          <span class="dup__stat-value">{{ totalDuplicatePhotos.toLocaleString() }}</span>
          <span class="dup__stat-label">photos involved</span>
        </div>
        <div class="dup__stat">
          <span class="dup__stat-value">{{ photosToReclaim.toLocaleString() }}</span>
          <span class="dup__stat-label">could be reclaimed</span>
        </div>
      </div>

      <div v-if="error" class="dup__error">{{ error }}</div>

      <div class="dup__body">
        <aside class="dup__list">
          <div v-if="loading && clusters.length === 0" class="dup__empty">
            Scanning for duplicates…
          </div>
          <div v-else-if="!loading && clusters.length === 0" class="dup__empty">
            No duplicates found. Clean library!
          </div>
          <ul v-else class="dup__clusters">
            <li
              v-for="c in clusters"
              :key="c.id"
              :class="['dup__cluster', { 'dup__cluster--active': c.id === selectedId }]"
              @click="selectCluster(c.id)"
            >
              <img class="dup__thumb" :src="thumbSrc(c.members[0])" alt="" />
              <div class="dup__cluster-meta">
                <div class="dup__cluster-size">{{ c.size }} copies</div>
                <div class="dup__cluster-state">
                  {{ c.size - 1 }} extra
                </div>
              </div>
            </li>
          </ul>
        </aside>

        <main class="dup__detail">
          <div v-if="!selectedCluster" class="dup__empty">
            Select a cluster on the left to review it.
          </div>
          <template v-else>
            <div class="dup__detail-header">
              <h3>{{ selectedCluster.size }} copies of the same photo</h3>
              <p class="dup__detail-sub">
                Keeping one copy would free {{ selectedCluster.size - 1 }}
                file{{ selectedCluster.size - 1 === 1 ? '' : 's' }}.
              </p>
            </div>
            <ul class="dup__files">
              <li
                v-for="(p, i) in selectedCluster.members"
                :key="p"
                class="dup__file"
              >
                <img class="dup__file-thumb" :src="thumbSrc(p)" alt="" />
                <div class="dup__file-meta">
                  <div class="dup__file-path" :title="p">{{ p }}</div>
                  <div class="dup__file-badge" v-if="i === 0">
                    First by path (suggested keep)
                  </div>
                </div>
              </li>
            </ul>
            <footer class="dup__footer">
              <p class="dup__footer-note">
                Per-cluster actions (keep one, move extras to trash) land
                in the next iteration. For now, review and copy paths as
                needed.
              </p>
            </footer>
          </template>
        </main>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dup {
  position: fixed;
  inset: 0;
  z-index: 90;
  display: flex;
  align-items: stretch;
  justify-content: center;
}
.dup__backdrop {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(3px);
}
.dup__panel {
  position: relative;
  width: min(1080px, 92vw);
  margin: 4vh auto;
  background: var(--surface-bg);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-3, 10px);
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.45);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.dup__header {
  display: flex;
  align-items: center;
  gap: var(--space-5);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.dup__title {
  margin: 0;
  font-size: var(--fs-medium);
  font-weight: var(--fw-semibold);
  color: var(--text-primary);
}
.dup__close {
  margin-left: auto;
  background: transparent;
  border: none;
  color: var(--text-secondary);
  font-size: 24px;
  line-height: 1;
  cursor: pointer;
}
.dup__help {
  margin: 0;
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
  color: var(--text-secondary);
  font-size: var(--fs-small);
  line-height: 1.5;
}
.dup__coming-soon {
  color: var(--text-muted);
  font-style: italic;
}
.dup__summary {
  display: flex;
  gap: var(--space-5);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.dup__stat {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.dup__stat-value {
  color: var(--text-primary);
  font-size: var(--fs-large, 18px);
  font-weight: var(--fw-semibold);
  font-variant-numeric: tabular-nums;
}
.dup__stat-label {
  color: var(--text-muted);
  font-size: var(--fs-micro);
  text-transform: uppercase;
  letter-spacing: var(--tracking-micro);
}
.dup__error {
  padding: var(--space-3) var(--space-5);
  background: rgba(255, 70, 70, 0.1);
  color: #ff8080;
  font-size: var(--fs-small);
}
.dup__body {
  flex: 1;
  display: grid;
  grid-template-columns: 280px 1fr;
  min-height: 0;
}
.dup__list {
  border-right: 1px solid var(--border-subtle);
  overflow-y: auto;
}
.dup__clusters {
  list-style: none;
  margin: 0;
  padding: 0;
}
.dup__cluster {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  cursor: pointer;
  border-bottom: 1px solid var(--border-subtle);
  transition: background var(--dur-fast) var(--ease-out);
}
.dup__cluster:hover {
  background: var(--surface-active);
}
.dup__cluster--active {
  background: var(--surface-active);
  box-shadow: inset 3px 0 0 var(--accent);
}
.dup__thumb {
  width: 48px;
  height: 48px;
  object-fit: cover;
  border-radius: var(--radius-2, 4px);
  background: var(--surface-active);
}
.dup__cluster-meta {
  display: flex;
  flex-direction: column;
  justify-content: center;
}
.dup__cluster-size {
  color: var(--text-primary);
  font-size: var(--fs-small);
  font-weight: var(--fw-medium);
}
.dup__cluster-state {
  color: var(--text-muted);
  font-size: var(--fs-micro);
}
.dup__detail {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.dup__detail-header {
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.dup__detail-header h3 {
  margin: 0 0 var(--space-1) 0;
  font-size: var(--fs-medium);
  color: var(--text-primary);
}
.dup__detail-sub {
  margin: 0;
  font-size: var(--fs-small);
  color: var(--text-muted);
}
.dup__files {
  flex: 1;
  overflow-y: auto;
  margin: 0;
  padding: var(--space-3) var(--space-5);
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.dup__file {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2);
  border-radius: var(--radius-2, 4px);
  background: var(--surface-raised, rgba(255, 255, 255, 0.03));
}
.dup__file-thumb {
  width: 56px;
  height: 56px;
  object-fit: cover;
  border-radius: var(--radius-2, 4px);
  background: var(--surface-active);
  flex-shrink: 0;
}
.dup__file-meta {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  flex: 1;
}
.dup__file-path {
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-small);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.dup__file-badge {
  color: var(--accent);
  font-size: var(--fs-micro);
  text-transform: uppercase;
  letter-spacing: var(--tracking-micro);
  font-weight: var(--fw-semibold);
}
.dup__footer {
  padding: var(--space-3) var(--space-5);
  border-top: 1px solid var(--border-subtle);
}
.dup__footer-note {
  margin: 0;
  color: var(--text-muted);
  font-size: var(--fs-micro);
  line-height: 1.5;
}
.dup__empty {
  padding: var(--space-5);
  color: var(--text-muted);
  font-size: var(--fs-small);
}
</style>
