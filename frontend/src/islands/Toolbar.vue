<script setup lang="ts">
import {
  cellSize,
  CELL_SIZE_MIN,
  CELL_SIZE_MAX,
  CELL_SIZE_STEP,
} from '../transport/selection'
</script>

<template>
  <div class="toolbar">
    <label class="toolbar__size">
      <span class="toolbar__label">Size</span>
      <input
        class="toolbar__slider"
        type="range"
        :min="CELL_SIZE_MIN"
        :max="CELL_SIZE_MAX"
        :step="CELL_SIZE_STEP"
        v-model.number="cellSize"
        aria-label="Thumbnail size"
      />
      <span class="toolbar__value">{{ cellSize }}px</span>
    </label>
  </div>
</template>

<style scoped>
.toolbar {
  color: var(--text-secondary);
  font-size: var(--fs-body);
  display: flex;
  align-items: center;
  justify-content: flex-end;
}
.toolbar__size {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  background: var(--surface-inset);
  border: var(--border-thin) solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  box-shadow: var(--elev-inset);
}
.toolbar__label {
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--text-secondary);
}
.toolbar__slider {
  width: 180px;
  accent-color: var(--accent);
  cursor: pointer;
}
.toolbar__value {
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
  min-width: 3.5ch;
  text-align: right;
}
</style>
