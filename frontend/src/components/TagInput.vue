<script setup lang="ts">
import { ref, watch } from 'vue'

// TagInput is a pill-based multi-value text input. Typing and pressing
// Space or Enter commits the current buffer as a pill. Backspace on an
// empty buffer removes the last pill. Tags are normalized (lowercase,
// trimmed, deduplicated) so the component matches the server's
// normalization contract — see internal/library.NormalizeTags.
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

// Local mirror so we can edit and emit in one place. Watch the prop so
// external resets (switching photos in the viewer, clearing search)
// flow in without round-tripping through the emit.
const tags = ref<string[]>([...(props.modelValue ?? [])])
watch(
  () => props.modelValue,
  (next) => {
    const incoming = next ?? []
    if (sameList(incoming, tags.value)) return
    tags.value = [...incoming]
  },
)

function sameList(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
  return true
}

function normalize(raw: string): string {
  return raw.trim().toLowerCase()
}

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
    // Space and Enter both commit. preventDefault on Enter stops it
    // from submitting any enclosing form; on Space it stops the
    // literal space from appearing in a fresh buffer.
    if (buffer.value.trim() !== '') {
      e.preventDefault()
      commitBuffer()
    } else if (e.key === 'Enter') {
      // Empty buffer + Enter: don't submit the form.
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
  // Commit whatever's in the buffer so a click-away doesn't silently
  // drop what the user typed.
  if (buffer.value.trim() !== '') commitBuffer()
}

function focus() {
  inputEl.value?.focus()
}

defineExpose({ focus })
</script>

<template>
  <div class="taginput" @click="focus">
    <span
      v-for="(tag, i) in tags"
      :key="tag"
      class="taginput__pill"
    >
      {{ tag }}
      <button
        type="button"
        class="taginput__remove"
        :aria-label="`Remove tag ${tag}`"
        @click.stop="removeAt(i)"
      >×</button>
    </span>
    <input
      ref="inputEl"
      v-model="buffer"
      class="taginput__field"
      type="text"
      :placeholder="tags.length ? '' : (placeholder ?? 'Add tag…')"
      :aria-label="ariaLabel ?? 'Tags'"
      @keydown="onKeydown"
      @blur="onBlur"
    />
  </div>
</template>

<style scoped>
.taginput {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  align-items: center;
  background: var(--surface-inset);
  color: var(--text-primary);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  box-shadow: var(--elev-inset);
  min-height: 36px;
  cursor: text;
}
.taginput:focus-within { border-color: var(--accent); }

.taginput__pill {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  border: var(--border-thin) solid var(--accent);
  color: var(--accent);
  border-radius: var(--radius-full);
  padding: var(--space-1) var(--space-3);
  font-size: var(--fs-caption);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  line-height: 1;
}
.taginput__remove {
  background: transparent;
  border: none;
  color: inherit;
  padding: 0;
  cursor: pointer;
  font-size: var(--fs-body);
  line-height: 1;
}
.taginput__remove:hover { color: var(--accent-hover); }

.taginput__field {
  flex: 1;
  min-width: 120px;
  background: transparent;
  border: none;
  outline: none;
  color: var(--text-primary);
  font: var(--fw-regular) var(--fs-default) / 1.2 var(--font-sans);
  padding: var(--space-1) 0;
}
.taginput__field::placeholder { color: var(--text-muted); }
</style>
