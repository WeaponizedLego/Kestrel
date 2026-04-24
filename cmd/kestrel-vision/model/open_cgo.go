//go:build cgo

package model

// The three Open* functions in this file load ONNX sessions for the
// sidecar's three models. They are the one place where the binary
// touches ONNX Runtime; everything else in cmd/kestrel-vision/ talks
// to them through the interfaces in model.go.
//
// *** This file is a SKELETON. ***
// The exported signatures, struct shapes, and TODO comments describe
// exactly what Phase 1 of TASKS.md needs to fill in. A developer with
// github.com/yalue/onnxruntime_go installed and the three .onnx model
// files in modelsDir (or embedded) can flesh these out without
// touching any other file in the repo.

import (
	"errors"
	"image"

	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/align"
	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/nms"
	"github.com/WeaponizedLego/kestrel/cmd/kestrel-vision/preprocess"
)

// ErrNotImplemented marks the skeleton entry points. Replaced by
// real implementations in Phase 1.
var ErrNotImplemented = errors.New("onnx model loader not implemented yet (see TASKS.md Phase 1)")

// --- Face detection (SCRFD) -------------------------------------

// OpenFaceDetector loads SCRFD-2.5G from modelsDir (or the embedded
// default when modelsDir is empty). Returns a ready FaceDetector
// that runs inference and post-processing under the hood.
func OpenFaceDetector(modelsDir string) (FaceDetector, error) {
	// TODO(phase-1):
	//   1. Resolve model path: modelsDir/scrfd_2.5g.onnx or //go:embed.
	//   2. Create an onnxruntime_go session.
	//   3. Return &scrfdDetector{session: sess}.
	return nil, ErrNotImplemented
}

// scrfdDetector is the skeleton implementation.
type scrfdDetector struct {
	// session *ort.DynamicAdvancedSession  // from onnxruntime_go
}

// Detect runs SCRFD on img and returns pixel-space face boxes. The
// full flow is documented inline so the Phase-1 author does not have
// to reconstruct it.
func (d *scrfdDetector) Detect(img image.Image) ([]FaceBox, error) {
	// 1. preprocess.SCRFDInput(img) → (tensor, letterbox geometry).
	// 2. Run the ONNX session; outputs are score maps + bbox deltas +
	//    landmark deltas across three feature-map strides (8, 16, 32).
	// 3. Decode each stride: for every anchor cell with score ≥ 0.5,
	//    apply the bbox delta and landmark deltas to get candidate
	//    predictions in letterboxed coordinates.
	// 4. nms.Apply(candidates, 0.4).
	// 5. For each kept candidate: un-letterbox bbox + landmarks using
	//    preprocess.UnLetterbox and the geometry from step 1.
	// 6. Assemble FaceBox entries and return.
	_ = preprocess.SCRFDInput
	_ = preprocess.UnLetterbox
	_ = nms.Apply
	_ = align.Landmarks{}
	return nil, ErrNotImplemented
}

func (d *scrfdDetector) Name() string   { return "scrfd-2.5g" }
func (d *scrfdDetector) Close() error   { return nil }

// --- Face embedding (ArcFace) -----------------------------------

// OpenFaceEmbedder loads ArcFace r100 (buffalo_l). The embedder is
// the one model that does NOT accept a raw image — the pipeline
// aligns and preprocesses a 112×112 face crop via align.FaceCrop +
// preprocess.ArcFaceInput first.
func OpenFaceEmbedder(modelsDir string) (FaceEmbedder, error) {
	// TODO(phase-1): load arcface_r100.onnx into a session and
	// return &arcfaceEmbedder{session: sess}.
	return nil, ErrNotImplemented
}

type arcfaceEmbedder struct {
	// session *ort.DynamicAdvancedSession
}

// Embed accepts the exact tensor produced by preprocess.ArcFaceInput
// (CHW float32, normalised to (x-127.5)/127.5). Returns a 512-d
// L2-normalised embedding suitable for cosine-similarity clustering.
func (e *arcfaceEmbedder) Embed(chwTensor []float32) ([]float32, error) {
	// 1. Run the session on the tensor (shape [1, 3, 112, 112]).
	// 2. Output is [1, 512]; extract as []float32.
	// 3. L2-normalise in place (divide by sqrt of sum of squares).
	// 4. Return the 512-d slice.
	return nil, ErrNotImplemented
}

func (e *arcfaceEmbedder) Name() string { return "arcface-r100" }
func (e *arcfaceEmbedder) Close() error { return nil }

// --- Object detection (YOLOv8n) ---------------------------------

// OpenObjectDetector loads YOLOv8n (80-class COCO). See pipeline
// expectations in Detect.
func OpenObjectDetector(modelsDir string) (ObjectDetector, error) {
	// TODO(phase-1): load yolov8n.onnx and return &yolov8Detector{}.
	return nil, ErrNotImplemented
}

type yolov8Detector struct {
	// session *ort.DynamicAdvancedSession
}

// Detect runs YOLOv8n and returns labelled object hits.
func (d *yolov8Detector) Detect(img image.Image) ([]ObjectHit, error) {
	// 1. preprocess.YOLOv8Input(img) → (tensor, letterbox geometry).
	// 2. Run session. Output shape is [1, 84, 8400]: 4 bbox coords
	//    + 80 class confidences across 8400 grid cells.
	// 3. For each cell: find the max-confidence class. If ≥ 0.25,
	//    emit a candidate (bbox, class, score).
	// 4. nms.Apply(candidates, 0.45).
	// 5. Un-letterbox each kept bbox; map class index → COCO label
	//    using the cocoLabels table below.
	return nil, ErrNotImplemented
}

func (d *yolov8Detector) Name() string { return "yolov8n" }
func (d *yolov8Detector) Close() error { return nil }

// cocoLabels is the index→name map YOLOv8n was trained on. Kept
// here so the pipeline never writes magic numbers. Order matches
// the class index in the output tensor.
var cocoLabels = [80]string{
	"person", "bicycle", "car", "motorcycle", "airplane", "bus", "train",
	"truck", "boat", "traffic light", "fire hydrant", "stop sign",
	"parking meter", "bench", "bird", "cat", "dog", "horse", "sheep",
	"cow", "elephant", "bear", "zebra", "giraffe", "backpack", "umbrella",
	"handbag", "tie", "suitcase", "frisbee", "skis", "snowboard",
	"sports ball", "kite", "baseball bat", "baseball glove", "skateboard",
	"surfboard", "tennis racket", "bottle", "wine glass", "cup", "fork",
	"knife", "spoon", "bowl", "banana", "apple", "sandwich", "orange",
	"broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair",
	"couch", "potted plant", "bed", "dining table", "toilet", "tv",
	"laptop", "mouse", "remote", "keyboard", "cell phone", "microwave",
	"oven", "toaster", "sink", "refrigerator", "book", "clock", "vase",
	"scissors", "teddy bear", "hair drier", "toothbrush",
}

// cocoLabel returns the class name for a class index, with a safe
// fallback for out-of-range values so a model-version mismatch can't
// crash the pipeline.
func cocoLabel(i int) string {
	if i < 0 || i >= len(cocoLabels) {
		return "unknown"
	}
	return cocoLabels[i]
}

// Suppress unused-import warnings in the skeleton. Deleted once the
// TODOs land.
var _ = cocoLabel
