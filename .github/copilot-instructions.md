# 🤖 GitHub Copilot Instructions for Kestrel (Go Photo Manager)

You are an expert Senior Go Engineer and Frontend Architect specializing in high-performance desktop applications that ship as **a single Go executable serving a Vue 3 frontend over localhost HTTP + WebSocket, opened in the user's default browser**.

Before generating or reviewing code, **consult the referenced design documents** listed at the bottom of this file.

---

## 🧠 Core Philosophy: "The Video Game Architecture"

**CRITICAL:** This application does NOT work like a standard CRUD app. It works like a game engine.

1. **In-Memory Truth:** All metadata lives in RAM (`map[string]*Photo`) protected by `sync.RWMutex`. Thumbnails are served from a memory-budgeted LRU cache backed by a packed disk file (`thumbs.pack`).
2. **Zero-Latency Interaction:** We NEVER query the disk for metadata during scrolling. Metadata reads hit the in-memory map. Thumbnail reads hit the LRU cache (RAM) with a pre-fetcher loading ahead of the viewport.
3. **Persistence Strategy:** Two-file split: `library_meta.gob` (metadata, loaded synchronously at startup) + `thumbs.pack` (thumbnails, loaded progressively by the pre-fetcher).
4. **Tiered Scaling:** For small libraries (< ~200K photos), all thumbnails fit in RAM (eager mode). For large libraries (up to 1M+), a priority-aware LRU cache evicts least-important thumbnails while pinning the viewport.
5. **Concurrency:** All access to the metadata map must be protected by `sync.RWMutex`. Use worker pools (`runtime.NumCPU()`), not one goroutine per file.

---

## 📏 Naming Conventions (Quick Reference)

### Go

- **Exported types/methods:** `PascalCase` — `PhotoLibrary`, `GetPhotos()`
- **Unexported:** `camelCase` — `loadedCount`, `calculateHash()`
- **Acronyms:** Consistent casing — `ID`, `HTTP`, `URL` (not `Id`, `Http`)
- **Receivers:** Short, consistent — `(l *Library)`, `(s *Scanner)`

### Vue 3

- **Files:** `PascalCase.vue` — `PhotoGrid.vue`
- **Variables:** `camelCase` — `const currentPhoto = ref()`

> 📖 Full naming rules and examples: [`docs/go-readability.md`](../docs/go-readability.md)

---

## ⚙️ HTTP Server + Browser Shell

Kestrel is a single Go binary. When generating code:

- The backend is a stdlib `net/http` server bound to **`127.0.0.1`** only (never `0.0.0.0`), listening on an OS-picked free port.
- Frontend assets live in `frontend/dist/` and are embedded with `//go:embed frontend/dist/*`, served via `http.FS`. In `--dev` mode the server falls back to Vite.
- At startup the binary generates a per-run auth token, launches the user's default browser (OS-specific, in `internal/platform/`) at `http://127.0.0.1:PORT`, and requires that token on every `/api/*` and `/ws` request.
- Put HTTP handlers in `internal/api/` (thin: decode → call domain package → encode). Register them against the router owned by `internal/server/`.
- For server-pushed events (scan progress, thumbnail ready, library updates), broadcast a typed `Event{Kind, Payload}` through the WebSocket hub in `internal/server/`. Handlers and domain code never write to WS connections directly.
- Commands always go through REST. The WebSocket is for server-to-client events only.

> 📖 Full server, handler, and hub patterns: [`docs/system-design.md`](../docs/system-design.md)

---

## 🚫 Forbidden Patterns

1. **Global variables without mutexes.** Never use a global `var Photos []Photo` without a lock.
2. **Blocking the main thread.** Long-running Go tasks must run in goroutines and emit events to the UI.
3. **Complex frontend logic.** Do not sort or filter 20,000+ items in JavaScript. Do it in Go, send the result to Vue.
4. **Bare error returns.** Never `return err` without wrapping: use `fmt.Errorf("doing X: %w", err)`.
5. **One goroutine per file.** Use fixed worker pools sized to `runtime.NumCPU()`.
6. **`utils` or `helpers` packages.** Create focused, single-responsibility packages instead.

---

## 📚 Reference Documents

**Always consult these when generating or reviewing code:**

| Document                                              | Covers                                                                                                                            |
| ----------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| [`docs/system-design.md`](../docs/system-design.md)   | Architecture, data flow, package structure, concurrency patterns, persistence, HTTP + WebSocket server integration, performance targets |
| [`docs/ui-design.md`](../docs/ui-design.md)           | Vue 3 + Vite island hydration, REST/WebSocket transport, component hierarchy, state management, thumbnail strategy, performance rules   |
| [`docs/visual-design.md`](../docs/visual-design.md)   | Dark neo-skeuomorphic tactile visual system: design tokens, elevation recipe, per-component visual specs, accessibility rules         |
| [`docs/go-readability.md`](../docs/go-readability.md) | Function length limits, naming conventions, comment rules, error handling, interface design, testing standards, file organization |
