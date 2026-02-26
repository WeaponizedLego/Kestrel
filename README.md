# 📸 Kestrel

Kestrel is a high-performance desktop photo manager built for very large libraries (20,000+ images), including collections stored on slow HDDs or network drives.

## Table of Contents

- [Philosophy: Video Game Architecture](#philosophy-video-game-architecture)
- [Core Features](#core-features)
  - [1) In-Memory Truth](#1-in-memory-truth)
  - [2) Zero-Latency Interaction](#2-zero-latency-interaction)
  - [3) Slow Drive Strategy](#3-slow-drive-strategy)
  - [4) Persistence with `library.gob`](#4-persistence-with-librarygob)
- [Tech Stack](#tech-stack)
- [Runtime Flow](#runtime-flow)
- [Getting Started (Dev)](#getting-started-dev)
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

## Tech Stack

- **Frontend:** Vue 3 (Composition API) + Vite
- **Backend:** Go (Golang) for scanning, hashing, thumbnail workflow, and memory management
- **Desktop Bridge:** [Wails v2](https://wails.io/)
- **Concurrency Model:** Go maps protected by `sync.RWMutex`

## Runtime Flow

1. Launch app and load persisted state into memory.
2. Interact with the library from the in-memory map for low-latency browsing.
3. Access original files from HDD/NAS only when opening full-resolution images.
4. Persist updated state on exit or manual sync to `library.gob`.

## Getting Started (Dev)

### Prerequisites

- Go toolchain
- Node.js and npm
- Wails v2 CLI

### Run in development mode

```bash
wails dev
```

## Coding Standards

- **Go style:** `PascalCase` for exported methods/structs, `camelCase` for private helpers/variables.
- **Concurrency rule:** Always guard shared library map access with `sync.RWMutex`.
- **Frontend style:** Use Vue 3 `<script setup lang="ts">` and call Go bindings via `wailsjs`.

## Troubleshooting / FAQ

**Why is RAM usage high?**  
Kestrel intentionally keeps metadata and thumbnails in memory to remove interaction-time I/O and keep navigation fast.

**Why can startup feel heavier than browsing?**  
Startup performs the expensive loading step up front so runtime interactions stay smooth afterward.

**Why can opening a full image still be slower than scrolling?**  
Browsing uses in-memory/thumbnail data, but opening full-resolution files may read from HDD/NAS on demand.
