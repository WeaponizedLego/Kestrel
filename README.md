# 📸 Kestrel

High-performance desktop photo manager for very large local libraries (20,000 – 1,000,000+ images), including collections stored on slow HDDs or network drives.

## Why Kestrel

- Built for libraries that are too big for most photo managers to stay responsive on.
- Works well when originals live on slow storage (HDD / NAS) — browsing is driven by cached thumbnails and in-memory metadata.
- Ships as a **single cross-platform binary**. Pure Go, CGO-free, no bundled Chromium, no runtime dependencies.
- Runs in your **default browser** via a local loopback server — not a webview, not an Electron shell.
- Dark-mode native UI built on Vue 3 + Tailwind / daisyUI.

## Install

Kestrel ships as a single self-contained download per platform — no Go or Node toolchain required to run it. Tagged releases (`vX.Y.Z`) are published on the [Releases page](../../releases) with all platform artifacts attached; bleeding-edge builds from every green CI run are also available as artifacts on the [Build Matrix workflow](.github/workflows/build-matrix.yml).

### macOS (Apple Silicon)

1. Download `kestrel-macos-arm64-app.zip` and unzip it — you'll get `Kestrel.app`.
2. Drag `Kestrel.app` into `/Applications`.
3. First launch: **right-click → Open** (not double-click). macOS will warn that the app is from an unidentified developer; click **Open** anyway. Subsequent launches double-click normally.
   - Alternative: `xattr -dr com.apple.quarantine /Applications/Kestrel.app`.

Builds are unsigned today — proper notarization is a tracked follow-up.

### Linux (x86_64)

1. Download `Kestrel-x86_64.AppImage` from the workflow artifacts.
2. `chmod +x Kestrel-x86_64.AppImage`
3. Double-click in your file manager, or run `./Kestrel-x86_64.AppImage` from a terminal.

The AppImage requires FUSE 2 on the host (preinstalled on most desktop distros).

### Windows (x86_64)

1. Download `Kestrel-windows-amd64.zip` and extract it — you'll get `kestrel-windows-amd64.exe`.
2. Double-click the `.exe`, or run it from a terminal.

Windows SmartScreen may warn that the publisher is unknown — click **More info → Run anyway**. A console window opens alongside the browser today; hiding it (`-H=windowsgui`) is a tracked follow-up because it also suppresses stdout logs.

## Usage

1. **Launch Kestrel.** It starts a local server on `127.0.0.1:<random-port>`, generates a per-run auth token, and opens your default browser at the loopback URL.
2. **Point it at a folder.** Use the scan control in the UI to choose the root of your photo library. Kestrel walks the tree in parallel (one worker per CPU core), extracts EXIF / metadata, computes a perceptual hash per photo, and generates thumbnails.
3. **Wait for the first scan.** For a 50k-photo library on a slow drive this can take a while — progress streams live into the UI over WebSocket. After the initial scan, startup is fast because metadata and the thumbnail index are loaded from a local cache.
4. **Browse.** Scrolling, sorting, and filtering run against in-memory data, so the grid stays fluid regardless of where the original files live. Opening a full-resolution image may briefly hit the underlying storage.
5. **Tag.** Auto-tags (camera, lens, year, place, kind) show up on every photo for free. The **Tagging Queue** groups visually similar photos into clusters so you can tag whole groups in one click.
6. **Quit.** Closing every Kestrel browser tab shuts the app down automatically within ~10 seconds.

Keyboard basics: arrow keys navigate the grid, **Enter** opens the full-resolution viewer, **Esc** closes it.

## Features

### In-memory truth

All library metadata is loaded into RAM at startup and held in a `map[string]*Photo` guarded by `sync.RWMutex`. Scroll / sort / filter / search never touch disk. Trades startup time and memory (target: 2–4 GB for large libraries) for consistently low-latency interaction.

### Zero-latency interaction

Because the interaction loop never waits on I/O, unpredictable disk or network-drive latency can't cause UI stutter. No per-item database queries during browsing.

### Slow-drive strategy

Originals stay on their original drive. Thumbnails are generated once and persisted to a compact local cache (`thumbs.pack`) on fast storage, fronted by a memory-budgeted priority LRU with a pre-fetcher that anticipates scroll direction. Full-resolution reads happen only when you open a photo.

### Persistence

Two files make up the on-disk state:

- `library_meta.gob` — small, versioned gob-encoded metadata, loaded synchronously at startup.
- `thumbs.pack` — packed JPEG thumbnails with an index loaded up front and pixel data fetched on demand.

