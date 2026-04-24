<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { apiGet } from '../transport/api'
import { onEvent } from '../transport/events'
import { fuzzyScore } from './fuzzy'

interface TagStat { name: string; count: number; kind: 'user' | 'auto'; hidden: boolean }

const props = withDefaults(defineProps<{
  modelValue: string[]
  placeholder?: string
  ariaLabel?: string
  suggestions?: 'off' | 'search'
}>(), { suggestions: 'off' })

const emit = defineEmits<{
  (e: 'update:modelValue', value: string[]): void
}>()

const buffer = ref('')
const inputEl = ref<HTMLInputElement | null>(null)

const tags = ref<string[]>([...(props.modelValue ?? [])])
watch(() => props.modelValue, (next) => {
  const incoming = next ?? []
  if (sameList(incoming, tags.value)) return
  tags.value = [...incoming]
})

function sameList(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
  return true
}

function normalize(raw: string): string { return raw.trim().toLowerCase() }

function commit(name: string) {
  const clean = normalize(name)
  buffer.value = ''
  highlightIndex.value = -1
  open.value = false
  if (!clean) return
  if (tags.value.includes(clean)) return
  tags.value = [...tags.value, clean]
  emit('update:modelValue', tags.value)
}

function commitBuffer() { commit(buffer.value) }

function removeAt(index: number) {
  tags.value = tags.value.filter((_, i) => i !== index)
  emit('update:modelValue', tags.value)
}

// --- Suggestions (only active when props.suggestions === 'search') ---

const open = ref(false)
const highlightIndex = ref(-1)
const allTags = ref<TagStat[]>([])
let lastFetch = 0
let disposeEvent: (() => void) | null = null

async function refreshSuggestions(force = false): Promise<void> {
  if (props.suggestions === 'off') return
  const now = Date.now()
  if (!force && now - lastFetch < 2000) return
  lastFetch = now
  try {
    allTags.value = await apiGet<TagStat[]>('/api/tags/list?include_auto=1')
  } catch {
    // Silent fail — autocomplete is a UX aid; the raw typed token still
    // works as a plain search term.
  }
}

const visibleSuggestions = computed<TagStat[]>(() => {
  if (props.suggestions === 'off') return []
  const q = buffer.value.trim().toLowerCase()
  if (!q) return []
  const chipped = new Set(tags.value)
  const scored: { tag: TagStat; score: number }[] = []
  for (const t of allTags.value) {
    if (chipped.has(t.name)) continue
    const s = fuzzyScore(q, t.name)
    if (s === null) continue
    scored.push({ tag: t, score: s })
  }
  scored.sort((a, b) => b.score - a.score)
  return scored.slice(0, 8).map((s) => s.tag)
})

watch(buffer, () => {
  if (props.suggestions === 'off') return
  open.value = buffer.value.trim() !== ''
  highlightIndex.value = visibleSuggestions.value.length > 0 ? 0 : -1
})

function onFocus() {
  if (props.suggestions === 'search') {
    refreshSuggestions()
    if (buffer.value.trim() !== '') open.value = true
  }
}

function onKeydown(e: KeyboardEvent) {
  const hasSuggestions = open.value && visibleSuggestions.value.length > 0

  if (hasSuggestions) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      highlightIndex.value = Math.min(highlightIndex.value + 1, visibleSuggestions.value.length - 1)
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      highlightIndex.value = Math.max(highlightIndex.value - 1, 0)
      return
    }
    if (e.key === 'Tab') {
      e.preventDefault()
      const i = Math.max(highlightIndex.value, 0)
      commit(visibleSuggestions.value[i].name)
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      open.value = false
      return
    }
  }

  if (e.key === ' ' || e.key === 'Enter') {
    if (hasSuggestions && highlightIndex.value >= 0) {
      e.preventDefault()
      commit(visibleSuggestions.value[highlightIndex.value].name)
      return
    }
    if (buffer.value.trim() !== '') {
      e.preventDefault()
      commitBuffer()
    } else if (e.key === 'Enter') {
      e.preventDefault()
    }
    return
  }
  if (e.key === 'Backspace' && buffer.value === '' && tags.value.length) {
    e.preventDefault()
    removeAt(tags.value.length - 1)
  }
}

function onBlur() {
  // Defer so a click on a suggestion row wins over the blur-commit.
  setTimeout(() => {
    if (document.activeElement === inputEl.value) return
    open.value = false
    if (buffer.value.trim() !== '') commitBuffer()
  }, 0)
}

function focus() { inputEl.value?.focus() }
defineExpose({ focus })

onMounted(() => {
  if (props.suggestions === 'search') {
    refreshSuggestions(true)
    disposeEvent = onEvent('library:updated', () => { refreshSuggestions(true) })
  }
})
onUnmounted(() => { disposeEvent?.() })

// Keep highlight in range if the suggestion list shrinks.
watch(visibleSuggestions, (list) => {
  if (list.length === 0) {
    highlightIndex.value = -1
  } else if (highlightIndex.value >= list.length) {
    highlightIndex.value = list.length - 1
  } else if (highlightIndex.value < 0) {
    highlightIndex.value = 0
  }
})

function onSuggestionClick(name: string) {
  commit(name)
  nextTick(() => inputEl.value?.focus())
}
</script>

<template>
  <div
    :class="[
      'input input-sm input-bordered flex min-h-9 h-auto flex-wrap items-center gap-1 py-1 cursor-text relative',
      open && visibleSuggestions.length > 0 ? 'z-50' : '',
    ]"
    @click="focus"
  >
    <span
      v-for="(tag, i) in tags"
      :key="tag"
      class="badge badge-primary badge-sm gap-1"
    >
      {{ tag }}
      <button
        type="button"
        class="cursor-pointer leading-none opacity-70 hover:opacity-100"
        :aria-label="`Remove tag ${tag}`"
        @click.stop="removeAt(i)"
      >×</button>
    </span>
    <input
      ref="inputEl"
      v-model="buffer"
      type="text"
      class="flex-1 min-w-24 border-none bg-transparent p-0 outline-none"
      :placeholder="tags.length ? '' : (placeholder ?? 'Add tag…')"
      :aria-label="ariaLabel ?? 'Tags'"
      role="combobox"
      :aria-expanded="open && visibleSuggestions.length > 0"
      aria-autocomplete="list"
      @keydown="onKeydown"
      @focus="onFocus"
      @blur="onBlur"
    />
    <ul
      v-if="suggestions === 'search' && open && visibleSuggestions.length > 0"
      class="menu menu-sm bg-base-200 rounded-box absolute left-0 top-full z-[100] mt-1 max-h-72 w-64 overflow-y-auto p-1 shadow-lg"
      role="listbox"
    >
      <li v-for="(s, i) in visibleSuggestions" :key="`${s.kind}:${s.name}`">
        <button
          type="button"
          role="option"
          :aria-selected="i === highlightIndex"
          :class="[
            'flex items-center gap-2',
            i === highlightIndex ? 'bg-primary text-primary-content' : '',
          ]"
          @mousedown.prevent="onSuggestionClick(s.name)"
          @mouseenter="highlightIndex = i"
        >
          <span class="truncate">{{ s.name }}</span>
          <span v-if="s.kind === 'auto'" class="badge badge-ghost badge-xs">auto</span>
          <span class="ml-auto opacity-60 text-xs">{{ s.count }}</span>
        </button>
      </li>
    </ul>
  </div>
</template>
