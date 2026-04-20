<script setup lang="ts">
// TaggingQueue is a cluster-first tagging surface for visually-similar
// photos: instead of tagging one photo at a time, the user tags a
// whole cluster in one action. Largest still-untagged clusters surface
// first so each click covers the most photos.
//
// Scope is fixed to "similar" here on purpose — duplicates belong to
// the Duplicates island (see Duplicates.vue), where the intended
// action is dedup, not tagging.
//
// The island mounts on page load but stays hidden until the Toolbar
// fires the open event — keeping the cold cost to the progress HUD
// fetch that never happens unless the user opens the panel.

import { onMounted, onUnmounted, ref, computed } from 'vue'
import { apiGet, apiPost, friendlyError } from '../transport/api'
import { onEvent } from '../transport/events'
import { thumbSrc } from '../transport/thumbs'
import { onOpenTaggingQueue } from '../transport/tagging'
import TagInput from '../components/TagInput.vue'
import type { TagCluster, TaggingProgress } from '../types'

const open = ref(false)
const clusters = ref<TagCluster[]>([])
const progress = ref<TaggingProgress | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const selectedId = ref<string | null>(null)
const pendingTags = ref<string[]>([])
const applying = ref(false)

// Only clusters with work left to do belong in the queue. A cluster
// whose Untagged count has dropped to zero is already handled and
// would just be visual noise the user has to scroll past.
const untaggedClusters = computed(() =>
  clusters.value.filter((c) => c.untagged > 0),
)

const selectedCluster = computed(() =>
  untaggedClusters.value.find((c) => c.id === selectedId.value) ?? null,
)

const progressPct = computed(() => {
  const p = progress.value
  if (!p || p.total === 0) return 0
  return Math.round((p.tagged / p.total) * 100)
})

