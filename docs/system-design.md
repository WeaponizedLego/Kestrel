# 🏗️ System Design — Kestrel

> This document describes the core architecture, data flow, and implementation patterns for Kestrel.
> It is referenced by `.github/copilot-instructions.md` and should be consulted when designing or reviewing backend code.

---

## Architecture Overview: "The Video Game Architecture"

Kestrel is modelled after a game engine, not a traditional CRUD application.
The guiding principle is **interaction speed after startup**: smooth scrolling, sorting, and browsing without waiting on disk I/O.

| Layer                 | Responsibility                                                                                                                                             |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **In-Memory Store**   | Single source of truth for metadata at runtime — a Go `map[string]*Photo` guarded by `sync.RWMutex`.                                                       |
| **Thumbnail Cache**   | Memory-budgeted LRU cache backed by a packed binary file on disk (`thumbs.pack`). Decoupled from the metadata store via the `ThumbnailProvider` interface. |
| **Scanner / Workers** | Background goroutine pool that discovers files, extracts metadata, and generates thumbnails.                                                               |
| **Persistence**       | Two-file split: `library_meta.gob` (metadata, loaded synchronously at startup) + `thumbs.pack` (thumbnails, loaded progressively).                         |
| **Pre-fetcher**       | Background goroutine pool that predicts which thumbnails the user will need next and loads them from disk into the LRU cache.                              |
| **Wails v3 Services** | Go structs registered as services — the bridge between Go and the Vue 3 frontend.                                                                          |
| **Frontend**          | Vue 3 (Composition API) consuming Wails-generated TypeScript bindings.                                                                                     |

---

## Data Flow

```
Startup
  └─▶ Load library_meta.gob → populate map[string]*Photo (< 5 sec for 1M photos)
        └─▶ Load thumbs.pack index (offsets only, not pixel data) (< 1 sec for 1M)
              └─▶ UI renders immediately with placeholder thumbnails
                    └─▶ Frontend sends initial SetViewport(startIndex, endIndex)
                          └─▶ Pre-fetcher loads visible thumbnails from disk (~50–200 ms)
                                └─▶ Pre-fetcher continues with lookahead tiers in background

User browses / scrolls / sorts
  └─▶ Metadata reads go to the in-memory map (RLock) — always instant
  └─▶ Thumbnail reads go to the LRU cache
        └─▶ Cache hit → serve from RAM (zero disk I/O)
        └─▶ Cache miss → load from thumbs.pack on demand (< 1 ms from SSD)
  └─▶ Pre-fetcher continuously loads ahead based on scroll direction & folder context

User opens full-resolution image
  └─▶ Read original file from HDD / NAS on demand

Background scan (async)
  └─▶ Worker pool walks directory tree
        └─▶ Each worker: hash file → extract EXIF → generate 256×256 JPEG thumbnail
              └─▶ Write metadata to in-memory map (Lock)
              └─▶ Append thumbnail to thumbs.pack + update index
                    └─▶ Emit progress events to frontend via Wails

Shutdown / Manual sync
  └─▶ Serialize metadata map to library_meta.gob
  └─▶ Flush any pending thumbnail writes to thumbs.pack
```

---

## Proposed Package Structure

```
kestrel/
├── cmd/                    # Application entry point
│   └── kestrel/
│       └── main.go         # Wails v3 app creation & service registration
├── internal/
│   ├── library/            # In-memory photo store (map + RWMutex) + Photo struct
│   ├── scanner/            # Directory walking & worker pool
│   ├── thumbnail/          # ThumbnailProvider interface, LRU cache, pack file, pre-fetcher
│   ├── metadata/           # EXIF / file metadata extraction
│   ├── persistence/        # .gob serialization & deserialization (metadata only)
│   └── platform/           # OS-specific helpers (paths, memory detection, file watchers)
├── services/               # Wails v3 service structs (exported to frontend)
│   ├── library_service.go
│   ├── scanner_service.go
│   └── viewer_service.go
├── frontend/               # Vue 3 + Vite project
│   ├── src/
│   └── ...
├── docs/                   # This folder
└── go.mod
```

**Rules:**

- `internal/` packages are never imported outside the module — enforce encapsulation.
- `services/` contains only thin Wails-facing wrappers; business logic lives in `internal/`.
- One package = one clear responsibility. Avoid `utils/` or `helpers/` catch-all packages.

