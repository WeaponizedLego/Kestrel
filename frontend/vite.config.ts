import { defineConfig, type PluginOption } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// The Go server reads its actual port back from the listener, so dev
// proxying needs a stable target. Set KESTREL_DEV_BACKEND (e.g.
// "http://127.0.0.1:5174") before `npm run dev`; defaults to a
// conventional dev port.
const devBackend = process.env.KESTREL_DEV_BACKEND ?? 'http://127.0.0.1:5174'

// In production the Go server substitutes the KESTREL_TOKEN_META
// placeholder with a freshly-generated per-run token. In dev Vite
// serves index.html directly, so we need to inject a fixed token
// here. The Go binary uses the same constant when launched with
// --dev, keeping both sides in sync. The value is not a secret —
// dev mode is loopback-only and the token is cosmetic.
const devToken = 'dev-kestrel-token'

function injectDevToken(): PluginOption {
  return {
    name: 'kestrel-dev-token',
    apply: 'serve',
    transformIndexHtml(html) {
      return html.replace(
        '<!--KESTREL_TOKEN_META-->',
        `<meta name="kestrel-token" content="${devToken}">`,
      )
    },
  }
}

const islandEntries = {
  sidebar:      'src/islands/Sidebar.entry.ts',
  toolbar:      'src/islands/Toolbar.entry.ts',
  photoGrid:    'src/islands/PhotoGrid.entry.ts',
  statusBar:    'src/islands/StatusBar.entry.ts',
  taggingQueue: 'src/islands/TaggingQueue.entry.ts',
  duplicates:   'src/islands/Duplicates.entry.ts',
}

export default defineConfig({
  plugins: [vue(), injectDevToken()],
  build: {
    // Vite writes into internal/assets/dist/ so //go:embed picks the
    // output up directly. emptyOutDir clears stale files between builds.
    outDir: fileURLToPath(new URL('../internal/assets/dist', import.meta.url)),
    emptyOutDir: true,
    manifest: true,
    rollupOptions: {
      // index.html stays the HTML entry; the named JS entries below
      // force Vite to emit one chunk per island so a future change to
      // PhotoGrid doesn't bust every island's cache.
      input: {
        index: fileURLToPath(new URL('./index.html', import.meta.url)),
        ...Object.fromEntries(
          Object.entries(islandEntries).map(([name, path]) => [
            name,
            fileURLToPath(new URL(`./${path}`, import.meta.url)),
          ]),
        ),
      },
    },
  },
  server: {
    proxy: {
      '/api': { target: devBackend, changeOrigin: true },
      '/ws':  { target: devBackend, changeOrigin: true, ws: true },
    },
  },
})