Missing state files simply mean a fresh library; nothing else to configure.

### Assisted tagging

- **Auto-tags from metadata.** Every photo gets baseline tags (camera, lens, year, ISO, orientation, media kind) at scan time. GPS-tagged photos are reverse-geocoded to city + country using an **embedded offline GeoNames dataset** — no network calls.
- **Perceptual-hash clustering.** A 64-bit pHash per photo groups near-duplicates and visually similar shots.
- **Tagging Queue UI.** Clusters are ordered by size so you can tag the biggest groups first — one click tags N photos at once.

All pure Go, all embedded, all cross-platform. See [`docs/assisted-tagging.md`](docs/assisted-tagging.md) for the full design.

## How it works

Kestrel uses a **"video game" architecture**: everything you interact with is already in RAM. The Go backend boots, loads the metadata map and thumbnail index, starts a `net/http` server on `127.0.0.1:0`, generates a session token, and opens the browser. The frontend talks to the backend over REST (commands) and a single one-way WebSocket (events like `scan:progress`, `thumbnail:ready`, `library:updated`).

The Vue 3 frontend uses **manual island hydration**: a build-time-rendered static shell with a separate Vue app mounted per interactive region (Sidebar, Toolbar, PhotoGrid, StatusBar, TaggingQueue). No single-root SPA; no JS-side sorting or filtering over the photo list — all sort / filter / search goes through pre-built Go indices.

For the full picture see [`docs/system-design.md`](docs/system-design.md) and [`docs/ui-design.md`](docs/ui-design.md).

## Build from source

### Prerequisites

- Go 1.22+
- Node.js 22+ and pnpm (via Corepack: `corepack enable`)

### Development

Dev runs two processes — Vite serves the frontend with HMR and proxies `/api` and `/ws` to the Go server:

```bash
# Terminal 1 — frontend with HMR
cd frontend && pnpm install && pnpm dev

# Terminal 2 — Go backend
go run ./cmd/kestrel --dev
```

`--dev` skips the browser launch (point your own at the Vite URL) and disables asset embedding, so you don't need a built `frontend/dist`.

### Production binary

```bash
cd frontend && pnpm build        # emits frontend/dist/
cd ..
go build -ldflags="-s -w" -o kestrel ./cmd/kestrel
```

The resulting `kestrel` binary embeds the built frontend, opens the default browser on launch, and has no external dependencies.

CI cross-compiles for `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, and `windows/amd64`. The build stays CGO-free so cross-compilation works from any host.

## Tech stack

- **Frontend:** Vue 3 (Composition API) + Vite, manual island hydration, Tailwind v4 + daisyUI.
- **Backend:** Go — scanner worker pool, in-memory library, thumbnail pipeline, persistence.
- **Transport:** REST/JSON for commands, a single WebSocket (`/ws`) for server-pushed events.
- **Embedding:** `//go:embed frontend/dist/*` — the built frontend ships inside the Go binary.
- **Concurrency:** `sync.RWMutex` around the photo map, worker pools sized to `runtime.NumCPU()`.
- **Distribution:** Pure Go, CGO-free, single static binary per target.

## Documentation

Detailed design documents live in [`docs/`](docs/):

- [System Design](docs/system-design.md) — architecture, data flow, packages, concurrency, persistence.
- [UI Design](docs/ui-design.md) — islands, component hierarchy, REST / WS transport.
- [Assisted Tagging](docs/assisted-tagging.md) — auto-tags, pHash clustering, Tagging Queue UX.
- [Visual Design](docs/visual-design.md) — visual language and component styling.
- [Go Readability](docs/go-readability.md) — code style, naming, testing conventions.

## Roadmap

The main post-MVP item is **on-device semantic tagging** (CLIP-style embeddings) — searching by content, e.g. "beach at sunset", without ever tagging those photos yourself. It's deliberately not in the MVP because every practical ML inference path in Go today either requires CGO-linked bindings or a separately shipped runtime, both of which would break Kestrel's single-binary / pure-Go guarantee. Until a clean path exists, Kestrel relies on [assisted tagging](#assisted-tagging) to close the "new library, no tags" gap.

Out of scope: multi-user / networked access (loopback only by design), cloud sync, plugin architecture.

## Contributing

The repo uses strict Gitflow:

- Direct pushes to `develop` and `main` are blocked — PRs only.
- Required checks: **Gitflow Guard** and **Develop PR Go Reviewer**.
- Go style follows [`docs/go-readability.md`](docs/go-readability.md); broader contributor guidance lives in [`CLAUDE.md`](CLAUDE.md).
