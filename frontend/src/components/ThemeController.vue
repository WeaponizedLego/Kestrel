<script setup lang="ts">
import { ref } from 'vue'
import { apiPut } from '../transport/api'

// Must match the themes enabled in src/shell/app.css.
const themes = [
  'dark', 'light', 'cupcake', 'bumblebee', 'emerald', 'corporate',
  'synthwave', 'retro', 'cyberpunk', 'valentine', 'halloween', 'garden',
  'forest', 'aqua', 'lofi', 'pastel', 'fantasy', 'wireframe', 'black',
  'luxury', 'dracula', 'cmyk', 'autumn', 'business', 'acid', 'lemonade',
  'night', 'coffee', 'winter', 'dim', 'nord', 'sunset',
]

// Read the persisted theme synchronously: the Go server injected it
// into <meta name="kestrel-theme"> on this request, and the inline
// bootstrap in index.html already applied it to <html data-theme>
// before any island mounted. Falls back to whatever the DOM has so
// dev mode (where Vite serves index.html and no meta is rewritten)
// still picks up the compiled-in default.
function initialTheme(): string {
  const meta = document.querySelector<HTMLMetaElement>('meta[name="kestrel-theme"]')
  if (meta && meta.content) return meta.content
  return document.documentElement.dataset.theme || 'dark'
}

const current = ref(initialTheme())

function pick(theme: string) {
  current.value = theme
  document.documentElement.dataset.theme = theme
  // Fire-and-forget: a failed PUT just means the theme reverts on the
  // next launch. Surfacing a toast for "couldn't save your theme"
  // would be louder than the cost of losing it.
  apiPut('/api/settings', { theme }).catch((err) => {
    console.warn('persisting theme failed', err)
  })
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
