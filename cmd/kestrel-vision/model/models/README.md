# kestrel-vision models

This directory is the drop zone for the three ONNX model files the
sidecar loads:

| File                  | Model                 | Purpose              |
|-----------------------|-----------------------|----------------------|
| `scrfd_2.5g.onnx`     | SCRFD-2.5G            | Face detection + 5-pt landmarks |
| `arcface_r100.onnx`   | ArcFace r100 (buffalo_l) | 512-d face identity embedding |
| `yolov8n.onnx`        | YOLOv8n (80-class COCO) | Object detection |

## How they get here

1. **Build-time fetch (CI + release binaries):** `scripts/fetch-vision-models.sh`
   downloads each file from its upstream URL and verifies SHA-256 before
   writing it here.
2. **`//go:embed` picks them up.** The CGO build then bakes the bytes
   into the `kestrel-vision` binary — users get a single working
   artifact, no extra downloads.

## How they don't get here

Checked-in `.onnx` files are explicitly excluded by `.gitignore` so
git history stays small and license metadata stays with the upstream
sources rather than duplicated into every PR diff.

## Runtime override

A user can point `--models /path/to/alternate` at a directory
containing differently-named or differently-tuned model files; the
filesystem path takes priority over the embedded copy. Useful for
A/B testing a new model without rebuilding the binary.
