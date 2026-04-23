<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { apiGet, apiPost, friendlyError } from '../transport/api'
import { onEvent } from '../transport/events'
import { thumbSrc } from '../transport/thumbs'
import { onOpenSimilarityReview } from '../transport/tagging'
import { requestDelete } from '../transport/fileops'
import TagInput from '../components/TagInput.vue'
import type { TagCluster } from '../types'

type TabKind = 'duplicate' | 'similar'

const open = ref(false)
const activeTab = ref<TabKind>('duplicate')
const loading = ref(false)
const error = ref<string | null>(null)

const duplicateClusters = ref<TagCluster[]>([])
const similarClusters = ref<TagCluster[]>([])
const selectedDuplicateId = ref<string | null>(null)
const selectedSimilarId = ref<string | null>(null)

const pendingTags = ref<string[]>([])
const applying = ref(false)

const clusters = computed<TagCluster[]>(() =>
  activeTab.value === 'duplicate' ? duplicateClusters.value : similarClusters.value,
)

const visibleClusters = computed(() =>
  activeTab.value === 'similar'
    ? clusters.value.filter((c) => c.untagged > 0)
    : clusters.value,
)

const selectedId = computed({
  get: () => (activeTab.value === 'duplicate' ? selectedDuplicateId.value : selectedSimilarId.value),
  set: (v) => {
    if (activeTab.value === 'duplicate') selectedDuplicateId.value = v
    else selectedSimilarId.value = v
  },
})

const selectedCluster = computed(
  () => visibleClusters.value.find((c) => c.id === selectedId.value) ?? null,
)

const totalDuplicatePhotos = computed(() =>
  duplicateClusters.value.reduce((sum, c) => sum + c.size, 0),
)
const photosToReclaim = computed(() =>
  duplicateClusters.value.reduce((sum, c) => sum + (c.size - 1), 0),
)

async function refreshActive(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const kind = activeTab.value
    const res = await apiGet<{ clusters: TagCluster[] }>(`/api/clusters?kind=${kind}`)
    const list = res.clusters ?? []
    if (kind === 'duplicate') duplicateClusters.value = list
    else similarClusters.value = list

    const visible = visibleClusters.value
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

function selectCluster(id: string): void { selectedId.value = id }
function deleteOne(path: string): void { requestDelete([path]) }

function deleteExtras(): void {
  const c = selectedCluster.value
  if (!c || c.size < 2) return
  requestDelete(c.members.slice(1))
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
    cluster.untagged = 0
    const handledId = cluster.id
    await refreshActive()
    const list = visibleClusters.value
    if (list.length === 0) {
      selectedId.value = null
    } else {
      const idx = list.findIndex((c) => c.id === handledId)
      const next = idx >= 0 && idx < list.length - 1 ? list[idx + 1] : list[0]
      selectedId.value = next.id
    }
  } catch (err) {
    error.value = friendlyError(err)
  } finally {
    applying.value = false
  }
}

function setTab(tab: TabKind): void {
  if (activeTab.value === tab) return
  activeTab.value = tab
  pendingTags.value = []
  refreshActive()
}

function close(): void { open.value = false }

function onKey(e: KeyboardEvent): void {
  if (!open.value) return
  if (e.key === 'Escape') { close(); return }
  const tag = (e.target as HTMLElement)?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA') return
  if (e.key === 'j') { stepSelection(1); e.preventDefault() }
  else if (e.key === 'k') { stepSelection(-1); e.preventDefault() }
}

function stepSelection(delta: number): void {
  const list = visibleClusters.value
  if (list.length === 0) return
  const idx = list.findIndex((c) => c.id === selectedId.value)
  const nextIdx = Math.max(0, Math.min(list.length - 1, idx + delta))
  selectedId.value = list[nextIdx]?.id ?? null
}

let disposeOpen: (() => void) | null = null
let disposeClusters: (() => void) | null = null
let disposeLibrary: (() => void) | null = null

