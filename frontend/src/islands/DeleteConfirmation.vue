<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{
  count: number
  samplePaths: string[]
  busy: boolean
}>()

const emit = defineEmits<{
  (e: 'confirm', permanent: boolean): void
  (e: 'close'): void
}>()

const mode = ref<'trash' | 'permanent'>('trash')
const confirmText = ref('')

const typedMatches = computed(
  () => mode.value === 'trash' || confirmText.value.trim() === String(props.count),
)
const confirmDisabled = computed(() => props.busy || !typedMatches.value)

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && !props.busy) emit('close')
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

watch(mode, (m) => { if (m === 'trash') confirmText.value = '' })

function submit() {
  if (confirmDisabled.value) return
  emit('confirm', mode.value === 'permanent')
}
</script>

<template>
  <div class="modal modal-open" role="dialog" aria-modal="true" @click.self="!busy && emit('close')">
    <div class="modal-box max-w-lg" @click.stop>
      <div class="flex items-center justify-between">
        <h2 class="text-lg font-semibold">
          Delete {{ count }} photo<span v-if="count !== 1">s</span>?
        </h2>
        <button
          type="button"
          class="btn btn-ghost btn-sm btn-square"
          aria-label="Close"
          :disabled="busy"
          @click="emit('close')"
        >✕</button>
      </div>

      <div class="mt-4 flex flex-col gap-4">
        <ul class="bg-base-200 max-h-36 overflow-auto rounded-box p-2 font-mono text-xs">
          <li v-for="p in samplePaths" :key="p" class="break-all py-0.5">{{ p }}</li>
          <li v-if="count > samplePaths.length" class="break-all py-0.5 text-base-content/50">
            …and {{ count - samplePaths.length }} more
          </li>
        </ul>

        <fieldset class="flex flex-col gap-2">
          <legend class="text-xs font-semibold uppercase tracking-wider text-base-content/60">
            Where does this go?
          </legend>
          <label
            :class="[
              'flex cursor-pointer items-start gap-3 rounded-box border p-3',
              mode === 'trash' ? 'border-primary' : 'border-base-300',
            ]"
          >
            <input type="radio" class="radio radio-primary radio-sm mt-0.5" value="trash" v-model="mode" name="delete-mode" />
            <span class="flex flex-col gap-0.5">
              <span class="font-medium">Move to Trash</span>
              <span class="text-xs text-base-content/60">Reversible. Files go to Kestrel's trash bin; Undo restores them.</span>
            </span>
          </label>
          <label
            :class="[
              'flex cursor-pointer items-start gap-3 rounded-box border p-3',
              mode === 'permanent' ? 'border-error' : 'border-base-300',
            ]"
          >
            <input type="radio" class="radio radio-error radio-sm mt-0.5" value="permanent" v-model="mode" name="delete-mode" />
            <span class="flex flex-col gap-0.5">
              <span class="font-medium">Delete Permanently</span>
              <span class="text-xs text-base-content/60">Unrecoverable. Bytes are unlinked immediately — no undo.</span>
            </span>
          </label>
        </fieldset>

        <label v-if="mode === 'permanent'" class="form-control">
          <span class="label-text text-xs">
            Type <strong>{{ count }}</strong> to confirm:
          </span>
          <input
            class="input input-sm input-bordered mt-1 font-mono"
            type="text"
            v-model="confirmText"
            :disabled="busy"
            autocomplete="off"
            spellcheck="false"
          />
        </label>
      </div>

      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="emit('close')">
          Cancel
        </button>
        <button
          type="button"
          class="btn btn-sm"
          :class="mode === 'permanent' ? 'btn-error' : 'btn-primary'"
          :disabled="confirmDisabled"
          @click="submit"
        >
          <span v-if="busy">Working…</span>
          <span v-else-if="mode === 'trash'">Move to Trash</span>
          <span v-else>Delete Permanently</span>
        </button>
      </div>
    </div>
  </div>
</template>
