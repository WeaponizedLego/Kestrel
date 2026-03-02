# 🏗️ System Design — Kestrel

> This document describes the core architecture, data flow, and implementation patterns for Kestrel.
> It is referenced by `.github/copilot-instructions.md` and should be consulted when designing or reviewing backend code.

---

## Architecture Overview: "The Video Game Architecture"

Kestrel is modelled after a game engine, not a traditional CRUD application.
The guiding principle is **interaction speed after startup**: smooth scrolling, sorting, and browsing without waiting on disk I/O.

| Layer | Responsibility |
|---|---|
| **In-Memory Store** | Single source of truth at runtime — a Go `map[string]*Photo` guarded by `sync.RWMutex`. |
| **Scanner / Workers** | Background goroutine pool that discovers files, extracts metadata, and generates thumbnails. |
| **Persistence** | Compressed binary (`library.gob`) loaded at startup, saved on exit or manual sync. |
| **Wails v3 Services** | Go structs registered as services — the bridge between Go and the Vue 3 frontend. |
| **Frontend** | Vue 3 (Composition API) consuming Wails-generated TypeScript bindings. |

---

## Data Flow

```
Startup
  └─▶ Load library.gob into memory
        └─▶ Populate map[string]*Photo
              └─▶ UI renders from in-memory data (zero disk reads)

User browses / scrolls / sorts
  └─▶ All reads go to the in-memory map (RLock)
        └─▶ No disk I/O in the interaction loop

User opens full-resolution image
  └─▶ Read original file from HDD / NAS on demand

Background scan (async)
  └─▶ Worker pool walks directory tree
        └─▶ Each worker: hash file → extract EXIF → generate thumbnail
              └─▶ Write results to in-memory map (Lock)
                    └─▶ Emit progress events to frontend via Wails

Shutdown / Manual sync
  └─▶ Serialize map to library.gob
```

---

## Proposed Package Structure

```
kestrel/
├── cmd/                    # Application entry point
│   └── kestrel/
│       └── main.go         # Wails v3 app creation & service registration
├── internal/
│   ├── library/            # In-memory photo store (map + RWMutex)
│   ├── scanner/            # Directory walking & worker pool
│   ├── thumbnail/          # Thumbnail generation & caching
│   ├── metadata/           # EXIF / file metadata extraction
│   ├── persistence/        # .gob serialization & deserialization
│   └── platform/           # OS-specific helpers (paths, file watchers)
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

**Rule:** Never return a bare `err`. Always wrap with context describing *what you were trying to do*.

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

| Aspect | Detail |
|---|---|
| **Format** | `encoding/gob` — Go-native binary serialization. |
| **File** | `library.gob` (location configurable, defaults to app data dir). |
| **Save triggers** | Application exit, manual sync button, periodic auto-save (configurable interval). |
| **Load** | Startup reads `library.gob` into the in-memory map. Missing file = fresh library. |
| **Migration** | If the `Photo` struct changes, add a version header to the .gob file and handle migration on load. |

---

## Wails v3 Integration

Kestrel targets **Wails v3** (currently in alpha). Key differences from v2:

| v2 | v3 |
|---|---|
| Single `wails.Run()` call | Procedural: `application.New()` → `app.NewWebviewWindow()` → `app.Run()` |
| Context injected into structs | No context injection — plain Go structs as services |
| Single window only | Multi-window support |
| Opaque build system | Transparent, customizable build pipeline |

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

| Metric | Target | Rationale |
|---|---|---|
| **Startup time** | < 30 seconds | Front-load .gob deserialization so interaction is instant. |
| **Memory footprint** | ~2–4 GB | Metadata + thumbnails for 20,000+ images resident in RAM. |
| **Scroll / sort latency** | < 16 ms (60 fps) | All reads from in-memory map; no disk I/O in the render loop. |
| **Full-image open** | Network/disk dependent | Acceptable — only triggered on explicit user action. |
| **Scan throughput** | CPU-bound, not I/O-bound | Worker pool sized to `runtime.NumCPU()`. |
