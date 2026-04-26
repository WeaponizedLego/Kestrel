# Scanner & Re-scan Behavior

Reference for how Kestrel's three filesystem-walking flows work, what they share, and the edge cases — especially around nested roots — that are not obvious from the code.

> Mental model: **scanning is "discover and upsert"; pruning is "reconcile to disk truth"**. All three flows share one walk implementation; they differ in scope, intensity, preemption, and whether they prune.

## The shared walk

All flows ultimately call `Scanner.Scan()` (`internal/scanner/scanner.go:155`), which spawns a producer/consumer pair:

- `walkPaths()` (`internal/scanner/scanner.go:213`) drives `filepath.WalkDir` from the root and pushes supported media paths onto a buffered channel (cap 128).
- `processPaths()` (`internal/scanner/scanner.go:252`) drains the channel with a worker pool sized to `runtime.NumCPU()` (normal) or `NumCPU/4` (low intensity).
- Each worker runs `unchanged()` (`internal/scanner/scanner.go:347`) — a size+mtime fast-path against the in-memory library — and skips fully-known files. Otherwise it stats, hashes (SHA-256), extracts EXIF, derives auto-tags, optionally generates a thumbnail, and calls `library.AddPhoto()`.
- Progress is batched every 25 files via `publishProgress()` (`internal/scanner/scanner.go:362`) so the WS hub isn't flooded.

Behaviors worth knowing:

- **Recursion is unconditional.** `WalkDir` descends every subdirectory, no depth limit, no hidden-folder skip. The `.`-prefix filtering you'll see in `internal/api/library.go:632,658` belongs to the folder browser, not the scanner.
- **Per-file errors abort the root.** The walk callback returns errors as-is (`internal/scanner/scanner.go:218`); one unreadable subdirectory ends that root's scan.
- **Writes are additive.** Photos are keyed by absolute path. Scanning the same tree twice overwrites identical entries; nothing is removed by a scan.

## Initial scan — `POST /api/scan`

`internal/api/library.go:1017`. User-triggered, single folder, full intensity.

- Calls `runner.Start(folder)` (`internal/scanner/runner.go:98`) — preempts any low-intensity scan, rejects if another normal-intensity scan is running (409 with `ErrScanInProgress`).
- After the scan starts, the handler attempts to register the folder as a watched root via `roots.Upsert(req.Folder)` (`internal/api/library.go:1055`) **unless** `isCoveredByExistingRoot` (`internal/api/library.go:1071`) reports the folder is already inside an existing root.
- A failing `Upsert` does not break the scan — the user's immediate action succeeds, the folder just won't appear in the background scheduler's list until the store becomes writable.
- Events emitted by `Runner.run()` (`internal/scanner/runner.go:249`): `scan:started`, `scan:progress` (batched), `scan:done`, `library:updated`. `OnFinish` flushes `library_meta.gob` and invalidates the cluster cache.

## User re-scan — `POST /api/rescan`

`internal/api/library.go:1111`. The "keep me in sync with disk" button.

- Body: `{folder?: string}`. Empty/omitted folder → sweep every watched root.
- `rescanPlan()` (`internal/api/library.go:1147`) is intentionally dumb: an explicit folder is taken verbatim with no validation against the watch list; an empty folder expands to `roots.List()` in store order.
- `runRescan()` (`internal/api/library.go:1167`) walks each root sequentially, waiting for each to finish via `runner.WaitForActive()` before starting the next. After each walk it runs `lib.PruneMissingUnder(root, fileExists)` (`internal/library/library.go:217`) and publishes `library:updated` if anything was pruned.
- Refuses (409) if a normal-intensity scan is already running; an in-flight low-intensity scan is allowed because `runner.Start` will preempt it.
- Events: `rescan:progress {phase: "scan" | "prune", root_index, root_total, current_root}` per root, terminal `rescan:done {roots, pruned, requested}`.

## Background scheduler

`internal/rescan/scheduler.go`. Continuously reconciles the watch list to disk.

- `Run()` (line 107) ticks every `Interval` (default 15 min). `cycle()` (line 124) gates on:
  - User idle for at least `IdleThreshold` (default 2 min).
  - No scan currently running.
- Sorts watched roots by `LastScannedAt` (oldest first) and iterates, with a `PerRootGap` (default 30 s) between them.
- `rescanOne()` (line 167): `runner.StartLowIntensity()` (`NumCPU/4` workers, 10 ms per-file sleep) → `WaitForActive()` → `PruneMissingUnder` → publish `library:updated` if pruned → `Roots.MarkScanned(root, time.Now())`.
- A user-triggered scan or rescan immediately preempts a running low-intensity sweep via `Runner.PreemptLowIntensity` (`internal/scanner/runner.go:167`).

## Side-by-side

| | Initial scan | User re-scan | Background scheduler |
|---|---|---|---|
| Endpoint / trigger | `POST /api/scan` | `POST /api/rescan` | tick (15 min) |
| Scope | one folder | one folder or all roots | one root per cycle |
| Intensity | normal | normal | low |
| Concurrency rule | rejects if normal scan running; preempts low | rejects if normal scan running; preempts low | yields to any user-triggered scan |
| Adds watch root | yes (unless covered) | no | no |
| Prunes missing files | no | yes (`PruneMissingUnder`) | yes (`PruneMissingUnder`) |
| Events | `scan:*`, `library:updated` | `rescan:progress`, `rescan:done`, `library:updated` | `library:updated` only |
| Side effect on `watchroots.json` | `Upsert` (boundary-safe, child-into-parent collapsed) | none | `MarkScanned` timestamp |