onMounted(() => {
  disposeOpen = onOpenSimilarityReview(async (tab?: TabKind) => {
    open.value = true
    if (tab) activeTab.value = tab
    await refreshActive()
  })
  disposeClusters = onEvent('clusters:ready', () => { if (open.value) refreshActive() })
  disposeLibrary = onEvent('library:updated', () => { if (open.value) refreshActive() })
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
  <div v-if="open" class="modal modal-open" role="dialog" aria-label="Similarity review">
    <div class="modal-box flex flex-col p-0 w-full max-w-6xl max-h-[92vh]">
      <div class="flex items-center gap-4 border-b border-base-300 px-4 py-3">
        <h2 class="m-0 text-lg font-semibold">Similarity review</h2>
        <div role="tablist" class="tabs tabs-boxed tabs-sm">
          <button
            type="button"
            role="tab"
            :class="['tab', activeTab === 'duplicate' ? 'tab-active' : '']"
            :aria-selected="activeTab === 'duplicate'"
            @click="setTab('duplicate')"
          >
            Duplicates
            <span v-if="duplicateClusters.length" class="badge badge-ghost badge-xs ml-2">{{ duplicateClusters.length }}</span>
          </button>
          <button
            type="button"
            role="tab"
            :class="['tab', activeTab === 'similar' ? 'tab-active' : '']"
            :aria-selected="activeTab === 'similar'"
            @click="setTab('similar')"
          >
            Similar
            <span v-if="similarClusters.length" class="badge badge-ghost badge-xs ml-2">{{ similarClusters.length }}</span>
          </button>
        </div>
        <button type="button" class="btn btn-ghost btn-sm btn-square ml-auto" @click="close" aria-label="Close">✕</button>
      </div>

      <p v-if="activeTab === 'duplicate'" class="border-b border-base-300 px-4 py-2 text-xs text-base-content/70">
        Near-identical shots — bursts, edits, or re-saves of the same photo. Review each cluster, then remove the extras.
      </p>
      <p v-else class="border-b border-base-300 px-4 py-2 text-xs text-base-content/70">
        Visually similar shots — same subject, scene, or framing. Tag a whole cluster in one action; clusters disappear once fully tagged.
      </p>

      <div
        v-if="activeTab === 'duplicate' && duplicateClusters.length > 0"
        class="stats stats-horizontal bg-base-200 rounded-none border-b border-base-300"
      >
        <div class="stat py-2">
          <div class="stat-value text-base">{{ duplicateClusters.length }}</div>
          <div class="stat-desc">duplicate clusters</div>
        </div>
        <div class="stat py-2">
          <div class="stat-value text-base">{{ totalDuplicatePhotos.toLocaleString() }}</div>
          <div class="stat-desc">photos involved</div>
        </div>
        <div class="stat py-2">
          <div class="stat-value text-base">{{ photosToReclaim.toLocaleString() }}</div>
          <div class="stat-desc">could be reclaimed</div>
        </div>
      </div>

      <div v-if="error" class="alert alert-error alert-soft rounded-none text-xs" role="alert">{{ error }}</div>

      <div class="flex-1 min-h-0 grid" style="grid-template-columns: 280px 1fr;">
        <aside class="border-r border-base-300 overflow-y-auto">
          <div v-if="loading && visibleClusters.length === 0" class="p-4 text-sm text-base-content/60">
            Loading…
          </div>
          <div v-else-if="!loading && visibleClusters.length === 0" class="p-4 text-sm text-base-content/60">
            <template v-if="activeTab === 'duplicate'">No duplicates found. Clean library!</template>
            <template v-else>No untagged clusters left. Run a scan, or everything's already handled.</template>
          </div>
          <ul v-else class="divide-y divide-base-300">
            <li
              v-for="c in visibleClusters"
              :key="c.id"
              :class="[
                'flex cursor-pointer gap-3 px-3 py-2 hover:bg-base-200',
                c.id === selectedId ? 'bg-base-200 border-l-2 border-primary' : '',
              ]"
              @click="selectCluster(c.id)"
            >
              <img class="h-12 w-12 shrink-0 rounded object-cover bg-base-300" :src="thumbSrc(c.members[0])" alt="" />
              <div class="flex flex-col justify-center min-w-0">
                <div class="truncate text-sm font-medium">
                  {{ c.size }} {{ activeTab === 'duplicate' ? 'copies' : 'photos' }}
                </div>
                <div class="text-xs text-base-content/50">
                  <template v-if="activeTab === 'duplicate'">{{ c.size - 1 }} extra</template>
                  <template v-else>{{ c.untagged }} untagged</template>
                </div>
              </div>
            </li>
          </ul>
        </aside>

        <main class="flex min-h-0 flex-col overflow-hidden">
          <div v-if="!selectedCluster" class="p-4 text-sm text-base-content/60">
            Select a cluster on the left.
          </div>
          <template v-else-if="activeTab === 'duplicate'">
            <div class="border-b border-base-300 px-4 py-3">
              <h3 class="m-0 text-base font-medium">{{ selectedCluster.size }} copies of the same photo</h3>
              <p class="m-0 text-xs text-base-content/60">
                Keeping one copy would free {{ selectedCluster.size - 1 }}
                file{{ selectedCluster.size - 1 === 1 ? '' : 's' }}.
              </p>
            </div>
            <ul class="flex-1 overflow-y-auto flex flex-col gap-2 px-4 py-3 list-none m-0">
              <li
                v-for="(p, i) in selectedCluster.members"
                :key="p"
                class="card card-side bg-base-200 flex items-center gap-3 p-2"
              >
                <img class="h-14 w-14 shrink-0 rounded object-cover bg-base-300" :src="thumbSrc(p)" alt="" />
                <div class="flex min-w-0 flex-1 flex-col gap-0.5">
                  <div class="truncate font-mono text-xs" :title="p">{{ p }}</div>
                  <div v-if="i === 0" class="text-primary text-[10px] uppercase tracking-wider font-semibold">
                    First by path (suggested keep)
                  </div>
                </div>
                <button
                  v-if="i !== 0"
                  type="button"
                  class="btn btn-xs btn-ghost text-error"
                  @click="deleteOne(p)"
                >
                  Delete
                </button>
              </li>
            </ul>
            <div class="flex items-center gap-4 border-t border-base-300 px-4 py-3">
              <button
                type="button"
                class="btn btn-sm btn-error"
                :disabled="selectedCluster.size < 2"
                @click="deleteExtras"
              >
                Delete {{ selectedCluster.size - 1 }} extra{{ selectedCluster.size - 1 === 1 ? '' : 's' }}
              </button>
              <p class="m-0 text-xs text-base-content/60">
                Keeps the suggested file; moves the others to trash (undoable).
              </p>
            </div>
          </template>
          <template v-else>
            <div class="border-b border-base-300 px-4 py-3">
              <h3 class="m-0 text-base font-medium">Cluster · {{ selectedCluster.size }} photos</h3>
              <p class="m-0 text-xs text-base-content/60">
                {{ selectedCluster.untagged }} currently untagged
              </p>
            </div>
            <div
              class="flex-1 overflow-y-auto grid gap-2 px-4 py-3"
              style="grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));"
            >
              <img
                v-for="p in selectedCluster.members.slice(0, 24)"
                :key="p"
                class="aspect-square w-full rounded object-cover bg-base-300"
                :src="thumbSrc(p)"
                alt=""
              />
            </div>
            <div v-if="selectedCluster.members.length > 24" class="px-4 pb-2 text-xs text-base-content/60">
              + {{ selectedCluster.members.length - 24 }} more
            </div>
            <div class="flex gap-3 border-t border-base-300 px-4 py-3">
              <TagInput
                v-model="pendingTags"
                placeholder="Type a tag, press space to add"
                aria-label="Tags to apply"
                class="flex-1 min-w-0"
              />
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="applying || pendingTags.length === 0"
                @click="applyTags"
              >
                {{ applying ? 'Applying…' : 'Tag cluster' }}
              </button>
            </div>
          </template>
        </main>
      </div>
    </div>
  </div>
</template>
