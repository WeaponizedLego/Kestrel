# kestrel-vision — Implementation Plan

> Turning the `cmd/kestrel-vision/` scaffold into a working ML sidecar that
> Kestrel core already speaks to. Core is done; everything below is inside
> the sidecar and its build pipeline.

## Decisions (locked)

- **Runtime:** `github.com/yalue/onnxruntime_go` (CGO, wraps Microsoft ONNX
  Runtime's C API).
- **Models:**
  - Face detection — **SCRFD-2.5G** (5-point landmarks included).
  - Face embedding — **ArcFace r100 buffalo_l** (512-d, L2-normalised).
  - Object detection — **YOLOv8n** (80-class COCO).
- **Distribution:** binary size is not a concern; embed models at build
  time via `//go:embed` so users get a single artifact per platform.
- **CGO split:** sidecar builds keep a pure-Go stub behind `//go:build !cgo`
  that returns empty responses. The real pipeline is `//go:build cgo`. The
  existing `_build-vision.yml` (CGO=0) keeps passing until we flip it to
  the native-matrix workflow.
- **Transport:** keep current `POST /detect` (bytes). Add `POST /detect/path`
  later as an optimisation — same-host, avoids pixel round-trip.

---

## Phase 0 — Refactor for growth  *(this PR / can do without ONNX installed)*

Pull the handler wiring out of `main.go` and introduce a `Pipeline` interface
so the ONNX implementation can land behind a build tag without churn.

- [x] Split `cmd/kestrel-vision/main.go` into `main.go` (wiring) + `server.go`
      (handlers + auth middleware).
- [x] Define `Pipeline` interface: `Detect(img image.Image) (protocol.DetectResponse, error)`.
- [x] `pipeline_stub.go` (`//go:build !cgo`) — empty result, logs once.
- [x] `pipeline_cgo.go` (`//go:build cgo`) — skeleton calling the three model wrappers.
- [x] Pure-Go helper packages under `cmd/kestrel-vision/`:
  - `preprocess/` — resize, letterbox, HWC→CHW, normalise. No CGO.
  - `nms/` — sort-by-confidence + IoU suppression. No CGO.
  - `align/` — 5-landmark 2D similarity transform for ArcFace crops. No CGO.
- [x] Table-driven tests for each helper (no model weights needed).

## Phase 1 — ONNX plumbing  *(done at the code level; runtime needs models)*

- [x] `go get github.com/yalue/onnxruntime_go` (added as direct dependency).
- [x] `model/runtime_cgo.go` — env init/destroy refcount, shared-library
      path discovery (`ORT_SHARED_LIBRARY` env + sidecar-adjacent fallback),
      model-path resolver.
- [x] `model/detector_cgo.go` — SCRFD session wrapper. Allocates 9 output
      tensors (3 strides × {score, bbox, kps}), decodes anchors across all
      three strides, NMS with IoU 0.4, un-letterboxes to original pixels,
      caps at 50 faces per image.
- [x] `model/embedder_cgo.go` — ArcFace r100 session wrapper. Fixed
      [1,3,112,112] input / [1,512] output, L2-normalises output in place.
- [x] `model/objects_cgo.go` — YOLOv8n session wrapper. Decodes the 84×8400
      channel-major output, per-class NMS (IoU 0.45, conf 0.25), Top-K 100,
      un-letterboxes, maps class indices to COCO labels.
- [x] `pipeline_cgo.go` orchestration — already wired in Phase 0.
- [ ] **Blocked on Phase 2** — one end-to-end test against a tiny committed
      fixture image (known faces, known objects). Needs the real `.onnx`
      files, so it lands together with the model-fetch script.

**Runtime state:** under CGO=1 builds, the sidecar binary compiles and runs.
Start it with `ORT_SHARED_LIBRARY=/path/to/libonnxruntime.so
./kestrel-vision --models /path/to/models-dir` once you have the three files.
A missing model file errors out at startup with a clear message.

## Phase 2 — Ship the weights  *(plumbing done; URLs/checksums need live pinning)*

- [x] `//go:embed` the three `.onnx` files from
      `cmd/kestrel-vision/model/models/`. Uses `embed.FS`; a blank dir
      (checkout before first fetch) doesn't break the build because
      `README.md` is always embeddable.
- [x] SHA-256 verification (`verifySHA`) with graceful degradation:
      empty expected value skips the check, non-empty rejects mismatches.
      `modelSHA` entries start blank; fill in after Phase 2 closes.
- [x] `scripts/fetch-vision-models.sh` — idempotent, verifies SHA-256,
      exits non-zero on placeholder hashes so a first-time run
      announces loudly that the script needs real values filled in.
- [x] `cmd/kestrel-vision/model/models/.gitignore` excludes `*.onnx`.
- [x] Runtime override: `--models /path/to/dir` takes priority over
      embedded copy so a dev can A/B-test a new model without rebuild.
- [x] Tests: filesystem-wins-over-embedded, missing-everywhere error
      message, SHA-256 skip + mismatch behaviour.
- [x] `_build-vision.yml` runs the fetch script before `go build`.
- [x] Script restructured to take URLs from env vars
      (`KESTREL_SCRFD_URL`, `KESTREL_ARCFACE_URL`, `KESTREL_YOLO_URL`)
      instead of hard-coded entries — "the right mirror" drifts over
      time, and letting the build owner supply URLs via secrets keeps
      the script from carrying stale links in git. Three modes: verify
      if files present, fetch if URL supplied, print hints otherwise.
- [ ] **Needs human + network:** pick mirrors for each of the three
      models, stash the URLs as GitHub Actions repo secrets with the
      names above, then run the fetch locally once to capture the
      three SHA-256 values. Paste them into:
      - `scripts/fetch-vision-models.sh` → expected-hash column
      - `cmd/kestrel-vision/model/embed_cgo.go` → `modelSHA` map
      Commit both together so every subsequent fetch/build enforces
      the pinned checksums.

**How to find the three models:**
- **SCRFD-2.5G:** search HuggingFace for `buffalo_m` (the pack that
  contains `det_2.5g.onnx` — NOT `buffalo_l` which has the bigger
  `det_10g.onnx`). Prefer org mirrors (`immich-app/*`, `deepghs/*`)
  over personal exports.
- **ArcFace r100:** search HuggingFace for `antelopev2` (contains
  `glintr100.onnx`, ~260 MB). The buffalo_l pack only has r50.
- **YOLOv8n:** do not use random HF mirrors. Export yourself:
  `pip install ultralytics && yolo export model=yolov8n.pt format=onnx
  imgsz=640 opset=12`. This downloads Ultralytics' own weights and
  produces a reproducible ONNX.

## Phase 3 — Flip CI to the real build

- [ ] `_build-vision.yml` switches to a native matrix:
      - `ubuntu-latest` (linux/amd64), `ubuntu-24.04-arm` or self-hosted arm64
      - `macos-13` (darwin/amd64), `macos-14` (darwin/arm64)
      - `windows-latest` (windows/amd64)
- [ ] Per-runner step that fetches the matching ONNX Runtime release tarball,
      sets `CGO_CFLAGS`/`CGO_LDFLAGS`, and copies the shared library into the
      artifact directory alongside the binary.
- [ ] `CGO_ENABLED: 1`. Remove the `!cgo` stub once every target builds green.

## Phase 4 — Signing (when you're ready to publish)

- [ ] macOS: codesign + notarise binary **and** the shared library. Apple
      Developer cert, `notarytool` submission.
- [ ] Windows: EV code-signing cert → sign `.exe` + `.dll`. SmartScreen
      reputation takes a few weeks to build regardless.
- [ ] Linux: AppImage signing is optional; desktop shortcut is nice.

## Phase 5 — Throughput (optional, ship first)

- [ ] Batch `/detect` calls into single ONNX session invocations.
- [ ] `POST /detect/path` for zero-copy same-host use.
- [ ] Warm sessions on startup with a dummy tensor to pay the first-run cost
      before the user's scan hits us.

---

## Where to resume

Phase 0 is scaffolded with tests. Phase 1 starts with `go get
github.com/yalue/onnxruntime_go` and opening `cmd/kestrel-vision/model/` —
the three wrapper files are skeletons with the shape documented but the ONNX
calls left to a dev who has the Runtime installed. Nothing else in the
repo needs to move.
