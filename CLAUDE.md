# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repo status

**This repo is docs-only.** There is no Go code, no `go.mod`, no `frontend/`, no `package.json` yet — only design documents in `README.md`, `docs/`, and CI workflows in `.github/`. When implementation starts, follow the architecture and package layout described in `docs/system-design.md`; do not invent a different structure.

## Architecture in one paragraph

Kestrel is a desktop photo manager for very large libraries (20K–1M+ images), built on a **"video game" architecture**: all metadata is loaded into an in-memory `map[string]*Photo` guarded by `sync.RWMutex` at startup, and scroll/sort/filter never touch disk. Thumbnails are persisted in a custom packed binary file (`thumbs.pack`) plus index, and served from a memory-budgeted priority-aware LRU cache fed by a pre-fetcher goroutine pool that predicts viewport motion. Persistence is split: `library_meta.gob` (small, loaded sync at startup) + `thumbs.pack` (large, index-only at startup, pixel data on demand).

The UI shell is **not** a webview framework. The Go binary starts a `net/http` server bound to `127.0.0.1:0`, embeds the built Vue frontend via `//go:embed frontend/dist/*`, generates a per-run auth token, and opens the user's default browser at the loopback URL. Frontend talks to backend over REST (commands) + a single WebSocket endpoint (server-pushed events like `scan:progress`, `thumbnail:ready`). The Vue app uses **manual island hydration** — a build-time-rendered static shell with one Vue app mounted per interactive region, not one root hydrating everything.

## Planned package layout (when coding starts)

- `cmd/kestrel/main.go` — wire internal packages, start server, launch browser
- `internal/library/` — in-memory photo store (`map` + `RWMutex`), pre-sorted indices
- `internal/scanner/` — directory walk + fixed worker pool (`runtime.NumCPU()`)
- `internal/thumbnail/` — `ThumbnailProvider` interface, LRU, `thumbs.pack`, pre-fetcher
- `internal/metadata/` — EXIF/file metadata extraction
- `internal/persistence/` — `.gob` serialization (metadata only)
- `internal/server/` — `net/http` router, middleware, WebSocket hub
- `internal/api/` — HTTP handlers (thin: decode → call domain → encode)
- `internal/assets/` — `//go:embed` glue for the built frontend
- `internal/platform/` — OS-specific helpers (browser launch, paths, memory detection)
- `frontend/src/islands/` — one Vite entry per interactive region
- `frontend/src/transport/` — shared `fetch` client + WebSocket singleton
- `frontend/src/shell/` + `frontend/scripts/` — build-time shell renderer using `@vue/server-renderer`

`internal/api/` and `internal/server/` are separated on purpose: handlers don't import the server, they register against its router.

## Common commands (per design docs — no build system exists yet)

```bash
# Dev: two processes, Vite proxies /api and /ws to Go
cd frontend && npm install && npm run dev   # terminal 1
go run ./cmd/kestrel --dev                  # terminal 2 (skips browser launch, skips embed)

# Production binary
cd frontend && npm run build                # emits frontend/dist/
go build -ldflags="-s -w" -o kestrel ./cmd/kestrel
```

