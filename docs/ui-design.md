# 🖼️ UI Design — Kestrel

> This document describes the frontend architecture, component design, and interaction patterns for Kestrel.
> It is referenced by `.github/copilot-instructions.md` and should be consulted when designing or reviewing frontend code.

---

## Frontend Architecture

| Concern              | Choice                                                                                                    |
| -------------------- | --------------------------------------------------------------------------------------------------------- |
| **Framework**        | Vue 3 (Composition API with `<script setup lang="ts">`)                                                   |
| **Build tool**       | Vite (multi-entry: one bundle per island + a build-time-rendered static shell)                            |
| **Hydration model**  | Manual islands — the HTML shell is static; each interactive region mounts its own Vue app                 |
| **Transport**        | REST (`fetch`) for commands, a single WebSocket for server-pushed events                                  |
| **Shell**            | User's default browser, opened by the Go binary at a loopback URL                                         |
| **State management** | Vue `ref` / `reactive` for UI state; Go is the source of truth for library data                           |
| **Styling**          | Plain scoped CSS + design tokens — see [`visual-design.md`](visual-design.md) for the tactile system      |

---

## Island Hydration Model

The build emits a **mostly-static HTML shell** plus one JS bundle per interactive region.
Each region — `PhotoGrid`, `PhotoViewer`, `Sidebar`, `Toolbar`, `StatusBar`, `TaggingQueue` —
is its own Vue app, mounted on a DOM node the shell placed for it. There is no root `<App>`
hydrating the whole tree.

**Why:** most of the chrome never re-renders. Paying the cost of hydrating the entire tree
on every startup would be wasteful, and it gives us no practical benefit over mounting only
what's interactive.

### Island entry shape

```ts
// frontend/src/islands/PhotoGrid.entry.ts
import { createApp } from 'vue'
import PhotoGrid from './PhotoGrid.vue'

const el = document.querySelector('[data-island="photo-grid"]')
if (el) createApp(PhotoGrid).mount(el)
```

`vite.config.ts` is configured with one entry per island. The shell HTML is rendered at
build time by a small Node script (`frontend/scripts/render-shell.ts`) using
`@vue/server-renderer` on static shell components, and `<script type="module">` tags for
each island are injected into the output.

---

## Calling the Go Backend

### REST (commands, queries)

A shared `api.ts` wraps `fetch`, attaches the session auth token (read from a meta tag the
Go server injected into `index.html`), and serialises JSON.

```ts
// frontend/src/transport/api.ts
const token = document.querySelector<HTMLMetaElement>('meta[name="kestrel-token"]')!.content

export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { 'X-Kestrel-Token': token } })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function apiPost<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'X-Kestrel-Token': token, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}
```

Consumed from an island:

```html
<script setup lang="ts">
  import { ref, onMounted } from 'vue'
  import { apiGet } from '../transport/api'
  import type { Photo } from '../types'

  const photos = ref<Photo[]>([])
  const loading = ref(true)
  const error = ref<string | null>(null)

  onMounted(async () => {
    try {
      photos.value = await apiGet<Photo[]>('/api/photos')
    } catch (err) {
      error.value = `Failed to load library: ${err}`
      console.error(error.value)
    } finally {
      loading.value = false
    }
  })
</script>
```

### WebSocket (server-pushed events)

A singleton WebSocket in `frontend/src/transport/events.ts` connects once, auto-reconnects
with backoff, and routes messages to typed listeners.

```ts
// frontend/src/transport/events.ts
type Listener = (payload: any) => void
const listeners = new Map<string, Set<Listener>>()

const token = document.querySelector<HTMLMetaElement>('meta[name="kestrel-token"]')!.content
const ws = new WebSocket(`ws://${location.host}/ws?token=${token}`)

ws.addEventListener('message', (ev) => {
  const { kind, payload } = JSON.parse(ev.data)
  listeners.get(kind)?.forEach((fn) => fn(payload))
})

export function onEvent(kind: string, fn: Listener) {
  if (!listeners.has(kind)) listeners.set(kind, new Set())
  listeners.get(kind)!.add(fn)
  return () => listeners.get(kind)!.delete(fn)
}
```

Usage inside an island:

```ts
import { onEvent } from '../transport/events'

