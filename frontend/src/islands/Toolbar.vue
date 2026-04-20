<script setup lang="ts">
import {
  cellSize,
  CELL_SIZE_MIN,
  CELL_SIZE_MAX,
  CELL_SIZE_STEP,
} from '../transport/selection'
import { computed } from 'vue'
import { openTaggingQueue, openDuplicates } from '../transport/tagging'

const fillPct = computed(() => {
  const range = CELL_SIZE_MAX - CELL_SIZE_MIN
  return range > 0
    ? ((cellSize.value - CELL_SIZE_MIN) / range) * 100
    : 0
})
</script>

<template>
  <div class="toolbar">
    <button
      class="toolbar__action"
      type="button"
      @click="openDuplicates"
      title="Review near-identical duplicates"
    >
      <span class="toolbar__action-dot toolbar__action-dot--warn" aria-hidden="true"></span>
      Duplicates
    </button>
    <button
      class="toolbar__action"
      type="button"
      @click="openTaggingQueue"
      title="Tag whole clusters of visually similar photos at once"
    >
      <span class="toolbar__action-dot" aria-hidden="true"></span>
      Tag queue
    </button>
    <label class="toolbar__size">
      <span class="toolbar__label">Size</span>
      <span
        class="toolbar__slider-wrap"
        :style="{ '--fill-pct': fillPct + '%' }"
      >
        <input
          class="toolbar__slider"
          type="range"
          :min="CELL_SIZE_MIN"
          :max="CELL_SIZE_MAX"
          :step="CELL_SIZE_STEP"
          v-model.number="cellSize"
          aria-label="Thumbnail size"
        />
      </span>
      <span class="toolbar__value">{{ cellSize }}<span class="toolbar__unit">px</span></span>
    </label>
  </div>
</template>

<style scoped>
.toolbar {
  width: 100%;
  color: var(--text-secondary);
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-5);
}

.toolbar__action {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  height: 26px;
  padding: 0 var(--space-4);
  background: var(--surface-raised, rgba(255,255,255,0.04));
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-2, 6px);
  color: var(--text-primary);
  font-size: var(--fs-small);
  font-weight: var(--fw-medium);
  cursor: pointer;
  transition: background var(--dur-fast) var(--ease-out),
              border-color var(--dur-fast) var(--ease-out);
}
.toolbar__action:hover {
  background: var(--surface-active);
  border-color: var(--border-strong, var(--border-subtle));
}
.toolbar__action:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px var(--accent-glow);
}
.toolbar__action-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-full);
  background: var(--accent);
  box-shadow: 0 0 6px var(--accent-glow);
}
.toolbar__action-dot--warn {
  background: var(--warn, #ffb547);
  box-shadow: 0 0 6px rgba(255, 181, 71, 0.5);
}

.toolbar__size {
  display: inline-flex;
  align-items: center;
  gap: var(--space-4);
  height: 26px;
  padding: 0 var(--space-4);
  font-size: var(--fs-small);
}

.toolbar__label {
  font-size: var(--fs-micro);
  font-weight: var(--fw-semibold);
  letter-spacing: var(--tracking-micro);
  text-transform: uppercase;
  color: var(--text-muted);
  user-select: none;
}

.toolbar__slider-wrap {
  position: relative;
  display: inline-flex;
  align-items: center;
  width: 160px;
  height: 14px;
}
.toolbar__slider-wrap::before,
.toolbar__slider-wrap::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: 50%;
  transform: translateY(-50%);
  height: 3px;
  border-radius: var(--radius-full);
  pointer-events: none;
}
.toolbar__slider-wrap::before {
  background: var(--surface-active);
}
.toolbar__slider-wrap::after {
  right: auto;
  width: var(--fill-pct, 0%);
  background: var(--accent);
}

.toolbar__slider {
  position: relative;
  z-index: 1;
  width: 100%;
  height: 14px;
  margin: 0;
  padding: 0;
  background: transparent;
  -webkit-appearance: none;
  appearance: none;
  cursor: pointer;
}
.toolbar__slider:focus { outline: none; }
.toolbar__slider:focus-visible::-webkit-slider-thumb {
  box-shadow: 0 0 0 3px var(--accent-glow);
}
.toolbar__slider:focus-visible::-moz-range-thumb {
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.toolbar__slider::-webkit-slider-runnable-track {
  height: 14px;
  background: transparent;
  border: none;
}
.toolbar__slider::-moz-range-track {
  height: 3px;
  background: transparent;
  border: none;
}

.toolbar__slider::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  width: 12px;
  height: 12px;
  border-radius: var(--radius-full);
  background: var(--text-primary);
  border: none;
  cursor: grab;
  margin-top: 1px;
  transition: transform var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.toolbar__slider::-moz-range-thumb {
  width: 12px;
  height: 12px;
  border-radius: var(--radius-full);
  background: var(--text-primary);
  border: none;
  cursor: grab;
  transition: transform var(--dur-fast) var(--ease-out),
              background var(--dur-fast) var(--ease-out);
}
.toolbar__slider:hover::-webkit-slider-thumb { background: var(--accent); transform: scale(1.1); }
.toolbar__slider:hover::-moz-range-thumb { background: var(--accent); transform: scale(1.1); }
.toolbar__slider:active::-webkit-slider-thumb { cursor: grabbing; background: var(--accent); }
.toolbar__slider:active::-moz-range-thumb { cursor: grabbing; background: var(--accent); }

.toolbar__value {
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-small);
  font-variant-numeric: tabular-nums;
  min-width: 48px;
  text-align: right;
  user-select: none;
}
.toolbar__unit {
  color: var(--text-muted);
  margin-left: 1px;
}
</style>
