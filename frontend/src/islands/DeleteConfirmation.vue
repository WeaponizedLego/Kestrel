<script setup lang="ts">
// Destructive-action confirmation. Default path is Trash (undoable);
// Permanent is gated behind an extra typed-confirmation step so a
// mis-click can't evaporate photos. Shares the FolderPicker's dialog
// skeleton + styles for visual cohesion.

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

// To permanently delete, the user must type the count. Unique enough
// that muscle memory of hitting Enter can't bypass it.
const typedMatches = computed(
  () => mode.value === 'trash' || confirmText.value.trim() === String(props.count),
)
const confirmDisabled = computed(() => props.busy || !typedMatches.value)

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && !props.busy) emit('close')
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

// Clear the typed confirmation whenever the mode resets back to trash
// so switching back and forth can't leak a stale value.
watch(mode, (m) => {
  if (m === 'trash') confirmText.value = ''
})

function submit() {
  if (confirmDisabled.value) return
  emit('confirm', mode.value === 'permanent')
}
</script>

<template>
  <div class="confirm" role="dialog" aria-modal="true" @click.self="!busy && emit('close')">
    <div class="confirm__panel" @click.stop>
      <header class="confirm__head">
        <h2 class="confirm__title">
          Delete {{ count }} photo<span v-if="count !== 1">s</span>?
        </h2>
        <button
          class="confirm__close"
          aria-label="Close"
          :disabled="busy"
          @click="emit('close')"
        >✕</button>
      </header>

      <div class="confirm__body">
        <ul class="confirm__samples">
          <li v-for="p in samplePaths" :key="p" class="confirm__sample">{{ p }}</li>
          <li v-if="count > samplePaths.length" class="confirm__sample confirm__sample--muted">
            …and {{ count - samplePaths.length }} more
          </li>
        </ul>

        <fieldset class="confirm__modes">
          <legend class="confirm__legend">Where does this go?</legend>
          <label class="confirm__mode" :class="{ 'confirm__mode--active': mode === 'trash' }">
            <input type="radio" value="trash" v-model="mode" name="delete-mode" />
            <span class="confirm__mode-body">
              <span class="confirm__mode-title">Move to Trash</span>
              <span class="confirm__mode-note">Reversible. Files go to Kestrel's trash bin; Undo restores them.</span>
            </span>
          </label>
          <label class="confirm__mode" :class="{ 'confirm__mode--active': mode === 'permanent' }">
            <input type="radio" value="permanent" v-model="mode" name="delete-mode" />
            <span class="confirm__mode-body">
              <span class="confirm__mode-title">Delete Permanently</span>
              <span class="confirm__mode-note">Unrecoverable. Bytes are unlinked immediately — no undo.</span>
            </span>
          </label>
        </fieldset>

        <label v-if="mode === 'permanent'" class="confirm__typed">
          <span class="confirm__typed-label">
            Type <strong>{{ count }}</strong> to confirm:
          </span>
          <input
            class="confirm__typed-input"
            type="text"
            v-model="confirmText"
            :disabled="busy"
            autocomplete="off"
            spellcheck="false"
          />
        </label>
      </div>

      <footer class="confirm__foot">
        <button class="confirm__btn confirm__btn--ghost" :disabled="busy" @click="emit('close')">
          Cancel
        </button>
        <button
          class="confirm__btn"
          :class="mode === 'permanent' ? 'confirm__btn--danger' : 'confirm__btn--primary'"
          :disabled="confirmDisabled"
          @click="submit"
        >
          <span v-if="busy">Working…</span>
          <span v-else-if="mode === 'trash'">Move to Trash</span>
          <span v-else>Delete Permanently</span>
        </button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.confirm {
  position: fixed;
  inset: 0;
  background: rgba(5, 5, 7, 0.68);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 950;
  animation: confirm-fade var(--dur-base) var(--ease-out);
}
@keyframes confirm-fade {
  from { opacity: 0; }
  to   { opacity: 1; }
}