onEvent('scan:progress', (data: { processed: number; total: number }) => {
  scanProgress.value = data
})
```

Commands always go through REST; the WebSocket is for server-pushed events only
(`scan:progress`, `thumbnail:ready`, `library:updated`, `clusters:ready`, …).

### Event catalog

| Event              | Payload shape                                              | Emitted by                  |
| ------------------ | ---------------------------------------------------------- | --------------------------- |
| `scan:progress`    | `{ processed: number, total: number }`                     | Scanner worker pool         |
| `thumbnail:ready`  | `{ path: string }`                                         | Pre-fetcher / LRU cache     |
| `library:updated`  | `{ reason: "scan" \| "tag-apply" \| "delete" }`            | Library mutations           |
| `clusters:ready`   | `{ kind: "duplicate" \| "similar" }`                       | `internal/library/cluster/` background compute (see `docs/assisted-tagging.md`) |

---

## Component Hierarchy (Planned)

```
App.vue
├── AppShell.vue              # Top-level layout: sidebar + main area
│   ├── Sidebar.vue           # Folder tree, collections, filters
│   ├── Toolbar.vue           # Sort, search, view toggles
│   └── MainArea.vue
│       ├── PhotoGrid.vue     # Virtualized thumbnail grid
│       │   └── PhotoCard.vue # Single thumbnail + overlay info
│       ├── PhotoViewer.vue   # Full-resolution image view
│       └── TaggingQueue.vue  # Cluster-first tagging view (see docs/assisted-tagging.md)
└── StatusBar.vue             # Scan progress, photo count, memory usage
```

**Rules:**

- Each `.vue` file = one component with a single clear responsibility.
- File names use `PascalCase.vue`.
- Components that call Go services should handle loading/error states explicitly.

---

## State Management Approach

### Principle: Go Owns the Data, Vue Owns the UI

| State type                          | Where it lives                            | Example                                            |
| ----------------------------------- | ----------------------------------------- | -------------------------------------------------- |
| **Library data** (photos, metadata) | Go in-memory map                          | `GET /api/photos`                                  |
| **Sorted / filtered views**         | Go — sort and filter in Go, return result | `GET /api/photos?sort=date&order=desc`             |
| **UI-only state**                   | Vue `ref` / `reactive`                    | `selectedPhotoId`, `sidebarOpen`, `gridColumns`    |
| **Transient interaction state**     | Vue `ref`                                 | `isScrolling`, `isDragging`, `contextMenuPosition` |

**Why:** Sorting / filtering 20,000+ items must happen in Go for performance. Vue receives pre-sorted slices and renders them. This prevents jank and keeps the frontend thin.

---

## Thumbnail Strategy

Thumbnails are **persisted** in a packed binary file (`thumbs.pack`) and served to the frontend from a memory-budgeted LRU cache. The frontend never reads thumbnails from disk directly.

```
Scan (Go background)
  └─▶ Generate thumbnail (256×256 JPEG)
        └─▶ Append to thumbs.pack (persistent disk cache)
              └─▶ Insert into LRU thumbnail cache (if budget allows)
                    └─▶ Broadcast thumbnail:ready on the WebSocket hub

Frontend renders
  └─▶ PhotoGrid POSTs /api/viewport with {startIndex, endIndex}
        └─▶ Go pre-fetcher loads visible + lookahead thumbnails into LRU cache
              └─▶ Frontend fetches each thumbnail from GET /api/thumbnail?path=...
                    └─▶ Cache hit → served from RAM (zero disk I/O)
                    └─▶ Cache miss → loaded from thumbs.pack on demand (< 5 ms from SSD)
              └─▶ Late arrivals (cache miss filled by pre-fetcher) trigger a
                  thumbnail:ready WS event so the grid can swap placeholders
```

**Key rules:**

- Thumbnails are generated **once** during scan and persisted to `thumbs.pack`.
- A memory-budgeted LRU cache holds the hot subset of thumbnails in RAM.
- The **pre-fetcher** predicts which thumbnails the user needs next (scroll direction, child folders) and loads them before the user gets there.
- Thumbnails currently in the **viewport are pinned** in the cache and never evicted.
- For small libraries (< ~200K photos), all thumbnails fit in RAM (eager mode) — the original zero-I/O experience.
- For large libraries (200K+), the system gracefully degrades to tiered loading with a > 95% cache hit rate target.
- Full-resolution images are loaded on demand only when the user opens the viewer.

> 📖 See `docs/system-design.md` → **Thumbnail Cache Architecture** for the `ThumbnailProvider` interface, LRU eviction tiers, and `thumbs.pack` file format.

---

## Virtualized Scrolling

The photo grid must handle 20,000+ thumbnails without creating 20,000 DOM nodes.

**Approach:**

- Use a virtualized list/grid component (e.g., `vue-virtual-scroller` or a custom implementation).
- Only render visible items + a small buffer above/below the viewport.
- Recycle DOM nodes as the user scrolls.

**Performance budget:** The grid must maintain 60 fps scrolling. If it doesn't, the bottleneck is likely DOM node count or un-virtualized rendering.

---

## Performance Rules for Frontend Code

1. **Never sort or filter in JavaScript.** Always call a REST endpoint that returns pre-sorted/filtered data from the Go in-memory store.
2. **Never hold the full photo list in multiple Vue refs.** Keep one canonical list; use computed properties for derived views.
3. **Virtualize all large lists.** No exceptions for grids with > 100 items.
4. **Debounce search input.** Wait 200–300 ms after the last keystroke before calling Go.
5. **Lazy-load heavy components.** The full-resolution viewer should be loaded on demand, not at startup.

---

## Accessibility & UX Notes

- Keyboard navigation: arrow keys to move between photos in the grid, Enter to open viewer, Escape to close.
- Focus management: when switching between grid and viewer, focus should move predictably.
- Loading states: always show a skeleton or spinner during async Go calls — never a blank screen.
- Error states: show user-friendly messages, not raw stack traces.
