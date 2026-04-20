# 📸 Kestrel

Kestrel is a high-performance desktop photo manager built for very large libraries (20,000+ images), including collections stored on slow HDDs or network drives.

## Table of Contents

- [Philosophy: Video Game Architecture](#philosophy-video-game-architecture)
- [Core Features](#core-features)
  - [1) In-Memory Truth](#1-in-memory-truth)
  - [2) Zero-Latency Interaction](#2-zero-latency-interaction)
  - [3) Slow Drive Strategy](#3-slow-drive-strategy)
  - [4) Persistence with `library.gob`](#4-persistence-with-librarygob)
  - [5) Assisted Tagging](#5-assisted-tagging)
- [Tech Stack](#tech-stack)
- [Runtime Flow](#runtime-flow)
- [Getting Started (Dev)](#getting-started-dev)
- [Roadmap / Future Work](#roadmap--future-work)
- [Gitflow CI Enforcement](#gitflow-ci-enforcement)
- [Coding Standards](#coding-standards)
- [Troubleshooting / FAQ](#troubleshooting--faq)

## Philosophy: Video Game Architecture

Kestrel is designed like a game engine, not a traditional CRUD app.  
The main goal is interaction speed after startup: smooth scrolling, sorting, and browsing without waiting on disk I/O.

## Core Features

### 1) In-Memory Truth

**What it does**
- Loads the active library state (metadata + thumbnails) into RAM.
- Uses an in-memory map as the runtime source of truth.

**Why it exists**
- Eliminates repeated storage lookups during normal UI interaction.
- Trades startup and memory cost for fast, consistent responsiveness.

**How it behaves in real usage**
- Startup work is front-loaded.
- Once loaded, common operations read from memory instead of querying disk.
- Target profile remains high-memory, low-latency (`~2GB - 4GB`, startup target `< 30s`).

### 2) Zero-Latency Interaction

**What it does**
- Keeps scroll/sort/search interaction on the in-memory dataset.

**Why it exists**
- Disk and network drive latency are unpredictable and can cause UI stutter.

**How it behaves in real usage**
- Scrolling and navigation remain fluid because disk reads are not part of the interaction loop.
- During browsing, no per-item database or disk queries are required.

### 3) Slow Drive Strategy

**What it does**
- Separates raw-photo storage from browsing-performance storage.

**Why it exists**
- Large libraries often live on slow storage (HDD/NAS), but browsing still needs to feel instant.

**How it behaves in real usage**
- Raw photos stay on HDD/NAS.
- Thumbnails are generated once, cached on a local SSD, and loaded into memory.
- You browse quickly even when originals live on slow drives; full-resolution file access happens on open/view.

### 4) Persistence with `library.gob`

**What it does**
- Saves application state to a compressed binary file (`library.gob`).

**Why it exists**
- Preserves computed/cached state between sessions.

**How it behaves in real usage**
- On startup, Kestrel restores state from persisted data.
- On exit or manual sync, current state is written back.

### 5) Assisted Tagging

**What it does**
- Auto-derives tags at scan time from EXIF (camera, lens, year, ISO, orientation), media kind, and — for GPS-tagged photos — an **offline** reverse-geocode to city + country via an embedded GeoNames dataset.
- Computes a 64-bit perceptual hash (pHash) per photo and groups near-duplicates and visually-similar photos into clusters.
- Provides a dedicated **Tagging Queue** UI that surfaces the largest untagged clusters first and lets the user tag a whole cluster in one click.

**Why it exists**
- A fresh library starts with zero tags. Without assistance, tagging tens of thousands of photos is daunting enough that users give up before they start.
- Auto-tags give every photo a useful baseline for free. Cluster-first tagging turns one click into N tags, so the user reaches "fully tagged" in a feasible number of sessions.

**How it behaves in real usage**
- Auto-tags appear on every photo automatically after the scan, rendered distinctly from user tags so you can tell inferred from confirmed.
- Opening the Tagging Queue shows clusters ordered by size; tagging the biggest groups first covers the most photos per click.
- All of this stays **pure-Go and CGO-free** — the GeoNames dataset, EXIF parser, and pHash library are all embedded or pure-Go deps, so Kestrel still ships as a single cross-platform binary.

> 📖 Full design in [`docs/assisted-tagging.md`](docs/assisted-tagging.md).

## Tech Stack

- **Frontend:** Vue 3 (Composition API) + Vite, using **manual island hydration** (each interactive region mounts as its own Vue app on a mostly-static HTML shell)
- **Backend:** Go (Golang) for scanning, hashing, thumbnail workflow, and memory management
- **UI Shell:** Go `net/http` server bound to `127.0.0.1`, frontend assets embedded via `//go:embed`. On launch, the binary opens the user's **default browser** at the chosen port — no webview, no bundled Chromium.
- **Transport:** REST/JSON for request-response, a single WebSocket endpoint for server-pushed events (scan progress, thumbnail-ready notifications)
- **Distribution:** A **single cross-platform executable** per target (Linux/macOS/Windows, amd64/arm64). Pure Go, CGO-free, produced by `go build`.
- **Concurrency Model:** Go maps protected by `sync.RWMutex`

> **Why a localhost server + browser instead of a webview?** It keeps the toolchain trivial
> (just `go build` and `vite build`), the output is one static binary with no platform SDKs or
> Chromium payloads, and it gives us full flexibility over the UI stack without living under a
> desktop-bridge framework's constraints.

## Runtime Flow

1. Launch app and load persisted state into memory.
2. Interact with the library from the in-memory map for low-latency browsing.
3. Access original files from HDD/NAS only when opening full-resolution images.
4. Persist updated state on exit or manual sync to `library.gob`.

## Getting Started (Dev)

### Prerequisites

- Go toolchain (1.22+)
- Node.js (22+) and npm

### Run in development mode

Dev runs two processes: Vite serves the frontend with HMR, and the Go binary serves the API + WebSocket. The frontend dev server proxies `/api` and `/ws` to the Go server.

```bash
# Terminal 1 — frontend with HMR
cd frontend && npm install && npm run dev

# Terminal 2 — Go backend
go run ./cmd/kestrel --dev
```

In `--dev` mode the Go binary skips opening the browser (you point your own at the Vite URL) and disables asset embedding so it doesn't need a built `frontend/dist`.

### Build the production binary

```bash
cd frontend && npm run build   # emits frontend/dist/
cd ..
go build -ldflags="-s -w" -o kestrel ./cmd/kestrel
```

The resulting `kestrel` binary embeds the built frontend, launches the default browser, and needs no external dependencies.

## Roadmap / Future Work

### Semantic content tagging (CLIP-style embeddings) — *post-MVP*

Once the MVP is stable, the plan is to add **on-device semantic tagging**: image embeddings
(CLIP-style) that let you search your library by content — `"beach at sunset"`,
`"dog in grass"`, `"receipt"` — without ever having tagged those photos yourself.

This is intentionally **not** part of the MVP. Every practical ML inference runtime in Go
today either requires CGO-linked ONNX/GGML bindings or a separately shipped runtime binary.
Both would break Kestrel's current **pure-Go, single cross-platform binary** guarantee,
which is a core UX promise of the project.

When we revisit this, we'll be making a conscious decision between:

- **Accept CGO** for a native ONNX runtime (loses clean cross-compilation).
- **Ship embeddings as an optional companion binary** that Kestrel talks to over the
  existing HTTP transport, so the core stays pure Go.
- **Wait** for a production-grade pure-Go inference path to mature.

Until then, Kestrel relies on its three-layer [assisted tagging](#5-assisted-tagging) to
close the "new library with no tags" gap.

## Documentation

Detailed design documents live in the [`docs/`](docs/) folder:

- **[System Design](docs/system-design.md)** — Architecture, data flow, package structure, concurrency patterns, persistence strategy
- **[UI Design](docs/ui-design.md)** — Frontend architecture, component hierarchy, island hydration model, REST/WebSocket transport
- **[Go Readability](docs/go-readability.md)** — Code style, naming, comments, testing, and readability standards
- **[Assisted Tagging](docs/assisted-tagging.md)** — Auto-derived tags, pHash clustering, and the Tagging Queue UX for fresh libraries

## Gitflow CI Enforcement

This repository includes CI workflows to enforce strict Gitflow pull-request flow:

- `gitflow-guard` (`.github/workflows/gitflow-guard.yml`)
- `develop-pr-go-reviewer` (`.github/workflows/develop-pr-go-reviewer.yml`)

### Required repository settings

To enforce this strictly, configure branch protection (or rulesets) for `develop` and `main`:

1. Require a pull request before merging.
2. Block direct pushes (including admins if you want absolute enforcement).
3. Require status checks to pass before merge.
4. Mark these checks as required:
   - **Gitflow Guard / Validate Gitflow branch mapping**
   - **Develop PR Go Reviewer / Run go-pr-reviewer**
5. Add repository secret `COPILOT_GITHUB_TOKEN` with the **Copilot Requests** permission.

## Coding Standards

- **Go style:** `PascalCase` for exported methods/structs, `camelCase` for private helpers/variables.
- **Concurrency rule:** Always guard shared library map access with `sync.RWMutex`.
- **Frontend style:** Use Vue 3 `<script setup lang="ts">` and talk to Go via the shared `fetch` / WebSocket client in `frontend/src/transport/`.

## Troubleshooting / FAQ

**Why is RAM usage high?**  
Kestrel intentionally keeps metadata and thumbnails in memory to remove interaction-time I/O and keep navigation fast.

**Why can startup feel heavier than browsing?**  
Startup performs the expensive loading step up front so runtime interactions stay smooth afterward.

**Why can opening a full image still be slower than scrolling?**  
Browsing uses in-memory/thumbnail data, but opening full-resolution files may read from HDD/NAS on demand.
