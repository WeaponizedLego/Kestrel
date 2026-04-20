# 🏷️ Assisted Tagging — Kestrel

> This document describes how Kestrel helps users tag large libraries without manually
> touching every photo. It is referenced from `README.md` and `docs/system-design.md`.

---

## Why This Exists

Tags are Kestrel's primary retrieval mechanism — but they are only useful **after** photos
have been tagged. A fresh import of 20K–1M images starts with zero tags, which puts the
entire value of the app behind a boring, manual task. Users give up before they start.

Assisted tagging closes that gap in three stacked layers. Each layer makes the *next click*
more productive than the last:

1. **Auto-derived tags at scan time** — free tags from EXIF, filesystem, and offline
   reverse-geocoding. Every photo ends up with several tags without the user doing anything.
2. **Perceptual-hash clustering** — group near-duplicates and visually-similar photos so the
   user tags a *group*, not a photo.
3. **Tagging queue UX** — a dedicated view that surfaces the largest untagged clusters first
   and turns one click into N tags.

A fourth layer — on-device semantic embeddings (CLIP-style) — is explicitly **out of scope
for MVP**. See [Future Work](#future-work) below.

---

## Design Constraints

Assisted tagging must honour the same hard constraints as the rest of Kestrel:

- **Pure Go, CGO-free.** No native runtimes, no shelled-out tools.
- **Single cross-platform binary** — Linux/macOS/Windows × amd64/arm64.
- **In-memory truth.** Tagging operations read/write the in-memory `Library` under the
  existing `sync.RWMutex` discipline. No per-photo disk queries during interaction.
- **No new scan-time I/O.** Derivation piggybacks on the existing EXIF-extraction and
  thumbnail-generation passes.

All dependencies introduced in this plan satisfy these constraints.

---

## Layer 1 — Auto-Derived Tags

Computed once per photo during the scan, stored on `Photo.AutoTags`, persisted in
`library_meta.gob`. Auto-tags are displayed distinctly in the UI so users can tell inferred
tags from their own.

### Sources

| Source            | Example tags                                                          | Cost         |
| ----------------- | --------------------------------------------------------------------- | ------------ |
| **EXIF**          | `camera:canon-eos-r5`, `lens:rf-24-70`, `year:2024`, `month:2024-06`, `iso:high`, `orientation:portrait`, `flash:on` | Already read |
| **Media kind**    | `kind:photo`, `kind:video`                                            | Free         |
| **Filesystem**    | `folder:vacation-italy` (opt-in, off by default)                      | Free         |
| **GPS → place**   | `place:rome`, `country:it`                                            | Offline lookup |

### GPS Reverse-Geocoding (Offline)

GPS-tagged photos are mapped to nearest city + country using an embedded
[GeoNames `cities500`](https://download.geonames.org/export/dump/) dataset (~10 MB
uncompressed). The dataset ships **inside** the binary via `//go:embed`, so there's no
network call and no external file to ship.

- Loaded once at startup into a pure-Go kd-tree (hand-rolled or `github.com/kyroy/kdtree`).
- Lookup is O(log n); trivial relative to scan cost.
- If GPS accuracy is poor or a city is missing, only `country:<iso>` is emitted.

### Storage

```go
type Photo struct {
    // ... existing fields ...

    Tags     []string // user-applied
    AutoTags []string // derived at scan time, regenerable
    PHash    uint64   // perceptual hash (Layer 2)
}
```

**Why a separate `AutoTags` field (not an `auto:` prefix in `Tags`):**

- Regeneration is safe: rebuild `AutoTags` from scratch without ever touching `Tags`.
- UI filtering is simpler: two visually distinct chip styles, no substring-matching.
- Persistence cost is trivial (short strings, high dedup potential via interning).

### Dependencies (pure Go, CGO-free)

- `github.com/dsoprea/go-exif/v3` *or* `github.com/rwcarlsen/goexif` for EXIF parsing.
  (Picked during implementation; both are pure Go.)
- GeoNames `cities500.txt` embedded asset — not a code dependency.
- Small kd-tree for nearest-neighbour; hand-roll unless `kyroy/kdtree` is preferred.

### Package

```
internal/metadata/autotag/
  derive.go        // Derive(meta ExifMetadata, fs FileInfo, geo *geoindex.Index) []string
  geoindex/
    geoindex.go    // embedded dataset + kd-tree lookup
```

Called inside the existing scanner worker pool, in the same pass that extracts EXIF.
No new goroutines, no new disk I/O.

---

## Layer 2 — Perceptual-Hash Clustering

Groups photos that *look* alike so the user tags a cluster in one action instead of
clicking 500 images.

### pHash Computation

- **64-bit pHash** per photo via `github.com/corona10/goimagehash` (pure Go, CGO-free).
- Computed **during thumbnail generation**, reusing the already-decoded image. Zero new
  I/O, near-zero CPU overhead relative to JPEG decode.
- Stored as `PHash uint64` on `Photo`, persisted in `library_meta.gob`.

### Clustering Queries

Two distance thresholds produce two cluster views:

| View              | Hamming distance | Intent                                         |
| ----------------- | ---------------- | ---------------------------------------------- |
| `duplicate`       | ≤ 5              | Bursts, edits, re-saves, same-scene duplicates |
| `similar`         | ≤ 12             | Looser visual groups (same subject / scene)    |

Thresholds are hardcoded defaults for v1; the config file exposes overrides for power
users. They are **not** surfaced in the UI until real usage data suggests they should be.

### Algorithm

- Union-find over pairs of photos within the distance threshold.
- A **sorted-hash prefilter** prunes candidates: photos with grossly different
  high-order bits cannot be within distance N of each other for small N. This avoids O(n²)
  comparisons and keeps clustering on 1M photos sub-second on a modern machine.
- Results are cached in-memory; invalidated when photos are added/removed.

### Package

```
internal/library/cluster/
  phash.go        // pHash storage + fast Hamming distance
  cluster.go      // union-find, prefilter, cluster cache
```

### Transport

- **REST:** `GET /api/clusters?kind=duplicate|similar&minSize=N` → paginated clusters.
- **Behaviour on first call:** if the in-memory cluster cache is cold, the handler kicks
  off a background compute and returns `202 Accepted` with a progress payload.
- **WS event:** `clusters:ready` is broadcast on the hub when the background compute
  finishes, so the UI can refresh without polling.

Commands (like "apply these tags to this cluster") still go through REST — the WS remains
one-way, server→client, per the transport rules in `system-design.md`.

---

## Layer 3 — Tagging Queue UX

A dedicated UI surface that turns tagging into a guided, keyboard-driven flow. Built as a
new Vue island so it's lazily loaded and doesn't weigh on the main grid.

### Backend (thin handlers in `internal/api/`)

| Endpoint                    | Purpose                                                                 |
| --------------------------- | ----------------------------------------------------------------------- |
| `GET /api/tagging/queue`    | Paginated list of **untagged** clusters, sorted by size desc, recency. Each entry: cluster ID, representative photo path, member count, auto-tags already on the group. |
| `POST /api/tagging/apply`   | `{clusterId, tags[], scope: "cluster" \| "similar"}`. Applies tags to every member under the library write lock. Publishes `library:updated`. |
| `GET /api/tagging/progress` | `{totalPhotos, untaggedPhotos, taggedPhotos, largestUntaggedCluster}` — drives the progress HUD. |

### Frontend (`frontend/src/islands/TaggingQueue/`)

- Vertical list of clusters, **largest first** (tagging the biggest group yields the
  highest clicks-to-coverage ratio).
- Each entry: representative thumbnail, member count, inherited auto-tag chips.
- Two primary actions per cluster:
  - **Tag this cluster** — applies to the tight (duplicate-threshold) group.
  - **Tag cluster + visually similar** — expands to the looser (similar-threshold)
    superset.
- Reuses `TagInput.vue` for chip entry.
- **Keyboard driven:** `j`/`k` to move, number keys bind recent tags, `enter` applies
  and advances to the next cluster.
- **Progress bar** pinned to the top: `12,340 / 48,200 photos tagged — 72 clusters left`.

### Design rule

Auto-tags are **suggestions**, never auto-applied to `Tags`. The user is always the one
confirming. Auto-tag chips render in a visually distinct style (per `visual-design.md`) so
the user can see at a glance what was inferred vs. what they confirmed.

---

## Data Flow

```
Scan (background)
  └─▶ EXIF extract
        └─▶ autotag.Derive(meta, fs, geoIdx) → photo.AutoTags
  └─▶ Thumbnail generate
        └─▶ goimagehash.PerceptionHash(img) → photo.PHash
  └─▶ Library.AddPhoto(photo)   // under write lock

First open of TaggingQueue
  └─▶ GET /api/clusters?kind=duplicate
        └─▶ cold cache → 202 + kick off background compute
              └─▶ on complete → hub.Broadcast(Event{Kind: "clusters:ready"})
        └─▶ warm cache → 200 + cluster list

User tags a cluster
  └─▶ POST /api/tagging/apply {clusterId, tags, scope}
        └─▶ library.ApplyTagsToCluster(...)  // write lock, atomic across all members
              └─▶ hub.Broadcast(Event{Kind: "library:updated"})
```

---

## Performance Targets

| Metric                          | Target                                                             |
| ------------------------------- | ------------------------------------------------------------------ |
| Auto-tag derivation             | < 100 µs / photo (dominated by EXIF parse, already in the budget)  |
| pHash computation               | Free (< 500 µs, shares decoded image with thumbnail generation)    |
| Cluster compute (cold, 1M photos) | < 10 sec background, non-blocking                                |
| Cluster compute (warm query)    | < 50 ms                                                            |
| Tag-apply on cluster of 10K     | < 200 ms under write lock                                          |

---

## Verification

Once implementation begins:

- **Layer 1:** Fixture folder under `testdata/` with varied EXIF (with/without GPS, video,
  missing fields). Table-driven tests assert derived tags match expectations. Known GPS
  coordinates verify `place:*` resolution.
- **Layer 2:** Seeded fixture of known bursts and known-distinct photos. Assert cluster
  membership at both thresholds. Benchmark clustering on 100K and 1M synthetic pHashes to
  confirm the sorted-prefix prefilter keeps it within target.
- **Layer 3:** End-to-end manual test — fresh library, open TaggingQueue island, tag the
  largest cluster, confirm the progress HUD updates and the state survives restart
  (`library_meta.gob` persistence).
- **Binary:** `go build` for all five target/arch combos from a single host; confirm no
  CGO warnings and all three features work with no external files.

---

## Future Work

### Semantic Content Tagging (CLIP-style embeddings)

**What:** On-device image embeddings to enable content search like `"beach at sunset"`,
`"dog in grass"`, `"receipt"`. Embeddings would be computed per-photo once and stored
alongside `PHash`; queries would nearest-neighbour search over a local index.

**Why deferred:** Every practical ML inference runtime in Go today requires either
CGO-linked ONNX/GGML bindings or a separately shipped runtime binary. Both break the
current **pure-Go, single-binary** guarantee.

**Revisit when:**

- MVP ships and we know whether the current three-layer system closes the tagging gap on
  its own.
- We're ready to make a conscious decision about which constraint to relax:
  - Accept CGO for a native ONNX runtime (loses clean cross-compilation).
  - Ship embeddings as a **separate optional binary** that Kestrel talks to over the
    existing HTTP transport, so the core stays pure Go.
  - Wait for a production-grade pure-Go inference path to mature.

Until then, Layers 1–3 are the plan.