Cross-compile matrix in CI: `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, `windows/amd64`. Stay **CGO-free** — no webview, pure-Go image libs — so cross-compilation is clean from any host.

## Code quality baseline

Write for the next human to read the file, not for the compiler. Every change should leave the structure **more** coherent than it found it, not less.

- **Readability first.** Names describe intent, not type (`thumbnailBudget`, not `n` or `tb`). If a block needs a comment to explain *what* it does, rename or extract until it doesn't — reserve comments for *why*.
- **Small, single-purpose units.** Functions do one thing; packages own one responsibility; files group related concerns. When a function grows past the `go-readability` cap or mixes concerns, split it before adding more to it.
- **Robust seams over clever code.** Prefer an obvious data flow with clear ownership (who creates, who mutates, who closes) over a terser implementation that requires the reader to reason about invariants. Explicit types and early returns beat nested branching.
- **Domain logic stays in domain packages.** `internal/api/` handlers decode/encode only — they never grow business logic. `internal/server/` owns transport and nothing else. A misplaced responsibility is a structural bug; fix the placement, don't paper over it.
- **Fail loudly at boundaries.** Validate inputs at the edges (HTTP handlers, file parsers), trust internal contracts. Wrap every error with what you were doing and which thing you were doing it to, so the log line is self-explanatory without a stack trace.
- **No speculative abstraction.** Introduce an interface when a second consumer or a test needs it — not before. Three similar lines are better than a premature framework.
- **Leave it better than you found it.** If you touch a file and notice a poor name, a dead branch, or a misplaced helper, fix it in the same change (scoped to what you're already editing). Don't merge work that knowingly worsens the structure.

## Non-negotiable rules

- **In-memory truth.** Never query disk for metadata during UI interaction. Reads go to the in-memory map; thumbnail reads go to the LRU cache.
- **Concurrency.** Never expose the photo map directly. All access goes through `Library` methods that acquire `sync.RWMutex` (`Lock` for writes, `RLock` for reads). Snapshots return copies so the lock is released before the frontend receives data.
- **Worker pools sized to `runtime.NumCPU()`.** Never one goroutine per file.
- **Loopback only.** The HTTP server binds `127.0.0.1` (never `0.0.0.0`). Bind port `:0` and read back the actual port. Gate every `/api/*` and `/ws` request on the per-run auth token, and reject WS upgrades with a non-matching `Origin`.
- **Transport split.** All commands go through REST. The WebSocket is **one-way** (server → client) for events only: `scan:progress`, `thumbnail:ready`, `library:updated`.
- **Event hub indirection.** Scanner/pre-fetcher/persistence never write to WS connections. They publish typed `Event{Kind, Payload}` to the hub, which fans out to subscribers.
- **No JS-side sorting or filtering** over the photo list. Always call a REST endpoint that returns pre-sorted/filtered data from Go.
- **Error wrapping.** Never `return err` bare. Always `fmt.Errorf("<doing what> for <which thing>: %w", err)`. Lowercase, no trailing punctuation.
- **No `utils`/`helpers` packages.** One package = one responsibility.

## Go readability standards (from `docs/go-readability.md`)

- Functions ≤ 40 lines target, hard cap 60. Extract helpers.
- Exported types/funcs `PascalCase`; unexported `camelCase`; acronyms consistent (`ID`, `HTTP`, `URL`).
- Short, consistent receiver names (`(l *Library)`, `(s *Scanner)`).
- Doc comments on every exported symbol, starting with the symbol name.
- Inline comments explain *why*, not *what*.
- Table-driven tests; test files next to code; fixtures in `testdata/`.
- File order: package → imports → consts/vars → types → constructors → exported methods → unexported methods → standalone funcs.

## Gitflow & CI

Branch protection is enforced by two workflows in `.github/workflows/`:

- `gitflow-guard.yml` — `Gitflow Guard / Validate Gitflow branch mapping`
- `develop-pr-go-reviewer.yml` — `Develop PR Go Reviewer / Run go-pr-reviewer` (needs repo secret `COPILOT_GITHUB_TOKEN`)

PRs only; direct pushes to `develop` and `main` are blocked. Both checks are required before merge.

## Reference documents (read these before non-trivial work)

- `docs/system-design.md` — architecture, data flow, package layout, concurrency patterns, persistence, HTTP + WS server integration, performance targets
- `docs/ui-design.md` — Vue 3 + Vite island hydration, REST/WS transport, component hierarchy, thumbnail strategy, frontend performance rules
- `docs/visual-design.md` — dark neo-skeuomorphic tactile visual system: design tokens, elevation recipe, per-component specs
- `docs/go-readability.md` — Go style, naming, comments, error handling, interfaces, tests, file organization
- `.github/copilot-instructions.md` — the same "video game architecture" and transport rules, condensed for Copilot