async function refreshClusters(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const [list, prog] = await Promise.all([
      apiGet<{ clusters: TagCluster[] }>('/api/clusters?kind=similar'),
      apiGet<TaggingProgress>('/api/tagging/progress'),
    ])
    clusters.value = list.clusters ?? []
    progress.value = prog
    const visible = untaggedClusters.value
    if (visible.length > 0 && !visible.some((c) => c.id === selectedId.value)) {
      selectedId.value = visible[0].id
    } else if (visible.length === 0) {
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

async function applyTags(): Promise<void> {
  const cluster = selectedCluster.value
  const tags = pendingTags.value
  if (!cluster || tags.length === 0 || applying.value) return
  applying.value = true
  error.value = null
  try {
    await apiPost<{ updated: number }>('/api/tagging/apply', {
      clusterId: cluster.id,
      members: cluster.members,
      tags,
    })
    pendingTags.value = []
    // Optimistic local update; the refresh right after pulls the
    // authoritative state and advanceToNext picks the next cluster.
    cluster.untagged = 0
    const handledId = cluster.id
    await refreshClusters()
    advanceToNext(handledId)
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    applying.value = false
  }
}

function advanceToNext(handledId: string): void {
  const list = untaggedClusters.value
  if (list.length === 0) {
    selectedId.value = null
    return
  }
  const idx = list.findIndex((c) => c.id === handledId)
  const next = idx >= 0 && idx < list.length - 1 ? list[idx + 1] : list[0]
  selectedId.value = next.id
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
  const list = untaggedClusters.value
  if (list.length === 0) return
  const idx = list.findIndex((c) => c.id === selectedId.value)
  const nextIdx = Math.max(0, Math.min(list.length - 1, idx + delta))
  selectedId.value = list[nextIdx]?.id ?? null
}

let disposeOpen: (() => void) | null = null
let disposeClusters: (() => void) | null = null
let disposeLibrary: (() => void) | null = null

onMounted(() => {
  disposeOpen = onOpenTaggingQueue(async () => {
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
  <div v-if="open" class="tq" role="dialog" aria-label="Tagging Queue">
    <div class="tq__backdrop" @click="close" />

    <section class="tq__panel">
      <header class="tq__header">
        <h2 class="tq__title">Tagging Queue</h2>
        <button class="tq__close" @click="close" aria-label="Close">×</button>
      </header>

      <p class="tq__help">
        Visually similar shots — same subject, scene, or framing.
        Skim the preview before tagging so a mislabelled group doesn't
        slip through. Clusters disappear once fully tagged.
      </p>

      <div v-if="progress" class="tq__progress">
        <div class="tq__progress-bar">
          <div class="tq__progress-fill" :style="{ width: progressPct + '%' }"></div>
        </div>
        <div class="tq__progress-label">
          <strong>{{ progress.tagged.toLocaleString() }}</strong>
          /
          {{ progress.total.toLocaleString() }} tagged
          <span class="tq__progress-sep">·</span>
          {{ untaggedClusters.length }} clusters left
        </div>
      </div>

      <div v-if="error" class="tq__error">{{ error }}</div>

      <div class="tq__body">
        <aside class="tq__list">
          <div v-if="loading && untaggedClusters.length === 0" class="tq__empty">
            Loading clusters…
          </div>
          <div v-else-if="!loading && untaggedClusters.length === 0" class="tq__empty">
            No untagged clusters left. Run a scan, or everything's already handled.
          </div>
          <ul v-else class="tq__clusters">
            <li
              v-for="c in untaggedClusters"
              :key="c.id"
              :class="['tq__cluster', { 'tq__cluster--active': c.id === selectedId }]"
              @click="selectCluster(c.id)"
            >
              <img class="tq__thumb" :src="thumbSrc(c.members[0])" alt="" />
              <div class="tq__cluster-meta">
                <div class="tq__cluster-size">{{ c.size }} photos</div>
                <div class="tq__cluster-state">
                  {{ c.untagged }} untagged
                </div>
              </div>
            </li>
          </ul>
        </aside>

        <main class="tq__detail">
          <div v-if="!selectedCluster" class="tq__empty">
            Select a cluster on the left to tag it.
          </div>
          <template v-else>
            <div class="tq__detail-header">
              <h3>Cluster · {{ selectedCluster.size }} photos</h3>
              <p class="tq__detail-sub">
                {{ selectedCluster.untagged }} currently untagged
              </p>
            </div>
            <div class="tq__grid">
              <img
                v-for="p in selectedCluster.members.slice(0, 24)"
                :key="p"
                class="tq__grid-img"
                :src="thumbSrc(p)"
                alt=""
              />
            </div>
            <div v-if="selectedCluster.members.length > 24" class="tq__more">
              + {{ selectedCluster.members.length - 24 }} more
            </div>
            <footer class="tq__apply">
              <TagInput
                v-model="pendingTags"
                placeholder="Type a tag, press space to add"
                aria-label="Tags to apply"
                class="tq__tag-input"
              />
              <button
                class="tq__apply-btn"
                :disabled="applying || pendingTags.length === 0"
                @click="applyTags"
              >
                {{ applying ? 'Applying…' : 'Tag cluster' }}
              </button>
            </footer>
          </template>
        </main>
      </div>
    </section>
  </div>
</template>

<style scoped>
.tq {
  position: fixed;
  inset: 0;
  z-index: 90;
  display: flex;
  align-items: stretch;
  justify-content: center;
}
.tq__backdrop {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(3px);
}
.tq__panel {
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
.tq__header {
  display: flex;
  align-items: center;
  gap: var(--space-5);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.tq__title {
  margin: 0;
  font-size: var(--fs-medium);
  font-weight: var(--fw-semibold);
  color: var(--text-primary);
}
.tq__close {
  margin-left: auto;
  background: transparent;
  border: none;
  color: var(--text-secondary);
  font-size: 24px;
  line-height: 1;
  cursor: pointer;
}
.tq__help {
  margin: 0;
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
  color: var(--text-secondary);
  font-size: var(--fs-small);
  line-height: 1.5;
}
.tq__progress {
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.tq__progress-bar {
  height: 6px;
  border-radius: var(--radius-full);
  background: var(--surface-active);
  overflow: hidden;
}
.tq__progress-fill {
  height: 100%;
  background: var(--accent);
  transition: width var(--dur-medium, 200ms) var(--ease-out);
}
.tq__progress-label {
  font-size: var(--fs-small);
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}
.tq__progress-sep {
  margin: 0 var(--space-2);
  color: var(--text-muted);
}
.tq__error {
  padding: var(--space-3) var(--space-5);
  background: rgba(255, 70, 70, 0.1);
  color: #ff8080;
  font-size: var(--fs-small);
}
.tq__body {
  flex: 1;
  display: grid;
  grid-template-columns: 280px 1fr;
  min-height: 0;
}
.tq__list {
  border-right: 1px solid var(--border-subtle);
  overflow-y: auto;
}
.tq__clusters {
  list-style: none;
  margin: 0;
  padding: 0;
}
.tq__cluster {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  cursor: pointer;
  border-bottom: 1px solid var(--border-subtle);
  transition: background var(--dur-fast) var(--ease-out);
}
.tq__cluster:hover {
  background: var(--surface-active);
}
.tq__cluster--active {
  background: var(--surface-active);
  box-shadow: inset 3px 0 0 var(--accent);
}
.tq__thumb {
  width: 48px;
  height: 48px;
  object-fit: cover;
  border-radius: var(--radius-2, 4px);
  background: var(--surface-active);
}
.tq__cluster-meta {
  display: flex;
  flex-direction: column;
  justify-content: center;
}
.tq__cluster-size {
  color: var(--text-primary);
  font-size: var(--fs-small);
  font-weight: var(--fw-medium);
}
.tq__cluster-state {
  color: var(--text-muted);
  font-size: var(--fs-micro);
}
.tq__detail {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.tq__detail-header {
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.tq__detail-header h3 {
  margin: 0 0 var(--space-1) 0;
  font-size: var(--fs-medium);
  color: var(--text-primary);
}
.tq__detail-sub {
  margin: 0;
  font-size: var(--fs-small);
  color: var(--text-muted);
}
.tq__grid {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4) var(--space-5);
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));
  gap: var(--space-2);
}
.tq__grid-img {
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  border-radius: var(--radius-2, 4px);
  background: var(--surface-active);
}
.tq__more {
  padding: 0 var(--space-5) var(--space-3);
  font-size: var(--fs-small);
  color: var(--text-muted);
}
.tq__apply {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-top: 1px solid var(--border-subtle);
}
.tq__tag-input {
  flex: 1;
  min-width: 0;
}
.tq__apply-btn {
  height: 32px;
  padding: 0 var(--space-4);
  background: var(--accent);
  border: none;
  border-radius: var(--radius-2, 6px);
  color: var(--text-primary);
  font-size: var(--fs-small);
  font-weight: var(--fw-medium);
  cursor: pointer;
}
.tq__apply-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.tq__empty {
  padding: var(--space-5);
  color: var(--text-muted);
  font-size: var(--fs-small);
}
</style>
