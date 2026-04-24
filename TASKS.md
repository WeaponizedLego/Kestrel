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

## Phase 1 — ONNX plumbing  *(needs ONNX Runtime installed)*

- [ ] `go get github.com/yalue/onnxruntime_go`.
- [ ] `model/detector.go` — SCRFD session wrapper, returns `[]FaceBox` with
      bbox + 5 landmarks + score. Post-process: decode anchor output, NMS.
- [ ] `model/embedder.go` — ArcFace session wrapper, returns 512-d L2-normalised
      vector for a pre-aligned 112×112 crop.
- [ ] `model/objects.go` — YOLOv8n session wrapper, returns `[]ObjectHit`
      with label + confidence + bbox. Post-process: decode output grid, NMS.
- [ ] `pipeline_cgo.go` orchestration:
      decode → SCRFD → align+crop per face → ArcFace → YOLOv8 → marshal.
- [ ] One end-to-end test against a tiny committed fixture image (known faces,
      known objects) with a tolerance on bbox coords and cosine similarity.

## Phase 2 — Ship the weights

- [ ] `//go:embed` the three `.onnx` files from `cmd/kestrel-vision/models/`.
      Document SHA-256 expected for each; reject mismatched bytes at startup
      (`sha256.Sum256` check before opening the session).
- [ ] `scripts/fetch-vision-models.sh` — downloads the three models to the
      embed directory. Checksums committed inline.
- [ ] `.gitignore` the `.onnx` files — they stay fetched, not committed.
- [ ] CI step in `_build-vision.yml` that runs the fetch script before `go build`.

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