## Nesting behavior

The piece that is hardest to re-derive from code.

### The walk doesn't know about watch roots

`filepath.WalkDir` recurses unconditionally from whichever path is passed in. Watch roots are a registration concept; the walker has no opinion about them.

### Child after parent — collapsed

Sequence: `/Photos` is already watched, user clicks Scan on `/Photos/2025`.

1. `runner.Start("/Photos/2025")` runs immediately (`internal/api/library.go:1032`) — the nesting check happens *after* the scan kicks off, not before.
2. The walk is mostly free: every file is already in the in-memory library (keyed by absolute path) and `unchanged()` skips hashing, EXIF, autotag, and thumbnail generation. New files added since the last sweep do get inserted.
3. `isCoveredByExistingRoot("/Photos/2025")` (`internal/api/library.go:1071`) builds `target = "/Photos/2025/"` and checks each watched root with `strings.HasPrefix(target, root+sep)`. Because `/Photos/` is a prefix of `/Photos/2025/`, the function returns true.
4. The condition at line 1054 (`!isCoveredByExistingRoot`) becomes false, so `roots.Upsert` is **skipped**. The doc comment at lines 1046–1053 is the rationale: nested watch roots make every background cycle walk the same tree twice.
5. The handler still returns `202 {id, root: "/Photos/2025"}` (line 1064). The HTTP response does not signal that the upsert was skipped — from the UI it looks identical to scanning a brand-new folder.

Boundary check: trailing-separator normalization (`internal/api/library.go:1077,1079`) means `/Photos/` covers `/Photos/2025/` but does **not** falsely cover `/PhotosArchive/`.

### Parent after child — not collapsed

Sequence: `/Photos/2025` was registered first (e.g. by an earlier scan). User now scans `/Photos`.

- `isCoveredByExistingRoot("/Photos")` builds `target = "/Photos/"` and checks each root with `HasPrefix(target, root+sep)`. For root `/Photos/2025`, that is `HasPrefix("/Photos/", "/Photos/2025/")` — false.
- Coverage is **not** detected. `roots.Upsert("/Photos")` runs and the store now contains both entries.
- `Upsert` (`internal/watchroots/store.go:82`) only deduplicates by exact path equality. There is no compaction pass to merge a parent over its children.
- Going forward, the background scheduler walks both `/Photos` and `/Photos/2025` every cycle. The second pass is mostly free thanks to the `unchanged()` fast-path, but it still recurses, stats every file, and runs its own `PruneMissingUnder`.

### Pruning is strictly prefix-scoped

`PruneMissingUnder` (`internal/library/library.go:217`) computes `prefix = root + sep` and only considers photos whose absolute path has that prefix. Consequences:

- Re-scanning `/Photos` prunes everything under `/Photos/**`, including any subtree that used to be its own watch root and is gone from disk.
- Re-scanning `/Photos/2025` only prunes inside `/Photos/2025/**`. Deletions in a sibling like `/Photos/2024` are untouched, which is the deliberate trade-off — a transient stat error inside one root cannot cascade into dropping photos from unrelated roots.

### Removing a covering root strands descendants

If `/Photos/2025` was added under the cover of `/Photos` (so it is *not* in the store), and the user later removes `/Photos` via `DELETE /watched-roots`, there is no logic that promotes `/Photos/2025` to a top-level root. The background scheduler stops sweeping that subtree, and entries persist in the in-memory map until something else (a manual scan or rescan, a fresh process start) reconciles them.

## Lifecycle and preemption

`Runner` (`internal/scanner/runner.go`) holds at most one `scanHandle` at a time, mutex-guarded.

- `Start()` (line 98) — full intensity. If a low-intensity scan is running, it is preempted via `PreemptLowIntensity` (line 167) and waited out before the new scan begins.
- `StartLowIntensity()` (line 116) — fails fast with `ErrScanInProgress` if **any** scan is already running.
- `Cancel()` (line 187) — flips the active scan's `context.CancelFunc`. Cooperative — workers see `ctx.Err()` between files.
- `WaitForActive()` (line 221) — blocks until the active scan finishes. Used by the user re-scan and the background scheduler to serialize per-root work.

## Practical guidance

- **Prefer one deep root.** A single `/Photos` watch root means one walk per cycle, full prune coverage, and follow-up scans on subfolders won't pollute the list (`isCoveredByExistingRoot` collapses them).
- **Don't manually create overlapping roots.** Adding `/Photos/2025` before `/Photos` leaves both in the store forever — there is no compaction. The redundant walk is cheap, but it's still wasted effort every 15 minutes.
- **Scoped re-scans are safe.** Because `PruneMissingUnder` is prefix-scoped, you can re-scan a subfolder without worrying about cascading deletes elsewhere.
- **Watch out for "I removed the parent root."** Descendants that were under the cover of that root keep living in memory until something else reconciles them.

## See also

- `docs/system-design.md` — overall "video game architecture", persistence layout, concurrency rules.
- `internal/scanner/scanner.go`, `internal/scanner/runner.go` — the walk and the lifecycle.
- `internal/rescan/scheduler.go` — the background loop.
- `internal/api/library.go:1017,1071,1111,1147,1167` — the two HTTP entry points and their helpers.
- `internal/library/library.go:217` — `PruneMissingUnder` and its prefix-scope contract.
- `internal/watchroots/store.go` — the durable list and its dedup semantics.
