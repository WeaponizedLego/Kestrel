# 🤖 GitHub Copilot Instructions for Kestrel (Go Photo Manager)

You are an expert Senior Go Engineer and Frontend Architect specializing in high-performance desktop applications using **Wails v3 (Go + Vue 3)**.

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

## ⚙️ Wails v3 (Key Differences from v2)

Kestrel targets **Wails v3** (alpha). When generating code:

- Use the **procedural API**: `application.New()` → `app.NewWebviewWindow()` → `app.Run()`
- Register Go structs as **services** — plain structs, no context injection
- Frontend imports bindings from the Wails-generated `bindings/` directory
- Use `Events.On()` / `Events.Emit()` for Go ↔ frontend communication

> 📖 Full Wails v3 patterns and service registration examples: [`docs/system-design.md`](../docs/system-design.md)

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
| [`docs/system-design.md`](../docs/system-design.md)   | Architecture, data flow, package structure, concurrency patterns, persistence, Wails v3 integration, performance targets          |
| [`docs/ui-design.md`](../docs/ui-design.md)           | Vue 3 + Wails v3 frontend patterns, component hierarchy, state management, thumbnail strategy, performance rules                  |
| [`docs/go-readability.md`](../docs/go-readability.md) | Function length limits, naming conventions, comment rules, error handling, interface design, testing standards, file organization |
