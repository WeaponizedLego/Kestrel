<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{
  modelValue: string[]
  placeholder?: string
  ariaLabel?: string
}>()

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

function commitBuffer() {
  const clean = normalize(buffer.value)
  buffer.value = ''
  if (!clean) return
  if (tags.value.includes(clean)) return
  tags.value = [...tags.value, clean]
  emit('update:modelValue', tags.value)
}

function removeAt(index: number) {
  tags.value = tags.value.filter((_, i) => i !== index)
  emit('update:modelValue', tags.value)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === ' ' || e.key === 'Enter') {
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
  if (buffer.value.trim() !== '') commitBuffer()
}

function focus() { inputEl.value?.focus() }
defineExpose({ focus })
</script>

<template>
  <div
    class="input input-sm input-bordered flex min-h-9 h-auto flex-wrap items-center gap-1 py-1 cursor-text"
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
      @keydown="onKeydown"
      @blur="onBlur"
    />
  </div>
</template>
