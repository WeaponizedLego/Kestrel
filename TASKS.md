# 🗺️ Kestrel — Phased Roadmap

> This file is the source of truth for what ships in which phase. Each phase is a merge-ready chunk; phases are ordered so that later ones can build on earlier ones without rework. Design details live in `docs/system-design.md`, `docs/ui-design.md`, and `docs/go-readability.md` — this file is the execution order, not the spec.

---

## Phase 0 — Scaffold (MVP: server sees a folder)

**Goal:** the binary starts, opens the browser, scans a folder you point it at, and renders the file list.

- `go.mod`, `.gitignore`
- `cmd/kestrel/main.go` with `--dev` and `--addr` flags
- `internal/library/` — `Library` (`map[string]*Photo` + `sync.RWMutex`), `Photo{Path, Name, SizeBytes, ModTime}`
- `internal/scanner/` — synchronous `filepath.WalkDir` that filters supported image extensions
- `internal/server/` — loopback bind on `127.0.0.1:0`, per-run auth token, `tokenMiddleware`, static-asset serving with `<meta name="kestrel-token">` injection
- `internal/api/` — `POST /api/scan`, `GET /api/photos`
- `internal/assets/` — `//go:embed all:dist`
- `internal/platform/` — `OpenBrowser` (xdg-open / open / rundll32)
- `frontend/` — Vite multi-entry (5 island entries), `@vue/server-renderer` shell script, `transport/api.ts` + stub `transport/events.ts`, functional `PhotoGrid` island with folder-path input + scan button

---

## Phase 1 — Scanner worker pool

Replace the synchronous walk with a fixed-size worker pool (`runtime.NumCPU()`) per `docs/system-design.md` → Pattern B.

- Producer walks the tree and feeds `chan string`
- Workers process files in parallel and publish results
- `context.Context` propagation so scans can be cancelled
- No goroutine-per-file

---

## Phase 2 — Metadata extraction

- New `internal/metadata/` package
- Pure-Go EXIF lib, image dimensions, `TakenAt`, `CameraMake`
- Extend `Photo`: `Hash`, `Width`, `Height`, `TakenAt`, `CameraMake`
- Scanner hashes each file (stable key for `thumbs.pack` in Phase 5)

---

## Phase 3 — Persistence (`library_meta.gob`)

- New `internal/persistence/` package
- Versioned gob encode/decode with a migration header
- Load synchronously at startup, save on shutdown + periodic auto-save
- Missing file → fresh library (no error)

---

## Phase 4 — WebSocket event hub

- `internal/server/hub.go` — `Event{Kind, Payload}`, fan-out to subscribers
- WS upgrade at `/ws`, token via `?token=`, same-origin check
- Scanner publishes `scan:progress`; library mutations publish `library:updated`
- Frontend `transport/events.ts` becomes a real singleton with auto-reconnect

---

## Phase 5 — Thumbnail generation + `thumbs.pack`

- New `internal/thumbnail/` package
- `Generate(path) → 256×256 JPEG` using pure-Go decode + encode (stdlib + `golang.org/x/image` where needed)
- `thumbs.pack` file format per `docs/system-design.md` (magic `KTMB`, version, index, concatenated JPEG bytes)
- Index-only load at startup; pixel data loaded on demand
- Append on scan; compact on shutdown when gaps accumulate

---

## Phase 6 — `ThumbnailProvider`, LRU, pre-fetcher

- `ThumbnailProvider` interface (`Get`, `GetOrLoad`, `Prefetch`, `MemoryUsage`, `LoadIndex`, `SaveAll`)
- Priority-tier LRU: viewport-pinned → lookahead → current folder → child folders → background
- `POST /api/viewport`, `POST /api/navigate` endpoints feed the pre-fetcher queue
- Adaptive mode: Eager when estimated thumb memory < 0.5 × budget; otherwise Tiered
- `thumbnail:ready` WS broadcast after insert

---

## Phase 7 — Pre-sorted indices

- `Library` maintains `byDate`, `byName`, `bySize` slices, rebuilt in background on mutation
- REST endpoints accept `sort` + `order` params and return slices from the pre-built index
- No JS-side sorting or filtering — enforced by code review

---

## Phase 8 — Virtualized PhotoGrid + full-res viewer

- `vue-virtual-scroller` (or a tiny custom implementation) in `PhotoGrid.vue`
- Recycling DOM nodes, 60 fps scroll budget
- `PhotoViewer.vue` lazy-loaded via dynamic import, opens on Enter / click

---

## Phase 9 — UX polish

- **Dark mode is the default (and for MVP, the only) theme.** The shell, islands, and all future components render on a dark palette. Light mode is not on the roadmap; don't bake light-mode assumptions (hard-coded `#fff` backgrounds, dark-on-light CSS) into earlier phases.
- Keyboard nav: arrows in grid, Enter opens viewer, Esc closes
- Focus management between grid and viewer
- Skeleton loaders during async Go calls
- Friendly error states (not raw stack traces)
- Debounced search (200–300 ms)

---

## Phase 10 — Ops & distribution

- Single-instance lockfile in user config dir; re-open browser at the running instance if present
- Graceful shutdown: trap SIGINT/SIGTERM, flush persistence, close hub, wait for in-flight requests
- Structured logging (slog)
- Cross-compile CI matrix: `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, `windows/amd64`
- CGO-free build verification
- Release binaries with `-ldflags="-s -w"`

---

## Out of scope (for now)

- Multi-user / networked access (loopback only by design)
- Video thumbnails
- Cloud sync
- Plugin architecture

If a feature shows up in discussion that fits one of the deferred phases, amend the phase description here rather than growing `docs/system-design.md` with roadmap-style bullet points.
