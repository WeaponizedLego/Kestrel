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
  background: var(--surface-raised);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: var(--space-2) var(--space-3);
  min-height: 28px;
  cursor: text;
  transition: border-color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.taginput:hover { border-color: var(--border-strong); }
.taginput:focus-within {
  border-color: var(--accent);
  background: var(--surface-hover);
}

.taginput__pill {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  background: var(--accent-wash);
  color: var(--accent);
  border: 1px solid transparent;
  border-radius: var(--radius-xs);
  padding: 0 var(--space-2) 0 var(--space-3);
  height: 18px;
  font-size: var(--fs-micro);
  font-weight: var(--fw-medium);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  line-height: 1;
  transition: background var(--dur-fast) var(--ease-out);
}
.taginput__pill:hover { background: var(--accent-wash-strong); }
.taginput__remove {
  background: transparent;
  border: none;
  color: var(--accent);
  padding: 0;
  width: 12px;
  height: 12px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
  opacity: 0;
  transition: opacity var(--dur-fast) var(--ease-out),
              color var(--dur-fast) var(--ease-out);
}
.taginput__pill:hover .taginput__remove { opacity: 1; }
.taginput__remove:hover { color: var(--accent-hover); }
.taginput__remove:focus-visible {
  opacity: 1;
  box-shadow: none;
  outline: 1px solid var(--accent);
  border-radius: var(--radius-xs);
}

.taginput__field {
  flex: 1;
  min-width: 100px;
  background: transparent;
  border: none;
  outline: none;
  color: var(--text-primary);
  font: var(--fw-regular) var(--fs-small) / 1.2 var(--font-sans);
  letter-spacing: var(--tracking-tight);
  padding: 0;
  height: 18px;
}
.taginput__field::placeholder {
  color: var(--text-muted);
  letter-spacing: var(--tracking-tight);
}
</style>