---

## Concurrency Model

### Pattern A: The Thread-Safe In-Memory Store

**Context:** Storing 20,000+ photos in RAM without race conditions.
**Rule:** Never expose the map directly. Access exclusively through methods that acquire the appropriate lock.

```go
type Library struct {
    mu     sync.RWMutex
    photos map[string]*Photo // Key = absolute file path
}

// Write — exclusive lock
func (l *Library) AddPhoto(p *Photo) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.photos[p.Path] = p
}

// Read — shared lock (multiple concurrent readers allowed)
func (l *Library) GetPhoto(path string) (*Photo, bool) {
    l.mu.RLock()
    defer l.mu.RUnlock()
    p, exists := l.photos[path]
    return p, exists
}

// Snapshot — return a copy for the frontend to avoid holding the lock
func (l *Library) AllPhotos() []*Photo {
    l.mu.RLock()
    defer l.mu.RUnlock()
    result := make([]*Photo, 0, len(l.photos))
    for _, p := range l.photos {
        result = append(result, p)
    }
    return result
}
```

### Pattern B: The Worker Pool (Safe Scanning)

**Context:** Scanning 100,000 files without exhausting file descriptors or memory.
**Rule:** Use a fixed-size goroutine pool (`runtime.NumCPU()`), not one goroutine per file.

```go
func ScanDirectory(root string, lib *Library) error {
    files := make(chan string, 100)
    var wg sync.WaitGroup

    // Fixed pool of workers
    for range runtime.NumCPU() {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for path := range files {
                photo, err := processFile(path)
                if err != nil {
                    log.Printf("skipping %s: %v", path, err)
                    continue
                }
                lib.AddPhoto(photo)
            }
        }()
    }

    // Producer: walk directory and feed paths into the channel
    err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return fmt.Errorf("walking %s: %w", path, err)
        }
        if !d.IsDir() && isSupportedImage(path) {
            files <- path
        }
        return nil
    })
    close(files)
    wg.Wait()
    return err
}
```

---

## Error Handling Strategy

**Rule:** Never return a bare `err`. Always wrap with context describing _what you were trying to do_.

```go
// ❌ BAD — caller has no idea what failed
if err != nil {
    return err
}

// ✅ GOOD — caller sees: "failed to open thumbnail for /photos/img.jpg: file not found"
if err != nil {
    return fmt.Errorf("failed to open thumbnail for %s: %w", path, err)
}
```

Use `%w` (not `%v`) so callers can unwrap with `errors.Is` / `errors.As` when needed.

---

## Persistence Strategy

Persistence is split into two files to enable progressive startup. Metadata loads synchronously (fast, small), and the UI becomes interactive before any thumbnail data is read from disk.

### File 1: `library_meta.gob` — Metadata

| Aspect            | Detail                                                                                                    |
| ----------------- | --------------------------------------------------------------------------------------------------------- |
| **Format**        | `encoding/gob` — Go-native binary serialization.                                                          |
| **Contents**      | The full `map[string]*Photo` — paths, hashes, EXIF data, dimensions, dates, tags. **No thumbnail bytes.** |
| **Size estimate** | ~200 bytes/photo → ~200 MB at 1M photos.                                                                  |
| **Load**          | Startup reads into the in-memory map synchronously. Missing file = fresh library.                         |
| **Save triggers** | Application exit, manual sync button, periodic auto-save (configurable interval).                         |
| **Migration**     | If the `Photo` struct changes, add a version header to the .gob file and handle migration on load.        |

### File 2: `thumbs.pack` — Thumbnails

| Aspect            | Detail                                                                                                                                                      |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Format**        | Custom packed binary: header index + concatenated raw JPEG bytes.                                                                                           |
| **Contents**      | Every generated 256×256 JPEG thumbnail, keyed by photo path hash.                                                                                           |
| **Size estimate** | ~15–30 KB/thumbnail → ~15–30 GB at 1M photos.                                                                                                               |
| **Load**          | Only the **index** is loaded at startup (maps photo path → byte offset + length). Thumbnail pixel data is loaded on demand by the pre-fetcher or LRU cache. |
| **Writes**        | New thumbnails are appended during scanning. On shutdown, optionally compact (rewrite without gaps from deleted entries).                                   |

