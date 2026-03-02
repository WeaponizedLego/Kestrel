# 🖼️ UI Design — Kestrel

> This document describes the frontend architecture, component design, and interaction patterns for Kestrel.
> It is referenced by `.github/copilot-instructions.md` and should be consulted when designing or reviewing frontend code.

---

## Frontend Architecture

| Concern | Choice |
|---|---|
| **Framework** | Vue 3 (Composition API with `<script setup lang="ts">`) |
| **Build tool** | Vite |
| **Desktop bridge** | Wails v3 — auto-generated TypeScript bindings |
| **State management** | Vue `ref` / `reactive` for UI state; Go is the source of truth for library data |
| **Styling** | TBD — decide before first component implementation (options: Tailwind CSS, CSS Modules, plain scoped CSS) |

---

## Wails v3 Frontend Patterns

### Calling Go Services from Vue

Wails v3 generates TypeScript bindings for every exported method on registered Go services.
Import them from the generated bindings directory and use `async/await`.

```html
<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { GetPhotos } from '../bindings/services/LibraryService';

const photos = ref<Photo[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    photos.value = await GetPhotos();
  } catch (err) {
    error.value = `Failed to load library: ${err}`;
    console.error(error.value);
  } finally {
    loading.value = false;
  }
});
</script>
```

### Listening to Go Events

Use Wails v3 event APIs to receive progress updates from background Go tasks (e.g., scanning).

```ts
import { Events } from '@wailsio/runtime';

Events.On('scan:progress', (data: { processed: number; total: number }) => {
  scanProgress.value = data;
});
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

| State type | Where it lives | Example |
|---|---|---|
| **Library data** (photos, metadata) | Go in-memory map | `LibraryService.GetPhotos()` |
| **Sorted / filtered views** | Go — sort and filter in Go, return result | `LibraryService.GetPhotosSorted(field, order)` |
| **UI-only state** | Vue `ref` / `reactive` | `selectedPhotoId`, `sidebarOpen`, `gridColumns` |
| **Transient interaction state** | Vue `ref` | `isScrolling`, `isDragging`, `contextMenuPosition` |

**Why:** Sorting / filtering 20,000+ items must happen in Go for performance. Vue receives pre-sorted slices and renders them. This prevents jank and keeps the frontend thin.

---

## Thumbnail Strategy

```
Scan (Go background)
  └─▶ Generate thumbnail (e.g., 256×256 JPEG)
        └─▶ Cache on local SSD (configurable path)
              └─▶ Store thumbnail bytes in memory (in-memory map)
                    └─▶ Serve to frontend as base64 or via local asset endpoint

Frontend renders
  └─▶ PhotoGrid requests visible thumbnails only (virtualized)
        └─▶ Display from in-memory cache — no disk reads during scroll
```

**Key rules:**
- Thumbnails are generated **once** during scan and cached.
- During browsing, thumbnails are served from RAM — never re-read from disk.
- Full-resolution images are loaded on demand only when the user opens the viewer.

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
