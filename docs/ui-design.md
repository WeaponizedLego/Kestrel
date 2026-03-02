# 🖼️ UI Design — Kestrel

> This document describes the frontend architecture, component design, and interaction patterns for Kestrel.
> It is referenced by `.github/copilot-instructions.md` and should be consulted when designing or reviewing frontend code.

---

## Frontend Architecture

| Concern              | Choice                                                                                                    |
| -------------------- | --------------------------------------------------------------------------------------------------------- |
| **Framework**        | Vue 3 (Composition API with `<script setup lang="ts">`)                                                   |
| **Build tool**       | Vite                                                                                                      |
| **Desktop bridge**   | Wails v3 — auto-generated TypeScript bindings                                                             |
| **State management** | Vue `ref` / `reactive` for UI state; Go is the source of truth for library data                           |
| **Styling**          | TBD — decide before first component implementation (options: Tailwind CSS, CSS Modules, plain scoped CSS) |

---

## Wails v3 Frontend Patterns

### Calling Go Services from Vue

Wails v3 generates TypeScript bindings for every exported method on registered Go services.
Import them from the generated bindings directory and use `async/await`.

```html
<script setup lang="ts">
  import { ref, onMounted } from 'vue'
  import { GetPhotos } from '../bindings/services/LibraryService'

  const photos = ref<Photo[]>([])
  const loading = ref(true)
  const error = ref<string | null>(null)

  onMounted(async () => {
    try {
      photos.value = await GetPhotos()
    } catch (err) {
      error.value = `Failed to load library: ${err}`
      console.error(error.value)
    } finally {
      loading.value = false
    }
  })
</script>
```

### Listening to Go Events

Use Wails v3 event APIs to receive progress updates from background Go tasks (e.g., scanning).

```ts
import { Events } from '@wailsio/runtime'

Events.On('scan:progress', (data: { processed: number; total: number }) => {
  scanProgress.value = data
})
```

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
│       └── PhotoViewer.vue   # Full-resolution image view
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
| **Library data** (photos, metadata) | Go in-memory map                          | `LibraryService.GetPhotos()`                       |
| **Sorted / filtered views**         | Go — sort and filter in Go, return result | `LibraryService.GetPhotosSorted(field, order)`     |
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
                    └─▶ Emit thumbnail:ready event to frontend

Frontend renders
  └─▶ PhotoGrid sends SetViewport(startIndex, endIndex) to Go
        └─▶ Go pre-fetcher loads visible + lookahead thumbnails into LRU cache
              └─▶ Frontend receives thumbnails via binding call or event
                    └─▶ Cache hit → served from RAM (zero disk I/O)
                    └─▶ Cache miss → loaded from thumbs.pack on demand (< 5 ms from SSD)
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

1. **Never sort or filter in JavaScript.** Always call a Go service method that returns pre-sorted/filtered data.
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