#### `thumbs.pack` File Format

```
[4 bytes: magic "KTMB"]
[4 bytes: version uint32]
[4 bytes: entry count uint32]
[index entries: repeated {
    path_hash  [32]byte   // SHA-256 of the photo's absolute path
    offset     uint64     // byte offset into the data section
    size       uint32     // thumbnail JPEG byte length
}]
[thumbnail data: concatenated raw JPEG bytes]
```

The index is small enough to reside fully in RAM: 1M entries × ~44 bytes ≈ 44 MB.

---

## Wails v3 Integration

Kestrel targets **Wails v3** (currently in alpha). Key differences from v2:

| v2                            | v3                                                                       |
| ----------------------------- | ------------------------------------------------------------------------ |
| Single `wails.Run()` call     | Procedural: `application.New()` → `app.NewWebviewWindow()` → `app.Run()` |
| Context injected into structs | No context injection — plain Go structs as services                      |
| Single window only            | Multi-window support                                                     |
| Opaque build system           | Transparent, customizable build pipeline                                 |

### Service Registration Pattern (v3)

```go
// A plain Go struct — no Wails-specific fields needed
type LibraryService struct {
    lib *library.Library
}

func NewLibraryService(lib *library.Library) *LibraryService {
    return &LibraryService{lib: lib}
}

// Exported methods become frontend-callable bindings
func (s *LibraryService) GetPhotos() []*library.Photo {
    return s.lib.AllPhotos()
}

// Registration in main.go
app := application.New(application.Options{
    Name: "Kestrel",
    Services: []application.Service{
        application.NewService(NewLibraryService(lib)),
    },
})
```

---

## Performance Targets

### By Library Size

| Scale           | Metadata load | UI interactive | All visible thumbs   | Memory (metadata) | Memory (thumb cache) |
| --------------- | ------------- | -------------- | -------------------- | ----------------- | -------------------- |
| **20K photos**  | < 1 sec       | < 1 sec        | < 1 sec (eager mode) | ~4 MB             | ~400 MB (all in RAM) |
| **100K photos** | < 2 sec       | < 2 sec        | < 1 sec              | ~20 MB            | ~2 GB (all in RAM)   |
| **500K photos** | < 5 sec       | < 5 sec        | < 200 ms             | ~100 MB           | Budgeted LRU         |
| **1M photos**   | < 10 sec      | < 10 sec       | < 200 ms             | ~200 MB           | Budgeted LRU         |

### Interaction Targets

| Metric                    | Target                   | Rationale                                                               |
| ------------------------- | ------------------------ | ----------------------------------------------------------------------- |
| **Scroll / sort latency** | < 16 ms (60 fps)         | Metadata reads from in-memory map; thumbnail reads from LRU cache.      |
| **Thumbnail cache hit**   | > 95% during scroll      | Pre-fetcher loads ahead of viewport; hits serve from RAM with zero I/O. |
| **Thumbnail cache miss**  | < 5 ms                   | Single seek + read from `thumbs.pack` on SSD.                           |
| **Full-image open**       | Network/disk dependent   | Acceptable — only triggered on explicit user action.                    |
| **Scan throughput**       | CPU-bound, not I/O-bound | Worker pool sized to `runtime.NumCPU()`.                                |

---

## Memory Management

### Design Principle: Never Crash, Gracefully Degrade

