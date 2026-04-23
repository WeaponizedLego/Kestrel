<script setup lang="ts">
import { onMounted, ref } from 'vue'

const STORAGE_KEY = 'kestrel-theme'

// Must match the themes enabled in src/shell/app.css.
const themes = [
  'dark', 'light', 'cupcake', 'bumblebee', 'emerald', 'corporate',
  'synthwave', 'retro', 'cyberpunk', 'valentine', 'halloween', 'garden',
  'forest', 'aqua', 'lofi', 'pastel', 'fantasy', 'wireframe', 'black',
  'luxury', 'dracula', 'cmyk', 'autumn', 'business', 'acid', 'lemonade',
  'night', 'coffee', 'winter', 'dim', 'nord', 'sunset',
]

const current = ref('dark')

onMounted(() => {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored) current.value = stored
  else current.value = document.documentElement.dataset.theme || 'dark'
})

function pick(theme: string) {
  current.value = theme
  document.documentElement.dataset.theme = theme
  try { localStorage.setItem(STORAGE_KEY, theme) } catch {}
}
</script>

<template>
  <div class="dropdown dropdown-end">
    <div tabindex="0" role="button" class="btn btn-ghost btn-sm">
      Theme
      <span class="badge badge-ghost badge-sm ml-1">{{ current }}</span>
      <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="6 9 12 15 18 9" /></svg>
    </div>
    <ul tabindex="0" class="dropdown-content menu bg-base-200 rounded-box z-50 mt-2 max-h-96 w-52 overflow-y-auto p-2 shadow-lg">
      <li v-for="theme in themes" :key="theme">
        <button
          type="button"
          :class="{ 'menu-active': theme === current }"
          @click="pick(theme)"
        >
          <span
            class="inline-block h-3 w-3 rounded-full border border-base-content/20"
            :data-theme="theme"
            :style="{ background: 'var(--color-primary, currentColor)' }"
          />
          {{ theme }}
        </button>
      </li>
    </ul>
  </div>
</template>