.confirm__panel {
  width: min(480px, 92vw);
  max-height: 80vh;
  background: var(--surface-elevated);
  color: var(--text-primary);
  border-radius: var(--radius-lg);
  box-shadow: var(--elev-modal);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  animation: confirm-rise var(--dur-slow) var(--ease-out);
}
@keyframes confirm-rise {
  from { transform: translateY(8px); opacity: 0; }
  to   { transform: translateY(0); opacity: 1; }
}

.confirm__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  height: 44px;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--border-subtle);
}
.confirm__title {
  margin: 0;
  font-size: var(--fs-emphasis);
  font-weight: var(--fw-semibold);
  letter-spacing: var(--tracking-tight);
}
.confirm__close {
  width: 24px;
  height: 24px;
  background: transparent;
  color: var(--text-muted);
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.confirm__close:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--surface-hover);
}
.confirm__close:disabled { opacity: 0.4; cursor: not-allowed; }

.confirm__body {
  padding: var(--space-5);
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  overflow: auto;
}

.confirm__samples {
  list-style: none;
  margin: 0;
  padding: var(--space-3);
  background: var(--surface-inset);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  font: var(--fw-regular) var(--fs-caption) / 1.4 var(--font-mono);
  color: var(--text-secondary);
  max-height: 140px;
  overflow: auto;
}
.confirm__sample { overflow-wrap: anywhere; padding: 2px 0; }
.confirm__sample--muted { color: var(--text-muted); }

.confirm__modes {
  border: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.confirm__legend {
  font: var(--fw-semibold) var(--fs-micro) / 1 var(--font-sans);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  color: var(--text-muted);
  margin-bottom: var(--space-2);
}
.confirm__mode {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--surface-raised, rgba(255,255,255,0.02));
  cursor: pointer;
  transition: border-color var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.confirm__mode:hover { background: var(--surface-hover); }
.confirm__mode--active {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent-glow);
}
.confirm__mode input {
  margin-top: 2px;
  accent-color: var(--accent);
}
.confirm__mode-body { display: flex; flex-direction: column; gap: 2px; }
.confirm__mode-title {
  font-size: var(--fs-small);
  font-weight: var(--fw-medium);
  color: var(--text-primary);
}
.confirm__mode-note {
  font-size: var(--fs-caption);
  color: var(--text-muted);
}

.confirm__typed {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.confirm__typed-label {
  font-size: var(--fs-caption);
  color: var(--text-secondary);
}
.confirm__typed-input {
  height: 30px;
  padding: 0 var(--space-3);
  font: var(--fw-regular) var(--fs-small) / 1 var(--font-mono);
  color: var(--text-primary);
  background: var(--surface-inset);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  outline: none;
  transition: border-color var(--dur-fast) var(--ease-out);
}
.confirm__typed-input:focus { border-color: var(--accent); }

.confirm__foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-top: 1px solid var(--border-subtle);
}
.confirm__btn {
  height: 28px;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: 0 var(--space-5);
  font: var(--fw-medium) var(--fs-small) / 1 var(--font-sans);
  cursor: pointer;
  transition: background var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out),
              color var(--dur-fast) var(--ease-out);
}
.confirm__btn:disabled { opacity: 0.35; cursor: not-allowed; }
.confirm__btn--ghost { background: transparent; color: var(--text-secondary); }
.confirm__btn--ghost:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--surface-hover);
  border-color: var(--border-strong);
}
.confirm__btn--primary {
  background: var(--accent);
  color: #0A0A0B;
  border-color: var(--accent);
}
.confirm__btn--primary:hover:not(:disabled) {
  background: var(--accent-hover);
  border-color: var(--accent-hover);
}
.confirm__btn--danger {
  background: var(--danger, #d94f4f);
  color: #fff;
  border-color: var(--danger, #d94f4f);
}
.confirm__btn--danger:hover:not(:disabled) {
  filter: brightness(1.1);
}
</style>