Kestrel must handle libraries from 1K to 1M+ photos on machines with varying amounts of RAM. Metadata is always fully resident in RAM (it's small). Thumbnail memory is **budgeted** — the system keeps as many thumbnails in RAM as the budget allows and loads the rest from disk on demand.

### Memory Budget Detection

At startup, Kestrel detects available system memory and sets a thumbnail cache budget:

```
thumbnailBudget = min(totalSystemRAM * 0.25, 4 GB)
```

- **Floor:** 512 MB (minimum usable cache)
- **Ceiling:** 4 GB (default; user-configurable upward)
- **Override:** User can set an explicit budget in the config file or via CLI flag

The budget covers **only** thumbnail pixel data in the LRU cache. Metadata, the pack file index, and application overhead are tracked separately and are not subject to eviction.

### Adaptive Mode Selection

After loading `library_meta.gob`, the system estimates total thumbnail memory:

```
estimatedThumbMemory = photoCount × avgThumbnailSize (default: 20 KB)
```

| Condition                             | Mode       | Behaviour                                                                                                          |
| ------------------------------------- | ---------- | ------------------------------------------------------------------------------------------------------------------ |
| `estimatedThumbMemory < budget × 0.5` | **Eager**  | Load all thumbnails from `thumbs.pack` into RAM at startup. No eviction. This is the full "video game" experience. |
| `estimatedThumbMemory ≥ budget × 0.5` | **Tiered** | Use the LRU cache + pre-fetcher. Only a window of thumbnails lives in RAM at any time.                             |

The `ThumbnailProvider` interface means the rest of the application does not know which mode is active. Service and frontend code always calls `Get(path)` and receives bytes.

---

## Thumbnail Cache Architecture

### `ThumbnailProvider` Interface

All thumbnail access goes through this interface. It abstracts whether thumbnails come from an in-memory eager cache or a disk-backed LRU cache.

```go
type ThumbnailProvider interface {
    // Get returns thumbnail bytes if currently in the RAM cache.
    Get(photoPath string) ([]byte, bool)

    // GetOrLoad returns from cache or loads from disk synchronously.
    GetOrLoad(photoPath string) ([]byte, error)

    // Prefetch hints that these thumbnails should be loaded into cache in the background.
    Prefetch(paths []string)

    // MemoryUsage returns the current RAM bytes consumed by cached thumbnails.
    MemoryUsage() int64

    // LoadIndex reads the thumbs.pack index into memory (called once at startup).
    LoadIndex() error

    // SaveAll persists any pending writes (called at shutdown).
    SaveAll() error
}
```

### LRU Cache with Priority-Aware Eviction

The thumbnail cache is not a simple LRU. Each cached entry carries a **priority tier** so that the pre-fetcher cannot evict thumbnails the user is actively viewing.

**Priority tiers (highest → lowest):**

| Tier                 | Description                                          | Evictable?                               |
| -------------------- | ---------------------------------------------------- | ---------------------------------------- |
| 1 — Viewport         | Thumbnails currently visible on screen               | **Pinned** — never evicted while visible |
| 2 — Scroll lookahead | Next 2–3 pages in the current scroll direction       | Yes, but last to go                      |
| 3 — Current folder   | All thumbnails in the active folder                  | Yes                                      |
| 4 — Child folders    | Thumbnails from subfolders (anticipating drill-down) | Yes                                      |
| 5 — Background       | Everything else (pre-fetched speculatively)          | First to be evicted                      |

**Eviction order:** When the cache exceeds the memory budget, evict the lowest tier first, then the oldest entry within that tier. Tier 1 entries are never evicted while the viewport references them.

### Predictive Pre-Fetcher

A background goroutine pool (2–4 workers) reads thumbnails from `thumbs.pack` and inserts them into the LRU cache ahead of the user's navigation.

**Inputs (from the frontend via Wails events):**

- `SetViewport(startIndex, endIndex)` — which thumbnails are currently visible
- `ScrollDirection` — inferred from sequential `SetViewport` calls
- `NavigateToFolder(path)` — user opened a folder in the sidebar

**Behaviour:**

1. On every viewport change, enqueue: visible thumbnails (tier 1) → scroll lookahead (tier 2) → current folder (tier 3) → child folders (tier 4).
2. When navigation changes (new folder), flush the work queue and reprioritize.
3. Workers read from `thumbs.pack` using the in-memory index (seek + read, single syscall per thumbnail).
4. Loaded thumbnails are inserted into the LRU cache, triggering eviction of lower-priority entries if at capacity.
5. After insertion, emit a Wails event (`thumbnail:ready`) so the frontend can swap placeholders for real images.

### Pre-Sorted Indices

To avoid re-sorting 1M items on every sort request, the `Library` maintains pre-computed sort indices:

```go
type Library struct {
    mu     sync.RWMutex
    photos map[string]*Photo

    // Pre-computed sort indices — rebuilt on mutation (scan adds/removes)
    byDate []*Photo
    byName []*Photo
    bySize []*Photo
}
```

When a scan adds new photos, the sort indices are rebuilt once in the background. When the frontend requests "photos sorted by date," the library returns a slice from the pre-built index — no sort computation at request time.
